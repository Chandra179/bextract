package tier2

import (
	"context"
	"strings"

	"bextract/internal/pipeline"

	"github.com/PuerkitoBio/goquery"
)

type hiddenInputExtractor struct {
	confidence float64
}

func (e *hiddenInputExtractor) Name() string        { return "hidden-inputs" }
func (e *hiddenInputExtractor) Priority() int       { return 8 }
func (e *hiddenInputExtractor) Confidence() float64 { return e.confidence }

// securityTokenNames are filtered out — they are not useful data fields.
var securityTokenNames = map[string]bool{
	"csrf_token":         true,
	"_token":             true,
	"authenticity_token": true,
	"_csrf":              true,
	"csrfmiddlewaretoken": true,
	"__requestverificationtoken": true,
}

func (e *hiddenInputExtractor) Extract(_ context.Context, doc *goquery.Document, resp *pipeline.Response) pipeline.ExtractorResult {
	result := pipeline.ExtractorResult{
		Source:     e.Name(),
		Priority:   e.Priority(),
		Confidence: e.Confidence(),
		Fields:     make(map[string]string),
	}

	doc.Find(`input[type="hidden"]`).Each(func(_ int, s *goquery.Selection) {
		name, _ := s.Attr("name")
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}

		key := normalizeKey(name)
		if securityTokenNames[key] {
			return
		}

		val, _ := s.Attr("value")
		val = strings.TrimSpace(val)
		if val == "" {
			return
		}

		if _, exists := result.Fields[key]; !exists {
			result.Fields[key] = val
		}
	})

	_ = resp
	return result
}
