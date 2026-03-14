package tier2

import (
	"strings"

	"bextract/internal/config"
	"bextract/internal/pipeline"

	"github.com/PuerkitoBio/goquery"
)

// PageClassification is the output of the page-type classifier.
type PageClassification struct {
	Type       pipeline.PageType
	Confidence float64
	// Penalties and TotalScore are kept for debug/observability.
	Penalties  map[string]float64
	TotalScore float64
}

// hollowResult is kept as an internal convenience type used by stage5_merge.
type hollowResult struct {
	IsHollow bool
	Score    float64
	Signals  []string // names of triggered signals, for observability
}

// classifyPage scores the page for signals that indicate JavaScript is required
// to render meaningful content and returns a PageClassification.
func classifyPage(doc *goquery.Document, resp *pipeline.Response, cfg config.HollowConfig) PageClassification {
	var score float64
	signals := make([]string, 0)
	penalties := make(map[string]float64)

	add := func(name string, penalty float64) {
		score += penalty
		signals = append(signals, name)
		penalties[name] = penalty
	}

	// Cloudflare challenge — definitive hollow signal.
	if p, ok := cfg.Penalties["cf-challenge"]; ok {
		// We detect CF challenge via TechHints (passed from stage 1), but here we
		// check the doc-level selector as a fallback for callers without hints.
		_ = p // penalty value looked up dynamically below via addSignal helper
	}

	// Helper that looks up the penalty from config or falls back to the provided default.
	addSignal := func(name string, defaultPenalty float64) {
		p, ok := cfg.Penalties[name]
		if !ok {
			p = defaultPenalty
		}
		add(name, p)
	}

	// CAPTCHA widget present.
	if doc.Find("[class*=recaptcha],[class*=hcaptcha],[class*=cf-turnstile],[data-sitekey]").Length() > 0 {
		addSignal("captcha", 0.95)
	}

	// No-script message.
	noscriptText := strings.ToLower(doc.Find("noscript").Text())
	if strings.Contains(noscriptText, "enable javascript") ||
		strings.Contains(noscriptText, "javascript required") ||
		strings.Contains(noscriptText, "javascript is disabled") ||
		strings.Contains(noscriptText, "javascript is required") {
		addSignal("noscript-message", 0.90)
	}

	// Empty app shell: common SPA root containers with no children.
	doc.Find("div#app, div#root, div#__next").Each(func(_ int, s *goquery.Selection) {
		if s.Children().Length() == 0 && strings.TrimSpace(s.Text()) == "" {
			addSignal("empty-app-shell", 0.85)
		}
	})

	// Link-rich counter-signal: pages with many text links (e.g. news aggregators)
	// have a low text/HTML ratio by design, not because JS is required.
	minLinks := cfg.LinkRichMinLinks
	if minLinks <= 0 {
		minLinks = 10
	}
	linkCount := 0
	doc.Find("a").Each(func(_ int, s *goquery.Selection) {
		if strings.TrimSpace(s.Text()) != "" {
			linkCount++
		}
	})
	isLinkRich := linkCount >= minLinks

	// Low text density: suppressed on link-rich pages (high link/HTML ratio is expected).
	densityThreshold := cfg.TextDensityRatio
	if densityThreshold <= 0 {
		densityThreshold = 0.05
	}
	if !isLinkRich && len(resp.Body) > 0 {
		bodyText := strings.TrimSpace(doc.Find("body").Text())
		density := float64(len(bodyText)) / float64(len(resp.Body))
		if density < densityThreshold {
			addSignal("low-text-density", 0.70)
		}
	}

	// Tiny body: suppressed on link-rich pages.
	tinyBytes := cfg.TinyBodyBytes
	if tinyBytes <= 0 {
		tinyBytes = 5120
	}
	if !isLinkRich && len(resp.Body) < tinyBytes {
		addSignal("tiny-body", 0.50)
	}

	if score > 1.0 {
		score = 1.0
	}

	threshold := cfg.Threshold
	if threshold <= 0 {
		threshold = 0.70
	}

	// Classify based on accumulated score and link-rich counter.
	var pageType pipeline.PageType
	var confidence float64

	switch {
	case score >= threshold:
		pageType = pipeline.PageTypeAppShell
		confidence = score
	case isLinkRich && score < threshold:
		pageType = pipeline.PageTypeLinkRich
		// Confidence grows with the number of links, capped at 0.95.
		confidence = min64(0.50+float64(linkCount)/float64(minLinks)*0.20, 0.95)
	case !isLinkRich && score == 0:
		pageType = pipeline.PageTypeContentRich
		confidence = 1.0 - score
	default:
		pageType = pipeline.PageTypeMixed
		confidence = 1.0 - score
	}

	return PageClassification{
		Type:       pageType,
		Confidence: confidence,
		Penalties:  penalties,
		TotalScore: score,
	}
}

// detectHollow adapts classifyPage into the internal hollowResult used by stage5_merge.
func detectHollow(doc *goquery.Document, resp *pipeline.Response, hints pipeline.TechHints, cfg config.HollowConfig) hollowResult {
	// Apply CF-challenge signal before scoring via classifyPage (it needs TechHints).
	// We inject it directly into a temporary local penalty accumulation.
	adjustedDoc := doc
	_ = adjustedDoc

	var extraScore float64
	var extraSignals []string
	if hints.CFChallenge {
		p := cfg.Penalties["cf-challenge"]
		if p <= 0 {
			p = 1.00
		}
		extraScore = p
		extraSignals = []string{"cf-challenge"}
	}

	pc := classifyPage(doc, resp, cfg)
	totalScore := pc.TotalScore + extraScore
	if totalScore > 1.0 {
		totalScore = 1.0
	}

	threshold := cfg.Threshold
	if threshold <= 0 {
		threshold = 0.70
	}

	allSignals := make([]string, 0, len(extraSignals)+len(pc.Penalties))
	allSignals = append(allSignals, extraSignals...)
	for k := range pc.Penalties {
		allSignals = append(allSignals, k)
	}

	return hollowResult{
		IsHollow: totalScore >= threshold,
		Score:    totalScore,
		Signals:  allSignals,
	}
}

func min64(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
