package tier2

import (
	"context"
	"strings"

	"bextract/internal/pipeline"

	"github.com/PuerkitoBio/goquery"
)

type microdataExtractor struct {
	confidence float64
}

func (e *microdataExtractor) Name() string        { return "microdata" }
func (e *microdataExtractor) Priority() int       { return 6 }
func (e *microdataExtractor) Confidence() float64 { return e.confidence }

func (e *microdataExtractor) Extract(_ context.Context, doc *goquery.Document, resp *pipeline.Response) pipeline.ExtractorResult {
	result := pipeline.ExtractorResult{
		Source:     e.Name(),
		Priority:   e.Priority(),
		Confidence: e.Confidence(),
		Fields:     make(map[string]string),
	}

	// Microdata: itemprop="*"
	doc.Find("[itemprop]").Each(func(_ int, s *goquery.Selection) {
		prop, _ := s.Attr("itemprop")
		prop = strings.TrimSpace(prop)
		if prop == "" {
			return
		}
		key := normalizeKey(prop)
		val := microdataValue(s)
		if val != "" {
			result.Fields[key] = val
		}
	})

	// RDFa: property="schema:*" or property="og:*"
	doc.Find("[property]").Each(func(_ int, s *goquery.Selection) {
		prop, _ := s.Attr("property")
		prop = strings.TrimSpace(prop)
		if prop == "" {
			return
		}
		// Strip schema: / og: / dc: namespace prefixes.
		for _, prefix := range []string{"schema:", "og:", "dc:", "dcterms:"} {
			prop = strings.TrimPrefix(prop, prefix)
		}
		key := normalizeKey(prop)
		val := microdataValue(s)
		if val != "" {
			result.Fields[key] = val
		}
	})

	_ = resp
	return result
}

// microdataValue returns the best value for an itemprop/property element.
// Priority: content attr → datetime attr → text content.
func microdataValue(s *goquery.Selection) string {
	if v, ok := s.Attr("content"); ok {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	if v, ok := s.Attr("datetime"); ok {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return strings.TrimSpace(s.Text())
}

// normalizeKey lowercases a property name and replaces spaces/hyphens with underscores.
func normalizeKey(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ReplaceAll(s, " ", "_")
	return s
}
