package tier2

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"bextract/internal/config"
	"bextract/internal/pipeline"
	"bextract/pkg/logger"

	"github.com/PuerkitoBio/goquery"
)

const defaultExtractionTimeout = 5 * time.Second

// Analyzer runs the five Tier 2 stages against a Tier 1 response.
// A single Analyzer is safe for concurrent use across goroutines.
type Analyzer struct {
	extractionTimeout time.Duration
	cfg               config.Tier2Config
	log               logger.Logger
}

// New constructs an Analyzer using the provided Tier2Config.
// Pass a zero-value config to use all defaults. Pass 0 extractionTimeout to use the default 5 s.
func New(cfg config.Tier2Config, log logger.Logger) *Analyzer {
	timeout := time.Duration(cfg.ExtractionTimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = defaultExtractionTimeout
	}
	return &Analyzer{extractionTimeout: timeout, cfg: cfg, log: log}
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

	// Stage 3: Page classification (hollow detection + type).
	hollowCfg := a.cfg.Hollow
	hollow := detectHollow(doc, resp, hints, hollowCfg)
	classification := classifyPageWithHints(doc, resp, hints, hollowCfg)

	// Stage 4: Concurrent extraction with timeout.
	results := a.runExtractors(ctx, doc, resp)

	// Stage 5: Merge and decide.
	req := resp.OriginalRequest
	if req == nil {
		req = &pipeline.Request{}
	}
	result := merge(results, hints, hollow, classification, req, a.cfg.Merge)
	result.OriginalResponse = resp
	result.Elapsed = time.Since(start)

	if result.Decision == pipeline.DecisionEscalate {
		a.logEscalation(ctx, resp, hollow, results, req)
	}

	return result
}

// classifyPageWithHints runs classifyPage with CF-challenge hint applied.
func classifyPageWithHints(doc *goquery.Document, resp *pipeline.Response, hints pipeline.TechHints, cfg config.HollowConfig) PageClassification {
	pc := classifyPage(doc, resp, cfg)
	// If CF challenge is signalled, override to app-shell.
	if hints.CFChallenge {
		pc.Type = pipeline.PageTypeAppShell
		pc.Confidence = 1.0
	}
	return pc
}

// logEscalation emits a Warn log explaining why tier2 decided to escalate.
func (a *Analyzer) logEscalation(
	ctx context.Context,
	resp *pipeline.Response,
	hollow hollowResult,
	results []pipeline.ExtractorResult,
	req *pipeline.Request,
) {
	url := resp.FinalURL
	if url == "" && resp.OriginalRequest != nil {
		url = resp.OriginalRequest.URL
	}

	minConf := a.cfg.Merge.MinConfidence
	if minConf <= 0 {
		minConf = 0.50
	}

	// Collect extractor summaries for debugging.
	var extractorNotes []string
	for _, r := range results {
		if r.Err != nil {
			extractorNotes = append(extractorNotes, fmt.Sprintf("%s:err(%s)", r.Source, r.Err))
		} else if r.Confidence < minConf || len(r.Fields) == 0 {
			extractorNotes = append(extractorNotes, fmt.Sprintf("%s:low(conf=%.2f,fields=%d)", r.Source, r.Confidence, len(r.Fields)))
		} else {
			extractorNotes = append(extractorNotes, fmt.Sprintf("%s:ok(%d fields)", r.Source, len(r.Fields)))
		}
	}

	fields := []logger.Field{
		{Key: "url", Value: url},
		{Key: "hollow", Value: hollow.IsHollow},
		{Key: "hollow_score", Value: hollow.Score},
		{Key: "hollow_signals", Value: strings.Join(hollow.Signals, ",")},
		{Key: "extractors", Value: strings.Join(extractorNotes, " | ")},
	}

	a.log.Warn(ctx, "tier2: escalating", fields...)
}

// buildExtractors constructs the extractor list from config.
// Extractors with enabled: false are omitted. Confidence values from config override defaults.
func buildExtractors(cfg config.Tier2Config) []Extractor {
	conf := func(name string, def float64) float64 {
		if c, ok := cfg.Extractors[name]; ok {
			if c.Confidence > 0 {
				return c.Confidence
			}
		}
		return def
	}
	enabled := func(name string) bool {
		if c, ok := cfg.Extractors[name]; ok {
			return c.Enabled
		}
		return true // default to enabled if not configured
	}

	type entry struct {
		name string
		ex   Extractor
	}
	candidates := []entry{
		{"json-ld", &jsonLDExtractor{confidence: conf("json-ld", 0.95)}},
		{"next-data", &nextDataExtractor{confidence: conf("next-data", 0.92)}},
		{"globals", &globalsExtractor{confidence: conf("globals", 0.85)}},
		{"inline-var", &inlineVarExtractor{confidence: conf("inline-var", 0.75)}},
		{"meta-tags", &metaExtractor{confidence: conf("meta-tags", 0.88)}},
		{"microdata", &microdataExtractor{confidence: conf("microdata", 0.82)}},
		{"data-attr", &dataAttrExtractor{confidence: conf("data-attr", 0.78)}},
		{"hidden-input", &hiddenInputExtractor{confidence: conf("hidden-input", 0.72)}},
		{"css-hidden", &cssHiddenExtractor{confidence: conf("css-hidden", 0.60)}},
		{"dom-text", &domTextExtractor{confidence: conf("dom-text", 0.55)}},
	}

	out := make([]Extractor, 0, len(candidates))
	for _, c := range candidates {
		if enabled(c.name) {
			out = append(out, c.ex)
		}
	}
	return out
}

// runExtractors fans out to all extractors in parallel and collects results.
func (a *Analyzer) runExtractors(
	ctx context.Context,
	doc *goquery.Document,
	resp *pipeline.Response,
) []pipeline.ExtractorResult {
	extractors := buildExtractors(a.cfg)

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
