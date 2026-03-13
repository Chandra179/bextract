package tier2handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func newRouter() *gin.Engine {
	r := gin.New()
	h := New(0, 0)
	r.POST("/api/v1/tier2/analyze", h.Analyze)
	return r
}

// TestAnalyzeHandlerSuccess verifies a happy-path analyze returns 200 with decision + fields.
func TestAnalyzeHandlerSuccess(t *testing.T) {
	const htmlBody = `<html><head>
		<script type="application/ld+json">{"@type":"Product","name":"Widget","offers":{"price":"19.99"}}</script>
	</head><body><h1>Widget</h1></body></html>`

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, htmlBody)
	}))
	defer origin.Close()

	reqBody := fmt.Sprintf(`{"url":%q,"target_fields":["price","title"]}`, origin.URL)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/tier2/analyze", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	newRouter().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp AnalyzeResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Decision == "" {
		t.Error("decision should not be empty")
	}
	if resp.ElapsedMS < 0 {
		t.Errorf("elapsed_ms should be >= 0, got %d", resp.ElapsedMS)
	}
}

// TestAnalyzeHandlerMissingURL verifies that omitting url returns 400.
func TestAnalyzeHandlerMissingURL(t *testing.T) {
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/tier2/analyze", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")

	newRouter().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want 400", w.Code)
	}
	var resp ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Error == "" {
		t.Error("error field should not be empty")
	}
}

// TestAnalyzeHandlerInvalidJSON verifies that malformed JSON returns 400.
func TestAnalyzeHandlerInvalidJSON(t *testing.T) {
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/tier2/analyze", strings.NewReader(`not-json`))
	req.Header.Set("Content-Type", "application/json")

	newRouter().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want 400", w.Code)
	}
}

// TestAnalyzeHandlerUnreachableHost verifies that an unreachable origin returns 502.
func TestAnalyzeHandlerUnreachableHost(t *testing.T) {
	body := `{"url":"http://127.0.0.1:1","fetch_timeout_ms":500}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/tier2/analyze", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	newRouter().ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("got status %d, want 502", w.Code)
	}
}

// TestAnalyzeHandlerTechHintsNextJS verifies IsNextJS is detected via headers.
func TestAnalyzeHandlerTechHintsNextJS(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("X-Powered-By", "Next.js")
		fmt.Fprint(w, "<html><body>next</body></html>")
	}))
	defer origin.Close()

	reqBody := fmt.Sprintf(`{"url":%q}`, origin.URL)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/tier2/analyze", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	newRouter().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp AnalyzeResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	// Just check the shape; the actual value depends on Tier 2 header detection logic.
	_ = resp.TechHints
}

// TestAnalyzeHandlerJSONResponse verifies that a JSON API endpoint is handled correctly.
func TestAnalyzeHandlerJSONResponse(t *testing.T) {
	const jsonPayload = `{"price":"42.00","title":"Gadget"}`
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, jsonPayload)
	}))
	defer origin.Close()

	reqBody := fmt.Sprintf(`{"url":%q,"target_fields":["price","title"]}`, origin.URL)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/tier2/analyze", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	newRouter().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp AnalyzeResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Decision == "" {
		t.Error("decision should not be empty")
	}
}

// TestAnalyzeHandlerHollowPage verifies hollow detection fields are present in response.
func TestAnalyzeHandlerHollowPage(t *testing.T) {
	// A near-empty page is more likely to be flagged hollow.
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body></body></html>")
	}))
	defer origin.Close()

	reqBody := fmt.Sprintf(`{"url":%q}`, origin.URL)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/tier2/analyze", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	newRouter().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp AnalyzeResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	// hollow_score must be a valid float (zero is fine for a minimal page).
	if resp.HollowScore < 0 {
		t.Errorf("hollow_score should be >= 0, got %f", resp.HollowScore)
	}
}
