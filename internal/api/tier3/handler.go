package tier3handler

import (
	"net/http"
	"time"

	"bextract/internal/pipeline"
	"bextract/internal/tier1"
	"bextract/internal/tier2"
	"bextract/internal/tier3"

	"github.com/gin-gonic/gin"
)

// Handler chains Tier 1 fetch → Tier 2 analysis → Tier 3 render.
type Handler struct {
	scraper  *tier1.Scraper
	analyzer *tier2.Analyzer
	renderer *tier3.Renderer
}

// New creates a Handler. Returns an error if Chrome is unavailable.
// Pass 0 for any timeout/pool argument to use tier defaults.
func New(fetchTimeoutMS, extractionTimeoutMS, renderTimeoutMS, poolSize int) (*Handler, error) {
	renderer, err := tier3.New(
		poolSize,
		time.Duration(renderTimeoutMS)*time.Millisecond,
		time.Duration(extractionTimeoutMS)*time.Millisecond,
	)
	if err != nil {
		return nil, err
	}
	return &Handler{
		scraper:  tier1.New(time.Duration(fetchTimeoutMS) * time.Millisecond),
		analyzer: tier2.New(time.Duration(extractionTimeoutMS) * time.Millisecond),
		renderer: renderer,
	}, nil
}

// RenderRequest is the JSON body for POST /tier3/render.
//
//	@Description	URL and optional configuration for Tier 3 browser rendering.
type RenderRequest struct {
	URL                 string   `json:"url"                          binding:"required" example:"https://example.com/product/123"`
	APIEndpoint         string   `json:"api_endpoint,omitempty"                          example:"https://api.example.com/product/123"`
	TargetFields        []string `json:"target_fields,omitempty"                         example:"[\"price\",\"title\"]"`
	FetchTimeoutMS      int      `json:"fetch_timeout_ms,omitempty"                      example:"10000"`
	ExtractionTimeoutMS int      `json:"extraction_timeout_ms,omitempty"                 example:"5000"`
	RenderTimeoutMS     int      `json:"render_timeout_ms,omitempty"                     example:"8000"`
}

// RenderResponse is the JSON response for POST /tier3/render.
//
//	@Description	Tier 3 render result including decision, hollow detection, escalation reason, and extracted fields.
type RenderResponse struct {
	Decision         string                            `json:"decision"          example:"Done"`
	IsHollow         bool                              `json:"is_hollow"         example:"false"`
	HollowScore      float64                           `json:"hollow_score"      example:"0.10"`
	ElapsedMS        int64                             `json:"elapsed_ms"        example:"320"`
	EscalationReason string                            `json:"escalation_reason" example:""`
	Fields           map[string]ExtractedFieldResponse `json:"fields"`
}

// ExtractedFieldResponse is a single resolved field in the response.
//
//	@Description	A data field extracted by Tier 2 running on the rendered DOM, with provenance metadata.
type ExtractedFieldResponse struct {
	Value      string  `json:"value"      example:"29.99"`
	Source     string  `json:"source"     example:"json-ld"`
	Confidence float64 `json:"confidence" example:"0.95"`
	Priority   int     `json:"priority"   example:"1"`
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
//	@Success		200		{object}	RenderResponse
//	@Failure		400		{object}	ErrorResponse
//	@Failure		502		{object}	ErrorResponse
//	@Router			/tier3/render [post]
func (h *Handler) Render(c *gin.Context) {
	var req RenderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	pReq := &pipeline.Request{
		URL:          req.URL,
		APIEndpoint:  req.APIEndpoint,
		TargetFields: req.TargetFields,
	}
	if req.FetchTimeoutMS > 0 {
		pReq.Timeout = time.Duration(req.FetchTimeoutMS) * time.Millisecond
	}

	scraper := h.scraper
	if req.FetchTimeoutMS > 0 {
		scraper = tier1.New(pReq.Timeout)
	}
	analyzer := h.analyzer
	if req.ExtractionTimeoutMS > 0 {
		analyzer = tier2.New(time.Duration(req.ExtractionTimeoutMS) * time.Millisecond)
	}

	resp, err := scraper.Fetch(c.Request.Context(), pReq)
	if err != nil {
		c.JSON(http.StatusBadGateway, ErrorResponse{Error: err.Error()})
		return
	}

	analysis := analyzer.Analyze(c.Request.Context(), resp)

	// Short-circuit: if Tier 2 already succeeded, no need to render.
	if analysis.Decision != pipeline.DecisionEscalate {
		c.JSON(http.StatusOK, renderResponseFromAnalysis(analysis))
		return
	}

	// Tier 2 escalated — fire up the browser.
	renderReq := pReq
	if req.RenderTimeoutMS > 0 {
		renderer, err2 := tier3.New(0, time.Duration(req.RenderTimeoutMS)*time.Millisecond, time.Duration(req.ExtractionTimeoutMS)*time.Millisecond)
		if err2 == nil {
			defer func() { _ = renderer.Close() }()
			result := renderer.Render(c.Request.Context(), renderReq)
			c.JSON(http.StatusOK, renderResponseFromResult(result))
			return
		}
	}

	result := h.renderer.Render(c.Request.Context(), renderReq)
	c.JSON(http.StatusOK, renderResponseFromResult(result))
}

func renderResponseFromAnalysis(a *pipeline.AnalysisResult) RenderResponse {
	fields := make(map[string]ExtractedFieldResponse, len(a.Fields))
	for k, f := range a.Fields {
		fields[k] = ExtractedFieldResponse{Value: f.Value, Source: f.Source, Confidence: f.Confidence, Priority: f.Priority}
	}
	return RenderResponse{
		Decision:    decisionString(a.Decision),
		IsHollow:    a.IsHollow,
		HollowScore: a.HollowScore,
		ElapsedMS:   a.Elapsed.Milliseconds(),
		Fields:      fields,
	}
}

func renderResponseFromResult(r *pipeline.RenderResult) RenderResponse {
	fields := make(map[string]ExtractedFieldResponse, len(r.Fields))
	for k, f := range r.Fields {
		fields[k] = ExtractedFieldResponse{Value: f.Value, Source: f.Source, Confidence: f.Confidence, Priority: f.Priority}
	}
	return RenderResponse{
		Decision:         decisionString(r.Decision),
		IsHollow:         r.IsHollow,
		HollowScore:      r.HollowScore,
		ElapsedMS:        r.Elapsed.Milliseconds(),
		EscalationReason: r.EscalationReason,
		Fields:           fields,
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
