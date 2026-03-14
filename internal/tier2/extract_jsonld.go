package tier2

import (
	"context"
	"encoding/json"
	"strings"

	"bextract/internal/pipeline"

	"github.com/PuerkitoBio/goquery"
)

type jsonLDExtractor struct {
	confidence float64
}

func (e *jsonLDExtractor) Name() string        { return "json-ld" }
func (e *jsonLDExtractor) Priority() int       { return 1 }
func (e *jsonLDExtractor) Confidence() float64 { return e.confidence }

func (e *jsonLDExtractor) Extract(_ context.Context, doc *goquery.Document, resp *pipeline.Response) pipeline.ExtractorResult {
	result := pipeline.ExtractorResult{
		Source:     e.Name(),
		Priority:   e.Priority(),
		Confidence: e.Confidence(),
		Fields:     make(map[string]string),
	}

	doc.Find(`script[type="application/ld+json"]`).Each(func(_ int, s *goquery.Selection) {
		text := strings.TrimSpace(s.Text())
		if text == "" {
			return
		}
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(text), &obj); err != nil {
			// May be an array of LD+JSON objects.
			var arr []map[string]interface{}
			if err2 := json.Unmarshal([]byte(text), &arr); err2 != nil {
				result.Err = err2
				return
			}
			for _, item := range arr {
				flattenInto(result.Fields, item)
			}
			return
		}
		flattenInto(result.Fields, obj)
	})

	_ = resp
	return result
}

// flattenInto copies top-level string-valued keys from src into dst.
// Nested objects are JSON-serialized as their value.
func flattenInto(dst map[string]string, src map[string]interface{}) {
	for k, v := range src {
		switch val := v.(type) {
		case string:
			if val != "" {
				dst[k] = val
			}
		case float64:
			dst[k] = strings.TrimRight(strings.TrimRight(
				strings.Replace(json.Number(fmt_float(val)).String(), ",", "", -1), "0"), ".")
		case bool:
			if val {
				dst[k] = "true"
			} else {
				dst[k] = "false"
			}
		default:
			if b, err := json.Marshal(v); err == nil {
				dst[k] = string(b)
			}
		}
	}
}

func fmt_float(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}
