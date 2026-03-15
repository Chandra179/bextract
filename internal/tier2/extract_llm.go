package tier2

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"bextract/internal/pipeline"
	"bextract/pkg/llm"

	"github.com/PuerkitoBio/goquery"
	readability "github.com/go-shiori/go-readability"
)

const defaultMaxTextBytes = 4000

const extractionPrompt = `You are a structured data extractor. Given the following webpage text, extract these fields if present:
- title: the main title of the page/article/product
- description: a brief description or summary
- price: price if this is a product page (include currency symbol)
- author: author name if this is an article
- date: publication or last updated date

Respond with ONLY a JSON object containing the fields you found. Omit fields that are not present.
Example: {"title": "...", "description": "...", "price": "$9.99"}

Webpage text:
%s`

// cleanText extracts readable article text from the parsed document using go-readability.
func cleanText(doc *goquery.Document, finalURL string, maxBytes int) string {
	var parsedURL *url.URL
	if finalURL != "" {
		parsedURL, _ = url.Parse(finalURL)
	}
	article, err := readability.FromDocument(doc.Get(0), parsedURL)
	if err != nil || strings.TrimSpace(article.TextContent) == "" {
		return ""
	}
	text := strings.TrimSpace(article.TextContent)
	if maxBytes > 0 && len(text) > maxBytes {
		text = text[:maxBytes]
	}
	return text
}

// runLLMPhaseC sends cleaned page text to the LLM and returns an ExtractorResult
// along with the clean text (populated even when the LLM call fails).
func runLLMPhaseC(ctx context.Context, client llm.Client, doc *goquery.Document, resp *pipeline.Response, confidence float64, maxBytes int) (pipeline.ExtractorResult, string) {
	if maxBytes <= 0 {
		maxBytes = defaultMaxTextBytes
	}
	base := pipeline.ExtractorResult{Source: "llm", Priority: 11, Confidence: confidence}

	text := cleanText(doc, resp.FinalURL, maxBytes)
	if text == "" {
		return base, ""
	}

	raw, err := client.Complete(ctx, fmt.Sprintf(extractionPrompt, text))
	if err != nil {
		base.Err = err
		return base, text
	}

	raw = strings.TrimSpace(raw)
	// Strip markdown code fences if the model wraps its output.
	if strings.HasPrefix(raw, "```") {
		lines := strings.SplitN(raw, "\n", -1)
		if len(lines) >= 3 {
			raw = strings.Join(lines[1:len(lines)-1], "\n")
		}
	}

	var fields map[string]string
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		base.Err = fmt.Errorf("llm json parse: %w", err)
		return base, text
	}

	// Drop empty values.
	for k, v := range fields {
		if strings.TrimSpace(v) == "" {
			delete(fields, k)
		}
	}

	base.Fields = fields
	return base, text
}
