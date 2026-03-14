package tier2

import (
	"context"
	"strings"

	"bextract/internal/pipeline"

	"github.com/PuerkitoBio/goquery"
)

type metaExtractor struct {
	confidence float64
}

func (e *metaExtractor) Name() string        { return "meta-tags" }
func (e *metaExtractor) Priority() int       { return 5 }
func (e *metaExtractor) Confidence() float64 { return e.confidence }

func (e *metaExtractor) Extract(_ context.Context, doc *goquery.Document, resp *pipeline.Response) pipeline.ExtractorResult {
	result := pipeline.ExtractorResult{
		Source:     e.Name(),
		Priority:   e.Priority(),
		Confidence: e.Confidence(),
		Fields:     make(map[string]string),
	}

	// <title>
	if t := strings.TrimSpace(doc.Find("title").First().Text()); t != "" {
		result.Fields["title"] = t
	}

	// <link rel="canonical">
	doc.Find(`link[rel="canonical"]`).Each(func(_ int, s *goquery.Selection) {
		if href, ok := s.Attr("href"); ok && href != "" {
			result.Fields["canonical_url"] = href
		}
	})

	// <meta> tags — Open Graph, Twitter Cards, and standard name= tags.
	doc.Find("meta").Each(func(_ int, s *goquery.Selection) {
		content, _ := s.Attr("content")
		content = strings.TrimSpace(content)
		if content == "" {
			return
		}

		// Open Graph: property="og:*"
		if prop, ok := s.Attr("property"); ok {
			prop = strings.ToLower(strings.TrimPrefix(prop, "og:"))
			prop = strings.TrimPrefix(prop, "twitter:")
			if prop != "" {
				result.Fields[strings.ReplaceAll(prop, ":", "_")] = content
			}
			return
		}

		// Standard / Twitter name="*"
		if name, ok := s.Attr("name"); ok {
			name = strings.ToLower(name)
			name = strings.TrimPrefix(name, "twitter:")
			switch name {
			case "description", "author", "keywords", "robots":
				result.Fields[name] = content
			default:
				if strings.HasPrefix(name, "og:") {
					result.Fields[strings.ReplaceAll(strings.TrimPrefix(name, "og:"), ":", "_")] = content
				}
			}
		}
	})

	_ = resp
	return result
}
