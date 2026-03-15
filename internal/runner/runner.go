package runner

import (
	"context"
	"time"

	"net/http"

	"bextract/internal/config"
	"bextract/internal/pipeline"
	"bextract/internal/tier1"
	"bextract/internal/tier2"
	"bextract/internal/tier3"
	"bextract/internal/tier4"
	"bextract/pkg/logger"
	"bextract/pkg/store"
)

// Runner executes the full tier 1→2→3→4 cascade for a single extraction request.
// It is safe for concurrent use.
type Runner struct {
	scraper   *tier1.Scraper
	analyzer  *tier2.Analyzer
	renderer3 *tier3.Renderer // nil if Chrome unavailable
	renderer4 *tier4.Renderer // nil if Browserless not configured
	t2cfg     config.Tier2Config
	log       logger.Logger
	store     store.Store
}

// New creates a Runner. renderer3 and renderer4 may be nil; the cascade stops
// at the last available tier.
func New(t1cfg config.Tier1Config, t2cfg config.Tier2Config, r3 *tier3.Renderer, r4 *tier4.Renderer, log logger.Logger, st store.Store) *Runner {
	return &Runner{
		scraper:   tier1.New(time.Duration(t1cfg.TimeoutMs) * time.Millisecond),
		analyzer:  tier2.New(t2cfg, log),
		renderer3: r3,
		renderer4: r4,
		t2cfg:     t2cfg,
		log:       log,
		store:     st,
	}
}

// Run executes the cascade and returns a RunResult. The only error returned is
// a Tier 1 fetch failure; all other failures fall back to the last good tier.
func (r *Runner) Run(ctx context.Context, req *pipeline.RunRequest) (*pipeline.RunResult, error) {
	start := time.Now()

	pReq := &pipeline.Request{
		URL:         req.URL,
		APIEndpoint: req.APIEndpoint,
		Timeout:     req.FetchTimeout,
	}

	// Allow per-request extraction timeout override.
	analyzer := r.analyzer
	if req.ExtractionTimeout > 0 {
		overrideCfg := r.t2cfg
		overrideCfg.ExtractionTimeoutMs = int(req.ExtractionTimeout.Milliseconds())
		analyzer = tier2.New(overrideCfg, r.log)
	}

	var jobID string
	if id, err := r.store.CreateJob(ctx, req.URL); err == nil {
		jobID = id
	}

	// --- Tier 1 ---
	var t1resp *pipeline.Response
	if jobID != "" {
		if job, err := r.store.GetJob(ctx, jobID); err == nil && job.Tier1 != nil {
			t1resp = tier1ResultToResponse(job.Tier1, pReq)
		}
	}
	if t1resp == nil {
		scraper := r.scraper
		if req.FetchTimeout > 0 {
			scraper = tier1.New(req.FetchTimeout)
		}
		var err error
		t1resp, err = scraper.Fetch(ctx, pReq)
		if err != nil {
			return nil, err
		}
		if jobID != "" {
			_ = r.store.SaveTier1(ctx, jobID, responseTier1Result(t1resp))
		}
	}

	// --- Tier 2 ---
	var analysis *pipeline.AnalysisResult
	if jobID != "" {
		if job, err := r.store.GetJob(ctx, jobID); err == nil && job.Tier2 != nil {
			analysis = tier2ResultToAnalysis(job.Tier2)
		}
	}
	if analysis == nil {
		analysis = analyzer.Analyze(ctx, t1resp)
		if jobID != "" {
			_ = r.store.SaveTier2(ctx, jobID, analysisToTier2Result(analysis))
		}
	}
	if analysis.Decision != pipeline.DecisionEscalate {
		return runResultFromAnalysis(analysis, 2, jobID, time.Since(start)), nil
	}

	// --- Tier 3 ---
	if r.renderer3 == nil {
		r.log.Warn(ctx, "runner: tier3 unavailable, returning tier2 escalation", logger.Field{Key: "url", Value: req.URL})
		return runResultFromAnalysis(analysis, 2, jobID, time.Since(start)), nil
	}
	result3 := r.renderer3.Render(ctx, pReq)
	if jobID != "" && result3 != nil {
		_ = r.store.SaveTier3(ctx, jobID, renderResultToTier3Result(result3))
	}
	if result3.Decision != pipeline.DecisionEscalate {
		return runResultFromRender(result3, 3, jobID, time.Since(start)), nil
	}

	// --- Tier 4 ---
	if r.renderer4 == nil {
		r.log.Warn(ctx, "runner: tier4 unavailable, returning tier3 escalation", logger.Field{Key: "url", Value: req.URL})
		return runResultFromRender(result3, 3, jobID, time.Since(start)), nil
	}
	result4 := r.renderer4.Render(ctx, pReq)
	if jobID != "" && result4 != nil {
		_ = r.store.SaveTier4(ctx, jobID, renderResultToTier4Result(result4))
	}
	return runResultFromRender(result4, 4, jobID, time.Since(start)), nil
}

// --- helpers ---

func runResultFromAnalysis(a *pipeline.AnalysisResult, tier int, jobID string, elapsed time.Duration) *pipeline.RunResult {
	return &pipeline.RunResult{
		JobID:    jobID,
		Tier:     tier,
		Decision: a.Decision,
		PageType: a.PageType,
		Fields:   a.Fields,
		Elapsed:  elapsed,
	}
}

func runResultFromRender(r *pipeline.RenderResult, tier int, jobID string, elapsed time.Duration) *pipeline.RunResult {
	return &pipeline.RunResult{
		JobID:            jobID,
		Tier:             tier,
		Decision:         r.Decision,
		PageType:         r.PageType,
		Fields:           r.Fields,
		EscalationReason: r.EscalationReason,
		Elapsed:          elapsed,
	}
}

func tier1ResultToResponse(t1 *store.Tier1Result, req *pipeline.Request) *pipeline.Response {
	hdrs := make(http.Header, len(t1.Headers))
	for k, v := range t1.Headers {
		hdrs[k] = []string{v}
	}
	return &pipeline.Response{
		OriginalRequest: req,
		StatusCode:      t1.StatusCode,
		Headers:         hdrs,
		Body:            []byte(t1.Body),
		FinalURL:        t1.FinalURL,
		ContentType:     t1.ContentType,
		Elapsed:         time.Duration(t1.ElapsedMS) * time.Millisecond,
	}
}

func responseTier1Result(r *pipeline.Response) *store.Tier1Result {
	headers := make(map[string]string, len(r.Headers))
	for k, vs := range r.Headers {
		if len(vs) > 0 {
			headers[k] = vs[0]
		}
	}
	return &store.Tier1Result{
		StatusCode:  r.StatusCode,
		FinalURL:    r.FinalURL,
		ContentType: r.ContentType,
		ElapsedMS:   r.Elapsed.Milliseconds(),
		Headers:     headers,
		Body:        string(r.Body),
		FetchedAt:   time.Now().UTC(),
	}
}

func tier2ResultToAnalysis(t2 *store.Tier2Result) *pipeline.AnalysisResult {
	fields := make(map[string]pipeline.ExtractedField, len(t2.Fields))
	for k, f := range t2.Fields {
		fields[k] = pipeline.ExtractedField{Value: f.Value, Source: f.Source, Confidence: f.Confidence, Priority: f.Priority}
	}
	return &pipeline.AnalysisResult{
		Decision:           decisionFromString(t2.Decision),
		PageType:           pipeline.PageType(t2.PageType),
		PageTypeConfidence: t2.PageTypeConfidence,
		HollowScore:        t2.HollowScore,
		TechHints: pipeline.TechHints{
			IsNextJS:     t2.TechHints.IsNextJS,
			IsCloudflare: t2.TechHints.IsCloudflare,
			CFChallenge:  t2.TechHints.CFChallenge,
			IsJSON:       t2.TechHints.IsJSON,
			IsPHP:        t2.TechHints.IsPHP,
		},
		Fields:  fields,
		Elapsed: time.Duration(t2.ElapsedMS) * time.Millisecond,
	}
}

func analysisToTier2Result(a *pipeline.AnalysisResult) *store.Tier2Result {
	fields := make(map[string]store.StoredField, len(a.Fields))
	for k, f := range a.Fields {
		fields[k] = store.StoredField{Value: f.Value, Source: f.Source, Confidence: f.Confidence, Priority: f.Priority}
	}
	return &store.Tier2Result{
		Decision:           decisionString(a.Decision),
		PageType:           string(a.PageType),
		PageTypeConfidence: a.PageTypeConfidence,
		HollowScore:        a.HollowScore,
		TechHints: store.StoredTechHints{
			IsNextJS:     a.TechHints.IsNextJS,
			IsCloudflare: a.TechHints.IsCloudflare,
			CFChallenge:  a.TechHints.CFChallenge,
			IsJSON:       a.TechHints.IsJSON,
			IsPHP:        a.TechHints.IsPHP,
		},
		Fields:     fields,
		ElapsedMS:  a.Elapsed.Milliseconds(),
		AnalyzedAt: time.Now().UTC(),
	}
}

func renderResultToTier3Result(r *pipeline.RenderResult) *store.Tier3Result {
	fields := make(map[string]store.StoredField, len(r.Fields))
	for k, f := range r.Fields {
		fields[k] = store.StoredField{Value: f.Value, Source: f.Source, Confidence: f.Confidence, Priority: f.Priority}
	}
	return &store.Tier3Result{
		Decision:           decisionString(r.Decision),
		PageType:           string(r.PageType),
		PageTypeConfidence: r.PageTypeConfidence,
		HollowScore:        r.HollowScore,
		EscalationReason:   r.EscalationReason,
		Fields:             fields,
		ElapsedMS:          r.Elapsed.Milliseconds(),
		RenderedAt:         time.Now().UTC(),
	}
}

func renderResultToTier4Result(r *pipeline.RenderResult) *store.Tier4Result {
	fields := make(map[string]store.StoredField, len(r.Fields))
	for k, f := range r.Fields {
		fields[k] = store.StoredField{Value: f.Value, Source: f.Source, Confidence: f.Confidence, Priority: f.Priority}
	}
	return &store.Tier4Result{
		Decision:           decisionString(r.Decision),
		PageType:           string(r.PageType),
		PageTypeConfidence: r.PageTypeConfidence,
		HollowScore:        r.HollowScore,
		EscalationReason:   r.EscalationReason,
		Fields:             fields,
		ElapsedMS:          r.Elapsed.Milliseconds(),
		RenderedAt:         time.Now().UTC(),
	}
}

func decisionString(d pipeline.Decision) string {
	switch d {
	case pipeline.DecisionDone:
		return "Done"
	case pipeline.DecisionEscalate:
		return "Escalate"
	case pipeline.DecisionAbort:
		return "Abort"
	case pipeline.DecisionBackoff:
		return "Backoff"
	default:
		return "Unknown"
	}
}

func decisionFromString(s string) pipeline.Decision {
	switch s {
	case "Done":
		return pipeline.DecisionDone
	case "Escalate":
		return pipeline.DecisionEscalate
	case "Abort":
		return pipeline.DecisionAbort
	case "Backoff":
		return pipeline.DecisionBackoff
	default:
		return pipeline.DecisionEscalate
	}
}
