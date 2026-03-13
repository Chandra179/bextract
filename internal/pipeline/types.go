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
