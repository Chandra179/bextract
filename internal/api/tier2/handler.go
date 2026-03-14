package tier2handler

import (
	"net/http"
	"time"

	"bextract/internal/api"
	"bextract/internal/config"
	"bextract/internal/pipeline"
	"bextract/internal/tier1"
	"bextract/internal/tier2"
	"bextract/pkg/logger"
	"bextract/pkg/store"

	"github.com/gin-gonic/gin"
)

// Handler holds Tier 1 + Tier 2 and exposes HTTP handlers.
type Handler struct {
	scraper  *tier1.Scraper
	analyzer *tier2.Analyzer
	cfg      config.Tier2Config
	log      logger.Logger
	store    store.Store
}

// New creates a Handler from the provided configs.
func New(t1cfg config.Tier1Config, t2cfg config.Tier2Config, log logger.Logger, st store.Store) *Handler {
	return &Handler{
		scraper:  tier1.New(time.Duration(t1cfg.TimeoutMs) * time.Millisecond),
		analyzer: tier2.New(t2cfg, log),
		cfg:      t2cfg,
		log:      log,
		store:    st,
	}
}

// AnalyzeRequest is the JSON body for POST /tier2/analyze.
//
//	@Description	URL and optional configuration for Tier 2 analysis.
type AnalyzeRequest struct {
	URL                 string `json:"url"                      binding:"required" example:"https://example.com/product/123"`
	APIEndpoint         string `json:"api_endpoint,omitempty"                      example:"https://api.example.com/product/123"`
	FetchTimeoutMS      int    `json:"fetch_timeout_ms,omitempty"                  example:"10000"`
	ExtractionTimeoutMS int    `json:"extraction_timeout_ms,omitempty"             example:"5000"`
	JobID               string `json:"job_id,omitempty"                            example:"550e8400-e29b-41d4-a716-446655440000"`
}

// FieldResponse is a single resolved field in the response.
//
//	@Description	A data field extracted by Tier 2.
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

// TechHintsResponse exposes technology signals detected in Stage 1.
//
//	@Description	Technology fingerprints detected from HTTP headers.
type TechHintsResponse struct {
	IsNextJS     bool `json:"is_nextjs"     example:"true"`
	IsCloudflare bool `json:"is_cloudflare" example:"false"`
	CFChallenge  bool `json:"cf_challenge"  example:"false"`
	IsJSON       bool `json:"is_json"       example:"false"`
	IsPHP        bool `json:"is_php"        example:"false"`
}

// AnalyzeDebug holds diagnostic information only populated when ?debug=true.
//
//	@Description	Internal diagnostic data for Tier 2 analysis (debug mode only).
type AnalyzeDebug struct {
	HollowScore float64                       `json:"hollow_score" example:"0.12"`
	TechHints   TechHintsResponse             `json:"tech_hints"`
	Fields      map[string]FieldDebugResponse `json:"fields"`
}

// AnalyzeResponse is the JSON response for POST /tier2/analyze.
//
//	@Description	Tier 2 analysis result including decision, page type, and extracted fields.
type AnalyzeResponse struct {
	JobID              string                   `json:"job_id"               example:"550e8400-e29b-41d4-a716-446655440000"`
	Decision           string                   `json:"decision"             example:"Done"`
	PageType           string                   `json:"page_type"            example:"content-rich"`
	PageTypeConfidence float64                  `json:"page_type_confidence" example:"0.88"`
	ElapsedMS          int64                    `json:"elapsed_ms"           example:"87"`
	Fields             map[string]FieldResponse `json:"fields"`
	// populated only when ?debug=true
	Debug *AnalyzeDebug `json:"debug,omitempty"`
}

// ErrorResponse is used for 4xx / 5xx JSON error replies.
//
//	@Description	Error detail returned on failure.
type ErrorResponse struct {
	Error string `json:"error" example:"dial tcp: connection refused"`
}

// Analyze handles POST /tier2/analyze.
//
//	@Summary		Tier 2 content detection & extraction
//	@Description	Fetches the URL via Tier 1 then runs the full Tier 2 five-stage pipeline:
//	@Description	header analysis, HTML parse, hollow detection, concurrent extraction, and merge.
//	@Tags			tier2
//	@Accept			json
//	@Produce		json
//	@Param			request	body		AnalyzeRequest	true	"Analyze parameters"
//	@Param			debug	query		bool			false	"Include diagnostic debug info"
//	@Success		200		{object}	AnalyzeResponse
//	@Failure		400		{object}	ErrorResponse
//	@Failure		502		{object}	ErrorResponse
//	@Router			/tier2/analyze [post]
func (h *Handler) Analyze(c *gin.Context) {
	debugMode := c.Query("debug") == "true"

	var req AnalyzeRequest
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
		overrideCfg := h.cfg
		overrideCfg.ExtractionTimeoutMs = req.ExtractionTimeoutMS
		analyzer = tier2.New(overrideCfg, h.log)
	}

	// Determine job ID; create one if absent.
	jobID := req.JobID
	if jobID == "" {
		if id, err := h.store.CreateJob(c.Request.Context(), req.URL); err == nil {
			jobID = id
		}
	}

	var resp *pipeline.Response

	// Try to read cached Tier1 result.
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
		// Persist Tier1 result.
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

	result := analyzer.Analyze(c.Request.Context(), resp)

	// Persist Tier2 result.
	if jobID != "" {
		storedFields := make(map[string]store.StoredField, len(result.Fields))
		for k, f := range result.Fields {
			storedFields[k] = store.StoredField{Value: f.Value, Source: f.Source, Confidence: f.Confidence, Priority: f.Priority}
		}
		t2r := &store.Tier2Result{
			Decision:           decisionString(result.Decision),
			PageType:           string(result.PageType),
			PageTypeConfidence: result.PageTypeConfidence,
			HollowScore:        result.HollowScore,
			TechHints: store.StoredTechHints{
				IsNextJS:     result.TechHints.IsNextJS,
				IsCloudflare: result.TechHints.IsCloudflare,
				CFChallenge:  result.TechHints.CFChallenge,
				IsJSON:       result.TechHints.IsJSON,
				IsPHP:        result.TechHints.IsPHP,
			},
			Fields:     storedFields,
			ElapsedMS:  result.Elapsed.Milliseconds(),
			AnalyzedAt: time.Now().UTC(),
		}
		_ = h.store.SaveTier2(c.Request.Context(), jobID, t2r)
	}

	// Build clean field map.
	fields := make(map[string]FieldResponse, len(result.Fields))
	for k, f := range result.Fields {
		fields[k] = FieldResponse{Value: f.Value, Source: f.Source}
	}

	out := AnalyzeResponse{
		JobID:              jobID,
		Decision:           decisionString(result.Decision),
		PageType:           string(result.PageType),
		PageTypeConfidence: result.PageTypeConfidence,
		ElapsedMS:          result.Elapsed.Milliseconds(),
		Fields:             fields,
	}

	if debugMode {
		debugFields := make(map[string]FieldDebugResponse, len(result.Fields))
		for k, f := range result.Fields {
			debugFields[k] = FieldDebugResponse{
				Value:      f.Value,
				Source:     f.Source,
				Confidence: f.Confidence,
				Priority:   f.Priority,
			}
		}
		out.Debug = &AnalyzeDebug{
			HollowScore: result.HollowScore,
			TechHints: TechHintsResponse{
				IsNextJS:     result.TechHints.IsNextJS,
				IsCloudflare: result.TechHints.IsCloudflare,
				CFChallenge:  result.TechHints.CFChallenge,
				IsJSON:       result.TechHints.IsJSON,
				IsPHP:        result.TechHints.IsPHP,
			},
			Fields: debugFields,
		}
	}

	c.JSON(http.StatusOK, out)
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
