package tier2handler

import (
	"net/http"
	"time"

	"bextract/internal/pipeline"
	"bextract/internal/tier1"
	"bextract/internal/tier2"

	"github.com/gin-gonic/gin"
)

// Handler holds Tier 1 + Tier 2 and exposes HTTP handlers.
type Handler struct {
	scraper  *tier1.Scraper
	analyzer *tier2.Analyzer
}

// New creates a Handler with default timeouts (0 → tier defaults).
func New(fetchTimeoutMS, extractionTimeoutMS int) *Handler {
	return &Handler{
		scraper:  tier1.New(time.Duration(fetchTimeoutMS) * time.Millisecond),
		analyzer: tier2.New(time.Duration(extractionTimeoutMS) * time.Millisecond),
	}
}

// AnalyzeRequest is the JSON body for POST /tier2/analyze.
//
//	@Description	URL, optional API endpoint, and target fields for Tier 2 analysis.
type AnalyzeRequest struct {
	URL                string   `json:"url"                      binding:"required" example:"https://example.com/product/123"`
	APIEndpoint        string   `json:"api_endpoint,omitempty"                      example:"https://api.example.com/product/123"`
	TargetFields       []string `json:"target_fields,omitempty"                     example:"[\"price\",\"title\"]"`
	FetchTimeoutMS     int      `json:"fetch_timeout_ms,omitempty"                  example:"10000"`
	ExtractionTimeoutMS int     `json:"extraction_timeout_ms,omitempty"             example:"5000"`
}

// ExtractedFieldResponse is a single resolved field in the response.
//
//	@Description	A data field extracted by Tier 2, with provenance metadata.
type ExtractedFieldResponse struct {
	Value      string  `json:"value"      example:"29.99"`
	Source     string  `json:"source"     example:"json-ld"`
	Confidence float64 `json:"confidence" example:"0.95"`
	Priority   int     `json:"priority"   example:"1"`
}

// AnalyzeResponse is the JSON response for POST /tier2/analyze.
//
//	@Description	Tier 2 analysis result including decision, hollow detection, and extracted fields.
type AnalyzeResponse struct {
	Decision    string                            `json:"decision"     example:"Done"`
	IsHollow    bool                              `json:"is_hollow"    example:"false"`
	HollowScore float64                           `json:"hollow_score" example:"0.12"`
	ElapsedMS   int64                             `json:"elapsed_ms"   example:"87"`
	TechHints   TechHintsResponse                 `json:"tech_hints"`
	Fields      map[string]ExtractedFieldResponse `json:"fields"`
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
//	@Success		200		{object}	AnalyzeResponse
//	@Failure		400		{object}	ErrorResponse
//	@Failure		502		{object}	ErrorResponse
//	@Router			/tier2/analyze [post]
func (h *Handler) Analyze(c *gin.Context) {
	var req AnalyzeRequest
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

	// Build a per-request scraper only when a custom fetch timeout is requested.
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

	result := analyzer.Analyze(c.Request.Context(), resp)

	fields := make(map[string]ExtractedFieldResponse, len(result.Fields))
	for k, f := range result.Fields {
		fields[k] = ExtractedFieldResponse{
			Value:      f.Value,
			Source:     f.Source,
			Confidence: f.Confidence,
			Priority:   f.Priority,
		}
	}

	c.JSON(http.StatusOK, AnalyzeResponse{
		Decision:    decisionString(result.Decision),
		IsHollow:    result.IsHollow,
		HollowScore: result.HollowScore,
		ElapsedMS:   result.Elapsed.Milliseconds(),
		TechHints: TechHintsResponse{
			IsNextJS:     result.TechHints.IsNextJS,
			IsCloudflare: result.TechHints.IsCloudflare,
			CFChallenge:  result.TechHints.CFChallenge,
			IsJSON:       result.TechHints.IsJSON,
			IsPHP:        result.TechHints.IsPHP,
		},
		Fields: fields,
	})
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
