package tier2

import (
	"strconv"
	"strings"
	"time"

	"bextract/internal/pipeline"
)

// analyzeHeaders inspects the status code and headers for hard decisions and
// technology fingerprints. Returns (result, hints, true) when a hard decision
// is reached (Abort/Backoff/Escalate), bypassing all subsequent stages.
// Returns (nil, hints, false) when analysis should continue to Stage 2.
func analyzeHeaders(resp *pipeline.Response) (*pipeline.AnalysisResult, pipeline.TechHints, bool) {
	hints := fingerprintHeaders(resp)

	// Cloudflare active challenge — skip Tiers 2–4, escalate directly to Tier 5.
	if hints.CFChallenge {
		return &pipeline.AnalysisResult{
			OriginalResponse: resp,
			Decision:         pipeline.DecisionEscalate,
			TechHints:        hints,
		}, hints, true
	}

	switch resp.StatusCode {
	case 404:
		return &pipeline.AnalysisResult{
			OriginalResponse: resp,
			Decision:         pipeline.DecisionAbort,
			TechHints:        hints,
		}, hints, true

	case 401, 403:
		return &pipeline.AnalysisResult{
			OriginalResponse: resp,
			Decision:         pipeline.DecisionAbort,
			TechHints:        hints,
		}, hints, true

	case 429:
		retryAfter := parseRetryAfter(resp)
		return &pipeline.AnalysisResult{
			OriginalResponse: resp,
			Decision:         pipeline.DecisionBackoff,
			RetryAfter:       retryAfter,
			TechHints:        hints,
		}, hints, true

	case 503:
		retryAfter := parseRetryAfter(resp)
		if retryAfter > 0 {
			return &pipeline.AnalysisResult{
				OriginalResponse: resp,
				Decision:         pipeline.DecisionBackoff,
				RetryAfter:       retryAfter,
				TechHints:        hints,
			}, hints, true
		}
		// 503 without Retry-After: continue to extraction, may have a body.
	}

	return nil, hints, false
}

// fingerprintHeaders extracts technology signals from response headers.
func fingerprintHeaders(resp *pipeline.Response) pipeline.TechHints {
	var h pipeline.TechHints

	powered := resp.Headers.Get("x-powered-by")
	if strings.Contains(strings.ToLower(powered), "next.js") {
		h.IsNextJS = true
	}
	if strings.Contains(strings.ToLower(powered), "php") {
		h.IsPHP = true
	}

	if resp.Headers.Get("cf-ray") != "" {
		h.IsCloudflare = true
	}
	if strings.EqualFold(resp.Headers.Get("cf-mitigated"), "challenge") {
		h.CFChallenge = true
		h.IsCloudflare = true
	}

	if strings.Contains(resp.ContentType, "application/json") {
		h.IsJSON = true
	}

	return h
}

// parseRetryAfter reads the Retry-After header and returns the delay duration.
// Returns 0 if the header is absent or unparseable.
func parseRetryAfter(resp *pipeline.Response) time.Duration {
	v := resp.Headers.Get("Retry-After")
	if v == "" {
		return 0
	}
	if secs, err := strconv.ParseFloat(v, 64); err == nil && secs > 0 {
		return time.Duration(secs * float64(time.Second))
	}
	return 0
}
