package tier3handler

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

// newRouter attempts to build the tier3 handler. If Chrome is unavailable it
// returns nil — tests must skip in that case.
func newRouter() *gin.Engine {
	h, err := New(0, 0, 0, 0)
	if err != nil {
		return nil
	}
	r := gin.New()
	r.POST("/api/v1/tier3/render", h.Render)
	return r
}

// TestRenderHandlerMissingURL verifies that omitting url returns 400 (no Chrome needed).
func TestRenderHandlerMissingURL(t *testing.T) {
	r := newRouter()
	if r == nil {
		t.Skip("Chrome unavailable, skipping tier3 handler tests")
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/tier3/render", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")

	r.ServeHTTP(w, req)

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

// TestRenderHandlerInvalidJSON verifies that malformed JSON returns 400.
func TestRenderHandlerInvalidJSON(t *testing.T) {
	r := newRouter()
	if r == nil {
		t.Skip("Chrome unavailable, skipping tier3 handler tests")
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/tier3/render", strings.NewReader(`not-json`))
	req.Header.Set("Content-Type", "application/json")

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want 400", w.Code)
	}
}

// TestRenderHandlerUnreachableHost verifies that an unreachable origin returns 502.
func TestRenderHandlerUnreachableHost(t *testing.T) {
	r := newRouter()
	if r == nil {
		t.Skip("Chrome unavailable, skipping tier3 handler tests")
	}

	body := `{"url":"http://127.0.0.1:1","fetch_timeout_ms":500}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/tier3/render", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("got status %d, want 502", w.Code)
	}
}

// TestRenderHandlerStaticPageShortCircuit verifies that a rich static page
// returns 200 without needing browser rendering (Tier 2 returns Done).
func TestRenderHandlerStaticPageShortCircuit(t *testing.T) {
	r := newRouter()
	if r == nil {
		t.Skip("Chrome unavailable, skipping tier3 handler tests")
	}

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
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/tier3/render", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp RenderResponse
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

// TestRenderHandlerResponseShape verifies all expected top-level fields are present.
func TestRenderHandlerResponseShape(t *testing.T) {
	r := newRouter()
	if r == nil {
		t.Skip("Chrome unavailable, skipping tier3 handler tests")
	}

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body><p>hello</p></body></html>")
	}))
	defer origin.Close()

	reqBody := fmt.Sprintf(`{"url":%q}`, origin.URL)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/tier3/render", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200; body: %s", w.Code, w.Body.String())
	}

	// Decode into a raw map to verify all top-level keys exist.
	var m map[string]any
	if err := json.NewDecoder(w.Body).Decode(&m); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	for _, key := range []string{"decision", "is_hollow", "hollow_score", "elapsed_ms", "escalation_reason", "fields"} {
		if _, ok := m[key]; !ok {
			t.Errorf("response missing key %q", key)
		}
	}
}
