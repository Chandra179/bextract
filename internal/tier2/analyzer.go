package tier2

import (
	"bytes"
	"context"
	"sync"
	"time"

	"bextract/internal/pipeline"

	"github.com/PuerkitoBio/goquery"
)

const defaultExtractionTimeout = 5 * time.Second

// Analyzer runs the five Tier 2 stages against a Tier 1 response.
// A single Analyzer is safe for concurrent use across goroutines.
type Analyzer struct {
	extractionTimeout time.Duration
}

// New constructs an Analyzer. Pass 0 to use the default 5-second extraction timeout.
func New(extractionTimeout time.Duration) *Analyzer {
	if extractionTimeout == 0 {
		extractionTimeout = defaultExtractionTimeout
	}
	return &Analyzer{extractionTimeout: extractionTimeout}
}

// Analyze runs all five Tier 2 stages and returns a single AnalysisResult.
// It never returns nil. Errors during individual extractors are captured
// per-extractor in ExtractorResult.Err and do not abort the pipeline.
func (a *Analyzer) Analyze(ctx context.Context, resp *pipeline.Response) *pipeline.AnalysisResult {
	start := time.Now()

	// Stage 1: Header analysis — may short-circuit the entire pipeline.
	hardResult, hints, done := analyzeHeaders(resp)
	if done {
		hardResult.OriginalResponse = resp
		hardResult.Elapsed = time.Since(start)
		return hardResult
	}

	// Stage 2: Parse HTML once; share the document with all extractors.
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(resp.Body))
	if err != nil {
		// Unparseable body — escalate so a browser tier can try.
		return &pipeline.AnalysisResult{
			OriginalResponse: resp,
			Decision:         pipeline.DecisionEscalate,
			TechHints:        hints,
			Elapsed:          time.Since(start),
		}
	}

	// Stage 3: Hollow page detection.
	hollow := detectHollow(doc, resp, hints)

	// Stage 4: Concurrent extraction with timeout.
	results := a.runExtractors(ctx, doc, resp)

	// Stage 5: Merge and decide.
	req := resp.OriginalRequest
	if req == nil {
		req = &pipeline.Request{}
	}
	result := merge(results, hints, hollow, req)
	result.OriginalResponse = resp
	result.Elapsed = time.Since(start)
	return result
}

// runExtractors fans out to all extractors in parallel and collects results.
func (a *Analyzer) runExtractors(
	ctx context.Context,
	doc *goquery.Document,
	resp *pipeline.Response,
) []pipeline.ExtractorResult {
	extractors := []Extractor{
		&jsonLDExtractor{},
		&nextDataExtractor{},
		&globalsExtractor{},
		&inlineVarExtractor{},
		&metaExtractor{},
		&microdataExtractor{},
		&dataAttrExtractor{},
		&hiddenInputExtractor{},
		&cssHiddenExtractor{},
		&domTextExtractor{},
	}

	ctx, cancel := context.WithTimeout(ctx, a.extractionTimeout)
	defer cancel()

	results := make(chan pipeline.ExtractorResult, len(extractors))
	var wg sync.WaitGroup
	for _, ex := range extractors {
		wg.Add(1)
		go func(e Extractor) {
			defer wg.Done()
			results <- e.Extract(ctx, doc, resp)
		}(ex)
	}
	wg.Wait()
	close(results)

	out := make([]pipeline.ExtractorResult, 0, len(extractors))
	for r := range results {
		out = append(out, r)
	}
	return out
}
