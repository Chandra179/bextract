package tier3

import (
	"context"
	"strings"
	"time"

	"bextract/internal/pipeline"
	"bextract/internal/tier2"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

const (
	defaultRenderTimeout = 8 * time.Second
	networkIdleWindow    = 500 * time.Millisecond
	defaultPoolSize      = 2
)

// Renderer executes JavaScript via a pooled Chrome browser and re-runs Tier 2
// extraction on the rendered DOM. A single Renderer is safe for concurrent use.
type Renderer struct {
	browser       *rod.Browser
	pool          rod.Pool[rod.Page]
	renderTimeout time.Duration
	analyzer      *tier2.Analyzer
}

// New launches a headless Chrome instance and returns a ready Renderer.
// Pass 0 for poolSize to use the default (2). Pass 0 for timeouts to use defaults.
// Returns an error if Chrome cannot be found or the browser fails to connect.
func New(poolSize int, renderTimeout, extractionTimeout time.Duration) (*Renderer, error) {
	if _, ok := launcher.LookPath(); !ok {
		return nil, errNoBrowser
	}
	if poolSize <= 0 {
		poolSize = defaultPoolSize
	}
	if renderTimeout == 0 {
		renderTimeout = defaultRenderTimeout
	}

	u, err := launcher.New().
		Headless(true).
		NoSandbox(true).
		Set("disable-gpu").
		Set("disable-dev-shm-usage").
		Set("disable-images").
		Launch()
	if err != nil {
		return nil, err
	}

	browser := rod.New().ControlURL(u)
	if err := browser.Connect(); err != nil {
		return nil, err
	}

	return &Renderer{
		browser:       browser,
		pool:          rod.NewPagePool(poolSize),
		renderTimeout: renderTimeout,
		analyzer:      tier2.New(extractionTimeout),
	}, nil
}

// Close shuts down the underlying Chrome browser.
func (r *Renderer) Close() error {
	return r.browser.Close()
}

// Render navigates to req.URL in a pooled Chrome page, waits for network idle,
// checks for escalation signals, then runs Tier 2 extraction on the rendered HTML.
func (r *Renderer) Render(ctx context.Context, req *pipeline.Request) *pipeline.RenderResult {
	start := time.Now()

	page, err := r.pool.Get(func() (*rod.Page, error) {
		return r.browser.Page(proto.TargetCreateTarget{URL: ""})
	})
	if err != nil {
		return escalateResult(req, "browser page creation failed: "+err.Error(), time.Since(start))
	}
	defer func() {
		_ = page.Navigate("about:blank")
		r.pool.Put(page)
	}()

	// Set up request interception to block heavyweight resources.
	router := page.HijackRequests()
	blockTypes := []proto.NetworkResourceType{
		proto.NetworkResourceTypeStylesheet,
		proto.NetworkResourceTypeImage,
		proto.NetworkResourceTypeMedia,
		proto.NetworkResourceTypeFont,
	}
	for _, rt := range blockTypes {
		rt := rt
		_ = router.Add("*", rt, func(h *rod.Hijack) {
			h.Response.Fail(proto.NetworkErrorReasonBlockedByClient)
		})
	}
	// Block known analytics/tracking domains regardless of resource type.
	analyticsPatterns := []string{
		"*google-analytics.com*",
		"*googletagmanager.com*",
		"*doubleclick.net*",
		"*facebook.com/tr*",
		"*hotjar.com*",
	}
	for _, pat := range analyticsPatterns {
		pat := pat
		_ = router.Add(pat, proto.NetworkResourceTypeDocument, func(h *rod.Hijack) {
			h.Response.Fail(proto.NetworkErrorReasonBlockedByClient)
		})
	}
	go router.Run()
	defer func() { _ = router.Stop() }()

	renderCtx, cancel := context.WithTimeout(ctx, r.renderTimeout)
	defer cancel()

	if err := page.Context(renderCtx).Navigate(req.URL); err != nil {
		return escalateResult(req, "navigation failed: "+err.Error(), time.Since(start))
	}

	// Wait for network to go idle (returns a wait func — call it to block).
	wait := page.WaitRequestIdle(networkIdleWindow, nil, nil, nil)
	waitDone := make(chan struct{})
	go func() {
		wait()
		close(waitDone)
	}()
	select {
	case <-waitDone:
	case <-renderCtx.Done():
		// Timeout is not fatal — proceed with whatever has loaded.
	}

	if reason := detectEscalation(page); reason != "" {
		return escalateResult(req, reason, time.Since(start))
	}

	html, err := page.HTML()
	if err != nil {
		return escalateResult(req, "HTML extraction failed: "+err.Error(), time.Since(start))
	}

	// Build a synthetic Tier 1 response from the rendered HTML and re-run Tier 2.
	syntheticResp := &pipeline.Response{
		OriginalRequest: req,
		StatusCode:      200,
		ContentType:     "text/html",
		Body:            []byte(html),
		FinalURL:        req.URL,
	}
	analysis := r.analyzer.Analyze(ctx, syntheticResp)

	result := &pipeline.RenderResult{
		OriginalRequest: req,
		Decision:        analysis.Decision,
		RetryAfter:      analysis.RetryAfter,
		IsHollow:        analysis.IsHollow,
		HollowScore:     analysis.HollowScore,
		Fields:          analysis.Fields,
		Elapsed:         time.Since(start),
	}
	return result
}

// detectEscalation checks the rendered page for signals that require a higher tier.
// Returns a non-empty reason string if escalation is warranted.
func detectEscalation(page *rod.Page) string {
	// Cookie consent overlays.
	cookieSelectors := []string{
		"#onetrust-banner-sdk",
		".fc-dialog-container",
		"#CybotCookiebotDialog",
		".cmp-popup",
	}
	for _, sel := range cookieSelectors {
		if page.MustHas(sel) {
			return "cookie consent wall: " + sel
		}
	}

	// Login walls — URL-based.
	info, err := page.Info()
	if err == nil {
		u := strings.ToLower(info.URL)
		for _, seg := range []string{"/login", "/signin", "/sign-in"} {
			if strings.Contains(u, seg) {
				return "login wall detected in URL"
			}
		}
	}

	// Login walls — form-based.
	if page.MustHas(`input[type="password"]`) {
		return "login wall: password input present"
	}

	// Cloudflare challenge.
	cfSelectors := []string{
		"#cf-challenge-running",
		".cf-error-type-1010",
	}
	for _, sel := range cfSelectors {
		if page.MustHas(sel) {
			return "cloudflare challenge: " + sel
		}
	}

	return ""
}

func escalateResult(req *pipeline.Request, reason string, elapsed time.Duration) *pipeline.RenderResult {
	return &pipeline.RenderResult{
		OriginalRequest:  req,
		Decision:         pipeline.DecisionEscalate,
		EscalationReason: reason,
		Elapsed:          elapsed,
	}
}
