package tier2

import (
	"context"
	"strings"

	"bextract/internal/pipeline"

	"github.com/PuerkitoBio/goquery"
)

type cssHiddenExtractor struct{}

func (e *cssHiddenExtractor) Name() string        { return "css-hidden" }
func (e *cssHiddenExtractor) Priority() int       { return 9 }
func (e *cssHiddenExtractor) Confidence() float64 { return 0.60 }

func (e *cssHiddenExtractor) Extract(_ context.Context, doc *goquery.Document, resp *pipeline.Response) pipeline.ExtractorResult {
	result := pipeline.ExtractorResult{
		Source:     e.Name(),
		Priority:   e.Priority(),
		Confidence: e.Confidence(),
		Fields:     make(map[string]string),
	}

	// Inline style hidden elements.
	doc.Find(`[style]`).Each(func(_ int, s *goquery.Selection) {
		style, _ := s.Attr("style")
		style = strings.ToLower(style)
		if !strings.Contains(style, "display:none") &&
			!strings.Contains(style, "display: none") &&
			!strings.Contains(style, "visibility:hidden") &&
			!strings.Contains(style, "visibility: hidden") {
			return
		}
		extractHiddenElement(s, result.Fields)
	})

	// Class-based hidden elements.
	doc.Find(`.hidden, .sr-only, [aria-hidden="true"]`).Each(func(_ int, s *goquery.Selection) {
		extractHiddenElement(s, result.Fields)
	})

	_ = resp
	return result
}

// extractHiddenElement infers a key from the element's id or class and stores its text.
func extractHiddenElement(s *goquery.Selection, fields map[string]string) {
	text := strings.TrimSpace(s.Text())
	if text == "" {
		return
	}

	// Prefer id as key.
	if id, ok := s.Attr("id"); ok && id != "" {
		key := normalizeKey(id)
		if _, exists := fields[key]; !exists {
			fields[key] = text
		}
		return
	}

	// Fall back to the first meaningful class name.
	if class, ok := s.Attr("class"); ok && class != "" {
		for _, cls := range strings.Fields(class) {
			cls = strings.ToLower(cls)
			// Skip utility class names that don't describe content.
			if cls == "hidden" || cls == "sr-only" || cls == "visually-hidden" {
				continue
			}
			key := normalizeKey(cls)
			if _, exists := fields[key]; !exists {
				fields[key] = text
			}
			return
		}
	}
}
