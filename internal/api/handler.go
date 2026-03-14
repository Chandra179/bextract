package api

import (
	"context"
	"net/http"
	"time"

	"bextract/internal/pipeline"

	"github.com/gin-gonic/gin"
)

// RunFunc is the cascade function injected into the Handler.
type RunFunc func(ctx context.Context, req *pipeline.RunRequest) (*pipeline.RunResult, error)

// Handler holds the injected cascade function and serves HTTP requests.
type Handler struct {
	run RunFunc
}

// New creates a Handler with the given cascade function.
func New(run RunFunc) *Handler {
	return &Handler{run: run}
}

// ExtractRequest is the JSON body for POST /extract.
//
//	@Description	URL and optional per-request timeout overrides for the full extraction cascade.
type ExtractRequest struct {
	URL                 string `json:"url"                          binding:"required" example:"https://example.com/product/123"`
	APIEndpoint         string `json:"api_endpoint,omitempty"                          example:"https://api.example.com/product/123"`
	FetchTimeoutMS      int    `json:"fetch_timeout_ms,omitempty"                      example:"10000"`
	ExtractionTimeoutMS int    `json:"extraction_timeout_ms,omitempty"                 example:"5000"`
	JobID               string `json:"job_id,omitempty"                                example:"550e8400-e29b-41d4-a716-446655440000"`
}

// FieldResponse is a single resolved field in the extract response.
//
//	@Description	A data field extracted from the page.
type FieldResponse struct {
	Value  string `json:"value"  example:"29.99"`
	Source string `json:"source" example:"json-ld"`
}

// ExtractResponse is the JSON response for POST /extract.
//
//	@Description	Extract result including which tier produced the final result, the decision, page type, and extracted fields.
type ExtractResponse struct {
	JobID            string                   `json:"job_id"                        example:"550e8400-e29b-41d4-a716-446655440000"`
	Tier             int                      `json:"tier"                          example:"2"`
	Decision         string                   `json:"decision"                      example:"Done"`
	PageType         string                   `json:"page_type"                     example:"content-rich"`
	Fields           map[string]FieldResponse `json:"fields"`
	ElapsedMS        int64                    `json:"elapsed_ms"                    example:"320"`
	EscalationReason string                   `json:"escalation_reason,omitempty"   example:""`
}

// ErrorResponse is used for 4xx / 5xx JSON error replies.
//
//	@Description	Error detail returned on failure.
type ErrorResponse struct {
	Error string `json:"error" example:"dial tcp: connection refused"`
}

// Extract handles POST /extract.
//
//	@Summary		Full extraction cascade
//	@Description	Fetches the URL and cascades through Tier 1 → Tier 2 → Tier 3 → Tier 4, stopping at the first non-Escalate decision.
//	@Tags			extract
//	@Accept			json
//	@Produce		json
//	@Param			request	body		ExtractRequest	true	"Extraction parameters"
//	@Success		200		{object}	ExtractResponse
//	@Failure		400		{object}	ErrorResponse
//	@Failure		502		{object}	ErrorResponse
//	@Router			/extract [post]
func (h *Handler) Extract(c *gin.Context) {
	var req ExtractRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	runReq := &pipeline.RunRequest{
		URL:         req.URL,
		APIEndpoint: req.APIEndpoint,
		JobID:       req.JobID,
	}
	if req.FetchTimeoutMS > 0 {
		runReq.FetchTimeout = time.Duration(req.FetchTimeoutMS) * time.Millisecond
	}
	if req.ExtractionTimeoutMS > 0 {
		runReq.ExtractionTimeout = time.Duration(req.ExtractionTimeoutMS) * time.Millisecond
	}

	result, err := h.run(c.Request.Context(), runReq)
	if err != nil {
		c.JSON(http.StatusBadGateway, ErrorResponse{Error: err.Error()})
		return
	}

	fields := make(map[string]FieldResponse, len(result.Fields))
	for k, f := range result.Fields {
		fields[k] = FieldResponse{Value: f.Value, Source: f.Source}
	}
	c.JSON(http.StatusOK, ExtractResponse{
		JobID:            result.JobID,
		Tier:             result.Tier,
		Decision:         decisionString(result.Decision),
		PageType:         string(result.PageType),
		Fields:           fields,
		ElapsedMS:        result.Elapsed.Milliseconds(),
		EscalationReason: result.EscalationReason,
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
