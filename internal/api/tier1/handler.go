package tier1handler

import (
	"net/http"
	"time"

	"bextract/internal/pipeline"
	"bextract/internal/tier1"

	"github.com/gin-gonic/gin"
)

// Handler holds the Tier 1 scraper and exposes HTTP handlers.
type Handler struct {
	scraper *tier1.Scraper
}

// New creates a Handler with the given default timeout (0 → 15 s).
func New(defaultTimeoutMS int) *Handler {
	return &Handler{scraper: tier1.New(time.Duration(defaultTimeoutMS) * time.Millisecond)}
}

// FetchRequest is the JSON body for POST /tier1/fetch.
//
//	@Description	Target URL and optional overrides for the Tier 1 static fetch.
type FetchRequest struct {
	URL         string `json:"url"                    binding:"required" example:"https://example.com"`
	APIEndpoint string `json:"api_endpoint,omitempty"                    example:"https://api.example.com/data"`
	TimeoutMS   int    `json:"timeout_ms,omitempty"                      example:"5000"`
}

// FetchResponse is the JSON response returned to the caller.
//
//	@Description	Raw HTTP response metadata and body returned by Tier 1.
type FetchResponse struct {
	StatusCode  int               `json:"status_code"  example:"200"`
	FinalURL    string            `json:"final_url"    example:"https://example.com/"`
	ContentType string            `json:"content_type" example:"text/html"`
	ElapsedMS   int64             `json:"elapsed_ms"   example:"342"`
	Headers     map[string]string `json:"headers"`
	BodySize    int               `json:"body_size"    example:"14823"`
	Body        string            `json:"body"         example:"<html>...</html>"`
}

// ErrorResponse is used for 4xx / 5xx JSON error replies.
//
//	@Description	Error detail returned on failure.
type ErrorResponse struct {
	Error string `json:"error" example:"dial tcp: connection refused"`
}

// Fetch handles POST /tier1/fetch.
//
//	@Summary		Tier 1 static HTTP fetch
//	@Description	Performs a plain HTTP GET (or fetches an API endpoint directly)
//	@Description	using a rotating User-Agent pool. No JavaScript execution.
//	@Tags			tier1
//	@Accept			json
//	@Produce		json
//	@Param			request	body		FetchRequest	true	"Fetch parameters"
//	@Success		200		{object}	FetchResponse
//	@Failure		400		{object}	ErrorResponse
//	@Failure		502		{object}	ErrorResponse
//	@Router			/tier1/fetch [post]
func (h *Handler) Fetch(c *gin.Context) {
	var req FetchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	pReq := &pipeline.Request{
		URL:         req.URL,
		APIEndpoint: req.APIEndpoint,
	}
	if req.TimeoutMS > 0 {
		pReq.Timeout = time.Duration(req.TimeoutMS) * time.Millisecond
	}

	resp, err := h.scraper.Fetch(c.Request.Context(), pReq)
	if err != nil {
		c.JSON(http.StatusBadGateway, ErrorResponse{Error: err.Error()})
		return
	}

	headers := make(map[string]string, len(resp.Headers))
	for k, vs := range resp.Headers {
		if len(vs) > 0 {
			headers[k] = vs[0]
		}
	}

	c.JSON(http.StatusOK, FetchResponse{
		StatusCode:  resp.StatusCode,
		FinalURL:    resp.FinalURL,
		ContentType: resp.ContentType,
		ElapsedMS:   resp.Elapsed.Milliseconds(),
		Headers:     headers,
		BodySize:    len(resp.Body),
		Body:        string(resp.Body),
	})
}
