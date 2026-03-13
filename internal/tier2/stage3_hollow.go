package tier2

import (
	"strings"

	"bextract/internal/pipeline"

	"github.com/PuerkitoBio/goquery"
)

const hollowThreshold = 0.70

type hollowResult struct {
	IsHollow bool
	Score    float64
	Signals  []string // names of triggered signals, for observability
}

// detectHollow scores the page for signals that indicate JavaScript is required
// to render meaningful content. A score ≥ hollowThreshold means the page is hollow.
func detectHollow(doc *goquery.Document, resp *pipeline.Response, hints pipeline.TechHints) hollowResult {
	var score float64
	var signals []string

	add := func(name string, penalty float64) {
		score += penalty
		signals = append(signals, name)
	}

	// Cloudflare challenge — definitive hollow signal.
	if hints.CFChallenge {
		add("cf-challenge", 1.00)
	}

	// CAPTCHA widget present.
	if doc.Find("[class*=recaptcha],[class*=hcaptcha],[class*=cf-turnstile],[data-sitekey]").Length() > 0 {
		add("captcha", 0.95)
	}

	// No-script message.
	noscriptText := strings.ToLower(doc.Find("noscript").Text())
	if strings.Contains(noscriptText, "enable javascript") ||
		strings.Contains(noscriptText, "javascript required") ||
		strings.Contains(noscriptText, "javascript is disabled") ||
		strings.Contains(noscriptText, "javascript is required") {
		add("noscript-message", 0.90)
	}

	// Empty app shell: common SPA root containers with no children.
	doc.Find("div#app, div#root, div#__next").Each(func(_ int, s *goquery.Selection) {
		if s.Children().Length() == 0 && strings.TrimSpace(s.Text()) == "" {
			add("empty-app-shell", 0.85)
		}
	})

	// Low text density: text content < 5% of raw HTML size.
	if len(resp.Body) > 0 {
		bodyText := strings.TrimSpace(doc.Find("body").Text())
		density := float64(len(bodyText)) / float64(len(resp.Body))
		if density < 0.05 {
			add("low-text-density", 0.70)
		}
	}

	// Tiny body: raw response < 5 KB.
	if len(resp.Body) < 5*1024 {
		add("tiny-body", 0.50)
	}

	if score > 1.0 {
		score = 1.0
	}

	return hollowResult{
		IsHollow: score >= hollowThreshold,
		Score:    score,
		Signals:  signals,
	}
}
