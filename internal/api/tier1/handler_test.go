package tier1handler

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
	h := New(0)
	r.POST("/api/v1/tier1/fetch", h.Fetch)
	return r
}

// TestFetchHandlerSuccess verifies a happy-path fetch returns 200 with expected fields.
func TestFetchHandlerSuccess(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<html><body>hello</body></html>")
	}))
	defer origin.Close()

	body := fmt.Sprintf(`{"url":%q}`, origin.URL)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/tier1/fetch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	newRouter().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp FetchResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("status_code: got %d, want 200", resp.StatusCode)
	}
	if resp.ContentType != "text/html" {
		t.Errorf("content_type: got %q, want text/html", resp.ContentType)
	}
	if resp.BodySize == 0 {
		t.Error("body_size should be > 0")
	}
	if resp.ElapsedMS < 0 {
		t.Errorf("elapsed_ms should be >= 0, got %d", resp.ElapsedMS)
	}
}

// TestFetchHandlerMissingURL verifies that omitting url returns 400.
func TestFetchHandlerMissingURL(t *testing.T) {
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/tier1/fetch", strings.NewReader(`{}`))
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

// TestFetchHandlerInvalidJSON verifies that malformed JSON returns 400.
func TestFetchHandlerInvalidJSON(t *testing.T) {
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/tier1/fetch", strings.NewReader(`not-json`))
	req.Header.Set("Content-Type", "application/json")

	newRouter().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want 400", w.Code)
	}
}

// TestFetchHandlerUnreachableHost verifies that an unreachable URL returns 502.
func TestFetchHandlerUnreachableHost(t *testing.T) {
	body := `{"url":"http://127.0.0.1:1","timeout_ms":500}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/tier1/fetch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	newRouter().ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("got status %d, want 502", w.Code)
	}
	var resp ErrorResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.Error == "" {
		t.Error("error field should not be empty")
	}
}

// TestFetchHandlerAPIEndpointOverride verifies that api_endpoint is fetched instead of url.
func TestFetchHandlerAPIEndpointOverride(t *testing.T) {
	const jsonPayload = `{"price":"9.99"}`
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/data" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, jsonPayload)
			return
		}
		t.Errorf("unexpected request to %q", r.URL.Path)
		http.NotFound(w, r)
	}))
	defer origin.Close()

	reqBody := fmt.Sprintf(`{"url":%q,"api_endpoint":%q}`, origin.URL+"/page", origin.URL+"/api/data")
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/tier1/fetch", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	newRouter().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}
	var resp FetchResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if resp.ContentType != "application/json" {
		t.Errorf("content_type: got %q, want application/json", resp.ContentType)
	}
	if resp.Body != jsonPayload {
		t.Errorf("body: got %q, want %q", resp.Body, jsonPayload)
	}
}

// TestFetchHandlerRedirect verifies that redirects are followed and final_url is updated.
func TestFetchHandlerRedirect(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/end", http.StatusFound)
			return
		}
		fmt.Fprint(w, "end page")
	}))
	defer origin.Close()

	reqBody := fmt.Sprintf(`{"url":%q}`, origin.URL+"/start")
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/tier1/fetch", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	newRouter().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}
	var resp FetchResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !strings.HasSuffix(resp.FinalURL, "/end") {
		t.Errorf("final_url %q should end with /end", resp.FinalURL)
	}
}

// TestFetchHandlerHeadersPresent verifies the headers map is populated.
func TestFetchHandlerHeadersPresent(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom-Header", "test-value")
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "ok")
	}))
	defer origin.Close()

	reqBody := fmt.Sprintf(`{"url":%q}`, origin.URL)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/tier1/fetch", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")

	newRouter().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", w.Code)
	}
	var resp FetchResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if len(resp.Headers) == 0 {
		t.Error("headers map should not be empty")
	}
}
