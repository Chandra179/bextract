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

// PageType classifies the kind of page encountered during Tier 2 analysis.
type PageType string

const (
	// PageTypeContentRich indicates good text density with no JS-shell signals.
	PageTypeContentRich PageType = "content-rich"
	// PageTypeLinkRich indicates a navigation or aggregator page with many text links.
	PageTypeLinkRich PageType = "link-rich"
	// PageTypeAppShell indicates a JavaScript-required page with no static content.
	PageTypeAppShell PageType = "app-shell"
	// PageTypeMixed indicates partial content that may benefit from JS rendering.
	PageTypeMixed PageType = "mixed"
)

// Request carries everything a tier needs to execute.
type Request struct {
	URL         string
	APIEndpoint string        // optional: pre-known JSON API override; fetched instead of URL when set
	Timeout     time.Duration // zero means use the tier's default timeout
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
	OriginalResponse   *Response
	Decision           Decision
	RetryAfter         time.Duration // non-zero when Decision == DecisionBackoff
	PageType           PageType
	PageTypeConfidence float64
	// HollowScore and TechHints are kept for debug/observability use only.
	HollowScore float64
	TechHints   TechHints
	Fields      map[string]ExtractedField
	CleanText   string        // readable article text produced by go-readability during Phase C
	Elapsed     time.Duration
}

// RunRequest is the input to the cascade runner.
type RunRequest struct {
	URL                 string
	APIEndpoint         string
	JobID               string
	FetchTimeout        time.Duration // 0 = tier default
	ExtractionTimeout   time.Duration // 0 = tier default
}

// RunResult is the normalised output of whichever tier completed the cascade.
type RunResult struct {
	JobID            string
	Tier             int
	Decision         Decision
	PageType         PageType
	Fields           map[string]ExtractedField
	EscalationReason string
	Elapsed          time.Duration
}

// RenderResult is the complete output of Tier 3.
type RenderResult struct {
	OriginalRequest    *Request
	Decision           Decision
	RetryAfter         time.Duration // non-zero when Decision == DecisionBackoff
	PageType           PageType
	PageTypeConfidence float64
	// HollowScore is kept for debug/observability use only.
	HollowScore      float64
	Fields           map[string]ExtractedField
	EscalationReason string // non-empty when Decision == DecisionEscalate
	Elapsed          time.Duration
}
