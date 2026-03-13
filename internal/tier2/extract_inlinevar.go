package tier2

import (
	"context"
	"encoding/json"
	"regexp"

	"bextract/internal/pipeline"

	"github.com/PuerkitoBio/goquery"
)

type inlineVarExtractor struct{}

func (e *inlineVarExtractor) Name() string       { return "inline-vars" }
func (e *inlineVarExtractor) Priority() int      { return 4 }
func (e *inlineVarExtractor) Confidence() float64 { return 0.75 }

// inlineVarPattern matches uppercase-only variable declarations assigned a JSON object.
// Uppercase restriction avoids matching framework internals that use camelCase.
var inlineVarPattern = regexp.MustCompile(
	`(?:var|const|let)\s+([A-Z_][A-Z0-9_]*)\s*=\s*(\{[\s\S]*?\})\s*;`,
)

func (e *inlineVarExtractor) Extract(_ context.Context, doc *goquery.Document, resp *pipeline.Response) pipeline.ExtractorResult {
	result := pipeline.ExtractorResult{
		Source:     e.Name(),
		Priority:   e.Priority(),
		Confidence: e.Confidence(),
		Fields:     make(map[string]string),
	}

	doc.Find("script:not([src])").Each(func(_ int, s *goquery.Selection) {
		text := s.Text()
		if text == "" {
			return
		}
		for _, m := range inlineVarPattern.FindAllStringSubmatch(text, -1) {
			jsonStr := m[2]
			var obj map[string]interface{}
			if err := json.Unmarshal([]byte(jsonStr), &obj); err != nil {
				result.Err = err
				continue
			}
			flattenInto(result.Fields, obj)
		}
	})

	_ = resp
	return result
}
