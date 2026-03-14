package tier2

import (
	"context"
	"strings"

	"bextract/internal/pipeline"

	"github.com/PuerkitoBio/goquery"
)

type dataAttrExtractor struct {
	confidence float64
}

func (e *dataAttrExtractor) Name() string        { return "data-attrs" }
func (e *dataAttrExtractor) Priority() int       { return 7 }
func (e *dataAttrExtractor) Confidence() float64 { return e.confidence }

// containerSelectors targets elements that commonly carry product/listing data attributes.
var containerSelectors = strings.Join([]string{
	`[class*="product"]`,
	`[class*="listing"]`,
	`[class*="item"]`,
	`[class*="card"]`,
	`[class*="offer"]`,
	`[data-sku]`,
	`[data-product-id]`,
	`[data-price]`,
	`[data-id]`,
}, ", ")

func (e *dataAttrExtractor) Extract(_ context.Context, doc *goquery.Document, resp *pipeline.Response) pipeline.ExtractorResult {
	result := pipeline.ExtractorResult{
		Source:     e.Name(),
		Priority:   e.Priority(),
		Confidence: e.Confidence(),
		Fields:     make(map[string]string),
	}

	doc.Find(containerSelectors).Each(func(_ int, s *goquery.Selection) {
		for _, attr := range s.Nodes[0].Attr {
			if !strings.HasPrefix(attr.Key, "data-") {
				continue
			}
			val := strings.TrimSpace(attr.Val)
			if val == "" {
				continue
			}
			// Normalise: strip "data-" prefix, hyphens → underscores.
			key := strings.ReplaceAll(strings.TrimPrefix(attr.Key, "data-"), "-", "_")
			// First-write-wins across multiple containers.
			if _, exists := result.Fields[key]; !exists {
				result.Fields[key] = val
			}
		}
	})

	_ = resp
	return result
}
