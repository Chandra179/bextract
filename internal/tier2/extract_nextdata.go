package tier2

import (
	"context"
	"encoding/json"
	"strings"

	"bextract/internal/pipeline"

	"github.com/PuerkitoBio/goquery"
)

type nextDataExtractor struct {
	confidence float64
}

func (e *nextDataExtractor) Name() string        { return "next-data" }
func (e *nextDataExtractor) Priority() int       { return 2 }
func (e *nextDataExtractor) Confidence() float64 { return e.confidence }

func (e *nextDataExtractor) Extract(_ context.Context, doc *goquery.Document, resp *pipeline.Response) pipeline.ExtractorResult {
	result := pipeline.ExtractorResult{
		Source:     e.Name(),
		Priority:   e.Priority(),
		Confidence: e.Confidence(),
		Fields:     make(map[string]string),
	}

	text := strings.TrimSpace(doc.Find("script#__NEXT_DATA__").Text())
	if text == "" {
		_ = resp
		return result
	}

	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(text), &obj); err != nil {
		result.Err = err
		_ = resp
		return result
	}

	// __NEXT_DATA__ wraps the actual page props under props.pageProps.
	// Flatten pageProps first; fall back to top-level if missing.
	if props, ok := obj["props"].(map[string]interface{}); ok {
		if pageProps, ok := props["pageProps"].(map[string]interface{}); ok {
			flattenInto(result.Fields, pageProps)
		}
	}
	// Also capture top-level fields (page, query, buildId, etc.).
	flattenInto(result.Fields, obj)

	_ = resp
	return result
}
