package pipeline

import (
	"net/http"
	"time"
)

// Decision is the outcome emitted by each tier after processing a request.
type Decision int

const (
	DecisionDone     Decision = iota // data found, quality sufficient — return to caller
	DecisionEscalate                 // this tier cannot fulfill the request — move to next tier
	DecisionAbort                    // resource unavailable (404, 401, 403) — stop entirely
	DecisionBackoff                  // rate limited or server error — retry same tier after delay
)

// Request carries everything a tier needs to execute.
type Request struct {
	URL          string
	TargetFields []string
	APIEndpoint  string        // optional: pre-known JSON API override; fetched instead of URL when set
	Timeout      time.Duration // zero means use the tier's default timeout
}

// Response is the complete output of Tier 1, passed as-is to Tier 2.
// Tier 1 makes no decisions — it populates this struct and returns it.
type Response struct {
	OriginalRequest *Request
	StatusCode      int
	Headers         http.Header
	Body            []byte
	FinalURL        string        // URL after following all redirects
	ContentType     string        // MIME type only, parameters (charset etc.) stripped
	Elapsed         time.Duration // wall-clock time from client.Do() to io.ReadAll() complete
}

// TechHints carries technology signals detected during Tier 2 header analysis.
type TechHints struct {
	IsNextJS     bool
	IsCloudflare bool
	CFChallenge  bool
	IsJSON       bool // content-type was application/json — Tier 1 hidden API was missed
	IsPHP        bool
}

// ExtractorResult is the raw output of a single Tier 2 extractor before merging.
type ExtractorResult struct {
	Source     string
	Priority   int
	Confidence float64
	Fields     map[string]string
	Err        error
}

// ExtractedField is a single resolved field after the Tier 2 priority merge.
type ExtractedField struct {
	Value      string
	Source     string
	Confidence float64
	Priority   int
}

// AnalysisResult is the complete output of Tier 2.
type AnalysisResult struct {
	OriginalResponse *Response
	Decision         Decision
	RetryAfter       time.Duration // non-zero when Decision == DecisionBackoff
	IsHollow         bool
	HollowScore      float64
	Fields           map[string]ExtractedField
	TechHints        TechHints
	Elapsed          time.Duration
}
