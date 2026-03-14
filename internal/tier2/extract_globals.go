package tier2

import (
	"context"
	"encoding/json"
	"regexp"

	"bextract/internal/pipeline"

	"github.com/PuerkitoBio/goquery"
)

type globalsExtractor struct {
	confidence float64
}

func (e *globalsExtractor) Name() string        { return "state-globals" }
func (e *globalsExtractor) Priority() int       { return 3 }
func (e *globalsExtractor) Confidence() float64 { return e.confidence }

// globalPatterns matches common framework window global assignments.
// Each pattern captures the JSON object as group 1.
var globalPatterns = []*regexp.Regexp{
	regexp.MustCompile(`window\.__NUXT__\s*=\s*(\{[\s\S]*?\})\s*;`),
	regexp.MustCompile(`window\.__REDUX_STATE__\s*=\s*(\{[\s\S]*?\})\s*;`),
	regexp.MustCompile(`window\.__INITIAL_STATE__\s*=\s*(\{[\s\S]*?\})\s*;`),
	regexp.MustCompile(`window\.__APP_STATE__\s*=\s*(\{[\s\S]*?\})\s*;`),
	regexp.MustCompile(`window\.__PRELOADED_STATE__\s*=\s*(\{[\s\S]*?\})\s*;`),
	regexp.MustCompile(`window\.__SERVER_DATA__\s*=\s*(\{[\s\S]*?\})\s*;`),
	regexp.MustCompile(`window\.__STORE__\s*=\s*(\{[\s\S]*?\})\s*;`),
}

func (e *globalsExtractor) Extract(_ context.Context, doc *goquery.Document, resp *pipeline.Response) pipeline.ExtractorResult {
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
		for _, pat := range globalPatterns {
			m := pat.FindStringSubmatch(text)
			if m == nil {
				continue
			}
			var obj map[string]interface{}
			if err := json.Unmarshal([]byte(m[1]), &obj); err != nil {
				result.Err = err
				continue
			}
			flattenInto(result.Fields, obj)
		}
	})

	_ = resp
	return result
}
