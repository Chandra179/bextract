package tier4

import (
	"context"
	"strings"
	"time"

	"bextract/internal/config"
	"bextract/internal/pipeline"
	"bextract/internal/tier2"
	"bextract/pkg/logger"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

const (
	defaultRenderTimeout = 15 * time.Second
	networkIdleWindow    = 500 * time.Millisecond
)

// Renderer connects to a remote Browserless instance via CDP and re-runs Tier 2
// extraction on the rendered DOM. A single Renderer is safe for concurrent use.
type Renderer struct {
	browser       *rod.Browser
	renderTimeout time.Duration
	analyzer      *tier2.Analyzer
	blockDomains  []string
	log           logger.Logger
}

// New connects to the Browserless CDP endpoint and returns a ready Renderer.
// Returns errNoBrowserless if BrowserlessURL is empty.
func New(t4cfg config.Tier4Config, t2cfg config.Tier2Config, log logger.Logger) (*Renderer, error) {
	if t4cfg.BrowserlessURL == "" {
		return nil, errNoBrowserless
	}

	renderTimeout := time.Duration(t4cfg.RenderTimeoutMs) * time.Millisecond
	if renderTimeout <= 0 {
		renderTimeout = defaultRenderTimeout
	}

	browser := rod.New().ControlURL(t4cfg.BrowserlessURL)
	if err := browser.Connect(); err != nil {
		return nil, err
	}

	blockDomains := t4cfg.BlockDomains
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
		renderTimeout: renderTimeout,
		analyzer:      tier2.New(t2cfg, log),
		blockDomains:  blockDomains,
		log:           log,
	}, nil
}

// Close disconnects from the Browserless instance.
func (r *Renderer) Close() error {
	return r.browser.Close()
}

// Render navigates to req.URL via Browserless, waits for network idle,
// checks for escalation signals, then runs Tier 2 extraction on the rendered HTML.
func (r *Renderer) Render(ctx context.Context, req *pipeline.Request) *pipeline.RenderResult {
	start := time.Now()

	r.log.Debug(ctx, "tier4: starting render", logger.Field{Key: "url", Value: req.URL})

	page, err := r.browser.Page(proto.TargetCreateTarget{URL: ""})
	if err != nil {
		r.log.Error(ctx, "tier4: page creation failed", logger.Field{Key: "url", Value: req.URL}, logger.Field{Key: "error", Value: err.Error()})
		return escalateResult(req, "page creation failed: "+err.Error(), time.Since(start))
	}
	defer func() { _ = page.Close() }()

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
		r.log.Error(ctx, "tier4: navigation failed", logger.Field{Key: "url", Value: req.URL}, logger.Field{Key: "error", Value: err.Error()})
		return escalateResult(req, "navigation failed: "+err.Error(), time.Since(start))
	}

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
		r.log.Warn(ctx, "tier4: escalation signal detected", logger.Field{Key: "url", Value: req.URL}, logger.Field{Key: "reason", Value: reason})
		return escalateResult(req, reason, time.Since(start))
	}

	html, err := page.HTML()
	if err != nil {
		r.log.Error(ctx, "tier4: HTML extraction failed", logger.Field{Key: "url", Value: req.URL}, logger.Field{Key: "error", Value: err.Error()})
		return escalateResult(req, "HTML extraction failed: "+err.Error(), time.Since(start))
	}

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
		r.log.Warn(ctx, "tier4: tier2 re-analysis returned escalate", logger.Field{Key: "url", Value: req.URL}, logger.Field{Key: "hollow_score", Value: result.HollowScore})
	} else {
		r.log.Debug(ctx, "tier4: render complete", logger.Field{Key: "url", Value: req.URL}, logger.Field{Key: "decision", Value: result.Decision}, logger.Field{Key: "elapsed_ms", Value: result.Elapsed.Milliseconds()})
	}

	return result
}

// detectEscalation checks for CAPTCHA and post-render 403 signals.
func detectEscalation(page *rod.Page) string {
	captchaSelectors := []string{
		"[class*=recaptcha]",
		"[class*=hcaptcha]",
		"[data-sitekey]",
	}
	for _, sel := range captchaSelectors {
		if page.MustHas(sel) {
			return "captcha detected: " + sel
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
