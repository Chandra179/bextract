package tier3

import (
	"context"
	"strings"
	"time"

	"bextract/internal/config"
	"bextract/internal/pipeline"
	"bextract/internal/tier2"
	"bextract/pkg/logger"

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
	poolSize      int
	renderTimeout time.Duration
	analyzer      *tier2.Analyzer
	blockDomains  []string
	log           logger.Logger
}

// New launches a headless Chrome instance and returns a ready Renderer.
// Pass zero-value configs to use all defaults.
// Returns an error if Chrome cannot be found or the browser fails to connect.
func New(t3cfg config.Tier3Config, t2cfg config.Tier2Config, log logger.Logger) (*Renderer, error) {
	binPath, ok := launcher.LookPath()
	if !ok {
		return nil, errNoBrowser
	}

	poolSize := t3cfg.PoolSize
	if poolSize <= 0 {
		poolSize = defaultPoolSize
	}
	renderTimeout := time.Duration(t3cfg.RenderTimeoutMs) * time.Millisecond
	if renderTimeout <= 0 {
		renderTimeout = defaultRenderTimeout
	}

	u, err := launcher.New().
		Bin(binPath).
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

	blockDomains := t3cfg.BlockDomains
	if len(blockDomains) == 0 {
		blockDomains = []string{
			"*google-analytics.com*",
			"*googletagmanager.com*",
			"*doubleclick.net*",
			"*facebook.com/tr*",
			"*hotjar.com*",
		}
	}

	return &Renderer{
		browser:       browser,
		pool:          rod.NewPagePool(poolSize),
		poolSize:      poolSize,
		renderTimeout: renderTimeout,
		analyzer:      tier2.New(t2cfg, log),
		blockDomains:  blockDomains,
		log:           log,
	}, nil
}

// PoolSize returns the configured pool size (useful for creating per-request renderers).
func (r *Renderer) PoolSize() int {
	return r.poolSize
}

// Close shuts down the underlying Chrome browser.
func (r *Renderer) Close() error {
	return r.browser.Close()
}

// Render navigates to req.URL in a pooled Chrome page, waits for network idle,
// checks for escalation signals, then runs Tier 2 extraction on the rendered HTML.
func (r *Renderer) Render(ctx context.Context, req *pipeline.Request) *pipeline.RenderResult {
	start := time.Now()

	r.log.Debug(ctx, "tier3: starting render", logger.Field{Key: "url", Value: req.URL})

	page, err := r.pool.Get(func() (*rod.Page, error) {
		return r.browser.Page(proto.TargetCreateTarget{URL: ""})
	})
	if err != nil {
		r.log.Error(ctx, "tier3: browser page creation failed", logger.Field{Key: "url", Value: req.URL}, logger.Field{Key: "error", Value: err.Error()})
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
	// Block configured analytics/tracking domains regardless of resource type.
	for _, pat := range r.blockDomains {
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
		r.log.Error(ctx, "tier3: navigation failed", logger.Field{Key: "url", Value: req.URL}, logger.Field{Key: "error", Value: err.Error()})
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
		r.log.Warn(ctx, "tier3: escalation signal detected", logger.Field{Key: "url", Value: req.URL}, logger.Field{Key: "reason", Value: reason})
		return escalateResult(req, reason, time.Since(start))
	}

	html, err := page.HTML()
	if err != nil {
		r.log.Error(ctx, "tier3: HTML extraction failed", logger.Field{Key: "url", Value: req.URL}, logger.Field{Key: "error", Value: err.Error()})
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
		OriginalRequest:    req,
		Decision:           analysis.Decision,
		RetryAfter:         analysis.RetryAfter,
		PageType:           analysis.PageType,
		PageTypeConfidence: analysis.PageTypeConfidence,
		HollowScore:        analysis.HollowScore,
		Fields:             analysis.Fields,
		Elapsed:            time.Since(start),
	}

	if result.Decision == pipeline.DecisionEscalate {
		r.log.Warn(ctx, "tier3: tier2 re-analysis returned escalate", logger.Field{Key: "url", Value: req.URL}, logger.Field{Key: "hollow_score", Value: result.HollowScore})
	} else {
		r.log.Debug(ctx, "tier3: render complete", logger.Field{Key: "url", Value: req.URL}, logger.Field{Key: "decision", Value: result.Decision}, logger.Field{Key: "elapsed_ms", Value: result.Elapsed.Milliseconds()})
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
