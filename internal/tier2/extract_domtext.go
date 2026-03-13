package tier2

import (
	"context"
	"strings"

	"bextract/internal/pipeline"

	"github.com/PuerkitoBio/goquery"
)

type domTextExtractor struct{}

func (e *domTextExtractor) Name() string        { return "dom-text" }
func (e *domTextExtractor) Priority() int       { return 10 }
func (e *domTextExtractor) Confidence() float64 { return 0.55 }

func (e *domTextExtractor) Extract(_ context.Context, doc *goquery.Document, resp *pipeline.Response) pipeline.ExtractorResult {
	result := pipeline.ExtractorResult{
		Source:     e.Name(),
		Priority:   e.Priority(),
		Confidence: e.Confidence(),
		Fields:     make(map[string]string),
	}

	req := resp.OriginalRequest
	if req == nil || len(req.TargetFields) == 0 {
		// No selector map supplied — nothing to do.
		return result
	}

	// TargetFields are "field:selector" pairs when using DOM text extraction.
	// Format: "price:.product-price__amount"
	for _, tf := range req.TargetFields {
		idx := strings.IndexByte(tf, ':')
		if idx <= 0 || idx == len(tf)-1 {
			continue
		}
		field := tf[:idx]
		selector := tf[idx+1:]
		text := strings.TrimSpace(doc.Find(selector).First().Text())
		if text != "" {
			result.Fields[field] = text
		}
	}

	return result
}
