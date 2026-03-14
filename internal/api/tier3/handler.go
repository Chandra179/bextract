package tier3handler

import (
	"net/http"
	"time"

	"bextract/internal/api"
	"bextract/internal/config"
	"bextract/internal/pipeline"
	"bextract/internal/tier1"
	"bextract/internal/tier2"
	"bextract/internal/tier3"
	"bextract/pkg/logger"
	"bextract/pkg/store"

	"github.com/gin-gonic/gin"
)

// Handler chains Tier 1 fetch → Tier 2 analysis → Tier 3 render.
type Handler struct {
	scraper  *tier1.Scraper
	analyzer *tier2.Analyzer
	renderer *tier3.Renderer
	t1cfg    config.Tier1Config
	t2cfg    config.Tier2Config
	log      logger.Logger
	store    store.Store
}

// New creates a Handler from configs. Returns an error if Chrome is unavailable.
func New(t1cfg config.Tier1Config, t2cfg config.Tier2Config, t3cfg config.Tier3Config, log logger.Logger, st store.Store) (*Handler, error) {
	renderer, err := tier3.New(t3cfg, t2cfg, log)
	if err != nil {
		return nil, err
	}
	return &Handler{
		scraper:  tier1.New(time.Duration(t1cfg.TimeoutMs) * time.Millisecond),
		analyzer: tier2.New(t2cfg, log),
		renderer: renderer,
		t1cfg:    t1cfg,
		t2cfg:    t2cfg,
		log:      log,
		store:    st,
	}, nil
}

// RenderRequest is the JSON body for POST /tier3/render.
//
//	@Description	URL and optional configuration for Tier 3 browser rendering.
type RenderRequest struct {
	URL                 string `json:"url"                          binding:"required" example:"https://example.com/product/123"`
	APIEndpoint         string `json:"api_endpoint,omitempty"                          example:"https://api.example.com/product/123"`
	FetchTimeoutMS      int    `json:"fetch_timeout_ms,omitempty"                      example:"10000"`
	ExtractionTimeoutMS int    `json:"extraction_timeout_ms,omitempty"                 example:"5000"`
	RenderTimeoutMS     int    `json:"render_timeout_ms,omitempty"                     example:"8000"`
	JobID               string `json:"job_id,omitempty"                                example:"550e8400-e29b-41d4-a716-446655440000"`
}

// FieldResponse is a single resolved field in the response.
//
//	@Description	A data field extracted by Tier 2 running on the rendered DOM.
type FieldResponse struct {
	Value  string `json:"value"  example:"29.99"`
	Source string `json:"source" example:"json-ld"`
}

// FieldDebugResponse extends FieldResponse with provenance metadata for debug mode.
//
//	@Description	A data field with full provenance metadata (debug only).
type FieldDebugResponse struct {
	Value      string  `json:"value"      example:"29.99"`
	Source     string  `json:"source"     example:"json-ld"`
	Confidence float64 `json:"confidence" example:"0.95"`
	Priority   int     `json:"priority"   example:"1"`
}

// RenderDebug holds diagnostic information only populated when ?debug=true.
//
//	@Description	Internal diagnostic data for Tier 3 rendering (debug mode only).
type RenderDebug struct {
	HollowScore float64                       `json:"hollow_score" example:"0.10"`
	Fields      map[string]FieldDebugResponse `json:"fields"`
}

// RenderResponse is the JSON response for POST /tier3/render.
//
//	@Description	Tier 3 render result including decision, page type, escalation reason, and extracted fields.
type RenderResponse struct {
	JobID              string                   `json:"job_id"               example:"550e8400-e29b-41d4-a716-446655440000"`
	Decision           string                   `json:"decision"             example:"Done"`
	PageType           string                   `json:"page_type"            example:"content-rich"`
	PageTypeConfidence float64                  `json:"page_type_confidence" example:"0.90"`
	ElapsedMS          int64                    `json:"elapsed_ms"           example:"320"`
	EscalationReason   string                   `json:"escalation_reason"    example:""`
	Fields             map[string]FieldResponse `json:"fields"`
	// populated only when ?debug=true
	Debug *RenderDebug `json:"debug,omitempty"`
}

// ErrorResponse is used for 4xx / 5xx JSON error replies.
//
//	@Description	Error detail returned on failure.
type ErrorResponse struct {
	Error string `json:"error" example:"dial tcp: connection refused"`
}

// Render handles POST /tier3/render.
//
//	@Summary		Tier 3 browser render & extraction
//	@Description	Fetches the URL via Tier 1, runs Tier 2 analysis, and — if Tier 2 returns Escalate —
//	@Description	renders the page in a headless Chrome instance and re-runs Tier 2 on the rendered DOM.
//	@Tags			tier3
//	@Accept			json
//	@Produce		json
//	@Param			request	body		RenderRequest	true	"Render parameters"
//	@Param			debug	query		bool			false	"Include diagnostic debug info"
//	@Success		200		{object}	RenderResponse
//	@Failure		400		{object}	ErrorResponse
//	@Failure		502		{object}	ErrorResponse
//	@Router			/tier3/render [post]
func (h *Handler) Render(c *gin.Context) {
	debugMode := c.Query("debug") == "true"

	var req RenderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	pReq := &pipeline.Request{
		URL:         req.URL,
		APIEndpoint: req.APIEndpoint,
	}
	if req.FetchTimeoutMS > 0 {
		pReq.Timeout = time.Duration(req.FetchTimeoutMS) * time.Millisecond
	}

	analyzer := h.analyzer
	if req.ExtractionTimeoutMS > 0 {
		overrideCfg := h.t2cfg
		overrideCfg.ExtractionTimeoutMs = req.ExtractionTimeoutMS
		analyzer = tier2.New(overrideCfg, h.log)
	}

	// Determine job ID.
	jobID := req.JobID
	if jobID == "" {
		if id, err := h.store.CreateJob(c.Request.Context(), req.URL); err == nil {
			jobID = id
		}
	}

	// Try cached Tier2 result first.
	if jobID != "" {
		if job, err := h.store.GetJob(c.Request.Context(), jobID); err == nil && job.Tier2 != nil {
			analysis := api.Tier2ResultToAnalysis(job.Tier2)
			if analysis.Decision != pipeline.DecisionEscalate {
				resp := renderResponseFromAnalysis(analysis, debugMode)
				resp.JobID = jobID
				c.JSON(http.StatusOK, resp)
				return
			}
			// Escalated — run Chrome directly without re-running Tier1/Tier2.
			result := h.doRender(c, req, pReq, jobID)
			if result != nil {
				resp := renderResponseFromResult(result, debugMode)
				resp.JobID = jobID
				c.JSON(http.StatusOK, resp)
			}
			return
		}
	}

	// No cached Tier2 — run full Tier1 → Tier2 → optionally Tier3.
	var resp *pipeline.Response

	if jobID != "" {
		if job, err := h.store.GetJob(c.Request.Context(), jobID); err == nil && job.Tier1 != nil {
			resp = api.Tier1ResultToResponse(job.Tier1, pReq)
		}
	}

	if resp == nil {
		scraper := h.scraper
		if req.FetchTimeoutMS > 0 {
			scraper = tier1.New(pReq.Timeout)
		}
		var err error
		resp, err = scraper.Fetch(c.Request.Context(), pReq)
		if err != nil {
			c.JSON(http.StatusBadGateway, ErrorResponse{Error: err.Error()})
			return
		}
		if jobID != "" {
			headers := make(map[string]string, len(resp.Headers))
			for k, vs := range resp.Headers {
				if len(vs) > 0 {
					headers[k] = vs[0]
				}
			}
			t1r := &store.Tier1Result{
				StatusCode:  resp.StatusCode,
				FinalURL:    resp.FinalURL,
				ContentType: resp.ContentType,
				ElapsedMS:   resp.Elapsed.Milliseconds(),
				Headers:     headers,
				Body:        string(resp.Body),
				FetchedAt:   time.Now().UTC(),
			}
			_ = h.store.SaveTier1(c.Request.Context(), jobID, t1r)
		}
	}

	analysis := analyzer.Analyze(c.Request.Context(), resp)

	// Persist Tier2.
	if jobID != "" {
		storedFields := make(map[string]store.StoredField, len(analysis.Fields))
		for k, f := range analysis.Fields {
			storedFields[k] = store.StoredField{Value: f.Value, Source: f.Source, Confidence: f.Confidence, Priority: f.Priority}
		}
		t2r := &store.Tier2Result{
			Decision:           decisionString(analysis.Decision),
			PageType:           string(analysis.PageType),
			PageTypeConfidence: analysis.PageTypeConfidence,
			HollowScore:        analysis.HollowScore,
			TechHints: store.StoredTechHints{
				IsNextJS:     analysis.TechHints.IsNextJS,
				IsCloudflare: analysis.TechHints.IsCloudflare,
				CFChallenge:  analysis.TechHints.CFChallenge,
				IsJSON:       analysis.TechHints.IsJSON,
				IsPHP:        analysis.TechHints.IsPHP,
			},
			Fields:     storedFields,
			ElapsedMS:  analysis.Elapsed.Milliseconds(),
			AnalyzedAt: time.Now().UTC(),
		}
		_ = h.store.SaveTier2(c.Request.Context(), jobID, t2r)
	}

	// Short-circuit: if Tier 2 already succeeded, no need to render.
	if analysis.Decision != pipeline.DecisionEscalate {
		out := renderResponseFromAnalysis(analysis, debugMode)
		out.JobID = jobID
		c.JSON(http.StatusOK, out)
		return
	}

	// Tier 2 escalated — fire up the browser.
	h.log.Debug(c.Request.Context(), "tier3: tier2 escalated, launching browser render", logger.Field{Key: "url", Value: req.URL})
	result := h.doRender(c, req, pReq, jobID)
	if result != nil {
		out := renderResponseFromResult(result, debugMode)
		out.JobID = jobID
		c.JSON(http.StatusOK, out)
	}
}

// doRender performs the Chrome render and persists the result. Returns nil if it already wrote the response.
func (h *Handler) doRender(c *gin.Context, req RenderRequest, pReq *pipeline.Request, jobID string) *pipeline.RenderResult {
	var result *pipeline.RenderResult

	if req.RenderTimeoutMS > 0 {
		overrideT3cfg := config.Tier3Config{
			PoolSize:        h.renderer.PoolSize(),
			RenderTimeoutMs: req.RenderTimeoutMS,
		}
		renderer, err2 := tier3.New(overrideT3cfg, h.t2cfg, h.log)
		if err2 != nil {
			h.log.Error(c.Request.Context(), "tier3: failed to create per-request renderer, falling back to default",
				logger.Field{Key: "url", Value: req.URL}, logger.Field{Key: "error", Value: err2.Error()})
		} else {
			defer func() { _ = renderer.Close() }()
			result = renderer.Render(c.Request.Context(), pReq)
		}
	}
	if result == nil {
		result = h.renderer.Render(c.Request.Context(), pReq)
	}

	// Persist Tier3.
	if jobID != "" && result != nil {
		storedFields := make(map[string]store.StoredField, len(result.Fields))
		for k, f := range result.Fields {
			storedFields[k] = store.StoredField{Value: f.Value, Source: f.Source, Confidence: f.Confidence, Priority: f.Priority}
		}
		t3r := &store.Tier3Result{
			Decision:           decisionString(result.Decision),
			PageType:           string(result.PageType),
			PageTypeConfidence: result.PageTypeConfidence,
			HollowScore:        result.HollowScore,
			EscalationReason:   result.EscalationReason,
			Fields:             storedFields,
			ElapsedMS:          result.Elapsed.Milliseconds(),
			RenderedAt:         time.Now().UTC(),
		}
		_ = h.store.SaveTier3(c.Request.Context(), jobID, t3r)
	}

	return result
}

func renderResponseFromAnalysis(a *pipeline.AnalysisResult, debugMode bool) RenderResponse {
	fields := make(map[string]FieldResponse, len(a.Fields))
	for k, f := range a.Fields {
		fields[k] = FieldResponse{Value: f.Value, Source: f.Source}
	}
	out := RenderResponse{
		Decision:           decisionString(a.Decision),
		PageType:           string(a.PageType),
		PageTypeConfidence: a.PageTypeConfidence,
		ElapsedMS:          a.Elapsed.Milliseconds(),
		Fields:             fields,
	}
	if debugMode {
		debugFields := make(map[string]FieldDebugResponse, len(a.Fields))
		for k, f := range a.Fields {
			debugFields[k] = FieldDebugResponse{Value: f.Value, Source: f.Source, Confidence: f.Confidence, Priority: f.Priority}
		}
		out.Debug = &RenderDebug{HollowScore: a.HollowScore, Fields: debugFields}
	}
	return out
}

func renderResponseFromResult(r *pipeline.RenderResult, debugMode bool) RenderResponse {
	fields := make(map[string]FieldResponse, len(r.Fields))
	for k, f := range r.Fields {
		fields[k] = FieldResponse{Value: f.Value, Source: f.Source}
	}
	out := RenderResponse{
		Decision:           decisionString(r.Decision),
		PageType:           string(r.PageType),
		PageTypeConfidence: r.PageTypeConfidence,
		ElapsedMS:          r.Elapsed.Milliseconds(),
		EscalationReason:   r.EscalationReason,
		Fields:             fields,
	}
	if debugMode {
		debugFields := make(map[string]FieldDebugResponse, len(r.Fields))
		for k, f := range r.Fields {
			debugFields[k] = FieldDebugResponse{Value: f.Value, Source: f.Source, Confidence: f.Confidence, Priority: f.Priority}
		}
		out.Debug = &RenderDebug{HollowScore: r.HollowScore, Fields: debugFields}
	}
	return out
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
