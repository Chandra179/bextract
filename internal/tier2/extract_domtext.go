package tier2

import (
	"context"

	"bextract/internal/pipeline"

	"github.com/PuerkitoBio/goquery"
)

type domTextExtractor struct {
	confidence float64
}

func (e *domTextExtractor) Name() string        { return "dom-text" }
func (e *domTextExtractor) Priority() int       { return 10 }
func (e *domTextExtractor) Confidence() float64 { return e.confidence }

func (e *domTextExtractor) Extract(_ context.Context, _ *goquery.Document, _ *pipeline.Response) pipeline.ExtractorResult {
	return pipeline.ExtractorResult{
		Source:     e.Name(),
		Priority:   e.Priority(),
		Confidence: e.Confidence(),
		Fields:     make(map[string]string),
	}
}
