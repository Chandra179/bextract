package tier2

import (
	"context"

	"bextract/internal/pipeline"

	"github.com/PuerkitoBio/goquery"
)

// Extractor is the interface every extraction strategy implements.
// All implementations must be safe for concurrent use — they only read
// the shared document and never mutate state.
type Extractor interface {
	// Name returns the short identifier used in ExtractorResult.Source.
	Name() string

	// Priority returns the merge priority: lower number wins field conflicts.
	Priority() int

	// Confidence returns the base confidence score for this source.
	Confidence() float64

	// Extract runs against the pre-parsed document and the raw response.
	// doc is shared read-only across all concurrent goroutines.
	// resp is provided for extractors that need header or raw body access.
	Extract(ctx context.Context, doc *goquery.Document, resp *pipeline.Response) pipeline.ExtractorResult
}
