package tier3

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"bextract/internal/config"
	"bextract/internal/pipeline"
	"bextract/pkg/logger"

	"github.com/go-rod/rod/lib/launcher"
)

// noopLogger satisfies logger.Logger without producing output.
type noopLogger struct{}

func (noopLogger) Debug(ctx context.Context, msg string, fields ...logger.Field) {}
func (noopLogger) Info(ctx context.Context, msg string, fields ...logger.Field)  {}
func (noopLogger) Warn(ctx context.Context, msg string, fields ...logger.Field)  {}
func (noopLogger) Error(ctx context.Context, msg string, fields ...logger.Field) {}

func defaultT2Cfg() config.Tier2Config {
	return config.Defaults().Tier2
}

// ---------------------------------------------------------------------------
// Constructor tests
// ---------------------------------------------------------------------------

func TestNewNoBrowserNoBrowserless(t *testing.T) {
	// When no Chrome binary is on PATH and BrowserlessURL is empty,
	// New must return errNoBrowser.
	t.Setenv("ROD_BROWSER_BIN", "/nonexistent/path/chromium-fake")

	cfg := config.Tier3Config{} // no BrowserlessURL
	_, err := New(cfg, defaultT2Cfg(), noopLogger{})
	if err == nil {
		t.Fatal("expected error when no browser is available, got nil")
	}
	if err != errNoBrowser {
		t.Errorf("expected errNoBrowser, got: %v", err)
	}
}

func TestNewBrowserlessURLConnectFails(t *testing.T) {
	// Point at an HTTP server that isn't a CDP WebSocket endpoint.
	// Connect() must fail (WebSocket handshake error).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not a browser", http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := config.Tier3Config{BrowserlessURL: srv.URL}
	_, err := New(cfg, defaultT2Cfg(), noopLogger{})
	if err == nil {
		t.Fatal("expected error when BrowserlessURL is not a CDP endpoint, got nil")
	}
	t.Logf("got expected error: %v", err)
}

// ---------------------------------------------------------------------------
// escalateResult helper
// ---------------------------------------------------------------------------

func TestEscalateResult(t *testing.T) {
	req := &pipeline.Request{URL: "https://example.com"}
	elapsed := 42 * time.Millisecond
	result := escalateResult(req, "test reason", elapsed)

	if result.Decision != pipeline.DecisionEscalate {
		t.Errorf("expected DecisionEscalate, got %v", result.Decision)
	}
	if result.EscalationReason != "test reason" {
		t.Errorf("expected reason %q, got %q", "test reason", result.EscalationReason)
	}
	if result.Elapsed != elapsed {
		t.Errorf("expected elapsed %v, got %v", elapsed, result.Elapsed)
	}
	if result.OriginalRequest != req {
		t.Error("OriginalRequest should point to the input request")
	}
}

// ---------------------------------------------------------------------------
// Full render integration (requires Chrome — skip if unavailable)
// ---------------------------------------------------------------------------

func TestRenderIntegration(t *testing.T) {
	_, ok := launcher.LookPath()
	if !ok {
		t.Skip("Chrome/Chromium not found — skipping integration test")
	}

	t3cfg := config.Tier3Config{
		PoolSize:        1,
		RenderTimeoutMs: 5000,
	}

	r, err := New(t3cfg, defaultT2Cfg(), noopLogger{})
	if err != nil {
		t.Fatalf("failed to create renderer: %v", err)
	}
	defer r.Close()

	// Serve a simple page with structured data.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html>
<html><head>
  <title>Test Product</title>
  <meta property="og:title" content="Widget" />
  <meta property="og:type" content="product" />
</head><body>
  <h1>Widget for sale</h1>
  <p>A fine widget at a fair price.</p>
</body></html>`)
	}))
	defer srv.Close()

	result := r.Render(context.Background(), &pipeline.Request{URL: srv.URL})

	if result.Decision == pipeline.DecisionEscalate && result.EscalationReason != "" {
		t.Fatalf("unexpected escalation: %s", result.EscalationReason)
	}
	if result.Elapsed <= 0 {
		t.Error("Elapsed should be positive")
	}
	if result.OriginalRequest == nil {
		t.Error("OriginalRequest should not be nil")
	}
}

func TestRenderJSContent(t *testing.T) {
	_, ok := launcher.LookPath()
	if !ok {
		t.Skip("Chrome/Chromium not found — skipping integration test")
	}

	t3cfg := config.Tier3Config{
		PoolSize:        1,
		RenderTimeoutMs: 5000,
	}
	r, err := New(t3cfg, defaultT2Cfg(), noopLogger{})
	if err != nil {
		t.Fatalf("failed to create renderer: %v", err)
	}
	defer r.Close()

	// Page with JS-injected JSON-LD — after Chrome renders it the tag is present.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html>
<html><head><title>JS Page</title></head><body>
<div id="root"></div>
<script>
  var s = document.createElement('script');
  s.type = 'application/ld+json';
  s.textContent = JSON.stringify({"@type":"Product","name":"DynWidget","price":"19.99"});
  document.head.appendChild(s);
  document.getElementById('root').innerHTML = '<h1>DynWidget</h1>';
</script>
</body></html>`)
	}))
	defer srv.Close()

	result := r.Render(context.Background(), &pipeline.Request{URL: srv.URL})

	if result.Decision == pipeline.DecisionEscalate {
		t.Logf("escalation reason: %s", result.EscalationReason)
	}
	if result.Fields != nil {
		if f, ok := result.Fields["name"]; ok {
			if f.Value != "DynWidget" {
				t.Errorf("expected name=DynWidget, got %q", f.Value)
			}
		}
	}
}

func TestRenderLoginWallDetection(t *testing.T) {
	_, ok := launcher.LookPath()
	if !ok {
		t.Skip("Chrome/Chromium not found — skipping integration test")
	}

	t3cfg := config.Tier3Config{
		PoolSize:        1,
		RenderTimeoutMs: 5000,
	}
	r, err := New(t3cfg, defaultT2Cfg(), noopLogger{})
	if err != nil {
		t.Fatalf("failed to create renderer: %v", err)
	}
	defer r.Close()

	// Serve a page that redirects to /login.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/product" {
			http.Redirect(w, req, "/login", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html>
<html><head><title>Login</title></head><body>
<form><input type="password" name="pw"/></form>
</body></html>`)
	}))
	defer srv.Close()

	result := r.Render(context.Background(), &pipeline.Request{URL: srv.URL + "/product"})

	if result.Decision != pipeline.DecisionEscalate {
		t.Errorf("expected DecisionEscalate for login wall, got %v", result.Decision)
	}
	if result.EscalationReason == "" {
		t.Error("expected non-empty escalation reason for login wall")
	}
}
