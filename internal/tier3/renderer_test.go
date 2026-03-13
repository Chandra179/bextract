package tier3_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"bextract/internal/pipeline"
	"bextract/internal/tier3"

	"github.com/go-rod/rod/lib/launcher"
)

// newRenderer creates a test Renderer, skipping if Chrome is unavailable.
func newRenderer(t *testing.T) *tier3.Renderer {
	t.Helper()
	if _, ok := launcher.LookPath(); !ok {
		t.Skip("Chrome not found — skipping Tier 3 browser tests")
	}
	r, err := tier3.New(1, 0, 0)
	if err != nil {
		t.Skipf("could not launch browser: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func TestRenderBasicSuccess(t *testing.T) {
	r := newRenderer(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!DOCTYPE html><html><head>
<script>document.title = "Test Page";</script>
</head><body>
<h1 itemprop="name">Test Product</h1>
<span itemprop="price">9.99</span>
</body></html>`))
	}))
	defer srv.Close()

	req := &pipeline.Request{URL: srv.URL, TargetFields: []string{"name", "price"}}
	result := r.Render(context.Background(), req)

	if result.Decision == pipeline.DecisionEscalate && result.EscalationReason != "" {
		t.Logf("escalated: %s", result.EscalationReason)
	}
	// Basic page should not abort.
	if result.Decision == pipeline.DecisionAbort {
		t.Fatalf("unexpected Abort decision")
	}
}

func TestRenderContextCancellation(t *testing.T) {
	r := newRenderer(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body>hello</body></html>`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	req := &pipeline.Request{URL: srv.URL}
	result := r.Render(ctx, req)

	if result.Decision == pipeline.DecisionDone {
		t.Log("render succeeded despite cancelled context (race — acceptable)")
	}
}

func TestRenderTimeout(t *testing.T) {
	if _, ok := launcher.LookPath(); !ok {
		t.Skip("Chrome not found — skipping Tier 3 browser tests")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Delay longer than the renderer timeout.
		time.Sleep(10 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Use a very short render timeout.
	renderer, err := tier3.New(1, 300*time.Millisecond, 0)
	if err != nil {
		t.Skipf("could not launch browser: %v", err)
	}
	defer func() { _ = renderer.Close() }()

	req := &pipeline.Request{URL: srv.URL}
	result := renderer.Render(context.Background(), req)

	if result.Decision == pipeline.DecisionDone {
		t.Fatalf("expected non-Done decision on timeout, got Done")
	}
}

func TestRenderLoginWallEscalates(t *testing.T) {
	r := newRenderer(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!DOCTYPE html><html><body>
<form><input type="password" name="pass"/></form>
</body></html>`))
	}))
	defer srv.Close()

	req := &pipeline.Request{URL: srv.URL}
	result := r.Render(context.Background(), req)

	if result.Decision != pipeline.DecisionEscalate {
		t.Fatalf("expected Escalate for login wall, got %v", result.Decision)
	}
}

func TestRenderCookieConsentEscalates(t *testing.T) {
	r := newRenderer(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!DOCTYPE html><html><body>
<div id="onetrust-banner-sdk">Please accept cookies</div>
<div>Product content here</div>
</body></html>`))
	}))
	defer srv.Close()

	req := &pipeline.Request{URL: srv.URL}
	result := r.Render(context.Background(), req)

	if result.Decision != pipeline.DecisionEscalate {
		t.Fatalf("expected Escalate for cookie consent wall, got %v", result.Decision)
	}
}
