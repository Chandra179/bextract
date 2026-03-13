package tier1

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bextract/internal/pipeline"
)

func newTestScraper() *Scraper {
	return New(5 * time.Second)
}

// TestFetchBasicSuccess verifies a normal 200 response is fully captured.
func TestFetchBasicSuccess(t *testing.T) {
	const body = "<html><body>hello</body></html>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	s := newTestScraper()
	resp, err := s.Fetch(context.Background(), &pipeline.Request{URL: srv.URL})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("got status %d, want 200", resp.StatusCode)
	}
	if string(resp.Body) != body {
		t.Errorf("got body %q, want %q", resp.Body, body)
	}
	if resp.ContentType != "text/html" {
		t.Errorf("got ContentType %q, want %q", resp.ContentType, "text/html")
	}
	if resp.FinalURL != srv.URL+"/" && resp.FinalURL != srv.URL {
		// httptest servers may redirect / or not; accept both forms
		if !strings.HasPrefix(resp.FinalURL, srv.URL) {
			t.Errorf("FinalURL %q does not start with server URL %q", resp.FinalURL, srv.URL)
		}
	}
	if resp.Elapsed <= 0 {
		t.Errorf("Elapsed should be positive, got %v", resp.Elapsed)
	}
	if resp.OriginalRequest == nil {
		t.Error("OriginalRequest should not be nil")
	}
}

// TestFetchFollowsRedirect verifies that a single redirect is followed and
// FinalURL reflects the destination, not the original URL.
func TestFetchFollowsRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/end", http.StatusFound)
			return
		}
		fmt.Fprint(w, "end")
	}))
	defer srv.Close()

	s := newTestScraper()
	resp, err := s.Fetch(context.Background(), &pipeline.Request{URL: srv.URL + "/start"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("got status %d, want 200", resp.StatusCode)
	}
	if !strings.HasSuffix(resp.FinalURL, "/end") {
		t.Errorf("FinalURL %q should end with /end", resp.FinalURL)
	}
}

// TestFetchMultiRedirect verifies a 3-hop redirect chain resolves to the last URL.
func TestFetchMultiRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/a":
			http.Redirect(w, r, "/b", http.StatusFound)
		case "/b":
			http.Redirect(w, r, "/c", http.StatusFound)
		case "/c":
			fmt.Fprint(w, "final")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	s := newTestScraper()
	resp, err := s.Fetch(context.Background(), &pipeline.Request{URL: srv.URL + "/a"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(resp.FinalURL, "/c") {
		t.Errorf("FinalURL %q should end with /c", resp.FinalURL)
	}
	if string(resp.Body) != "final" {
		t.Errorf("got body %q, want %q", resp.Body, "final")
	}
}

// TestFetchAPIEndpointOverride verifies that when APIEndpoint is set, it is
// fetched instead of URL.
func TestFetchAPIEndpointOverride(t *testing.T) {
	const jsonBody = `{"price": 9.99}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/product" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, jsonBody)
			return
		}
		// The HTML page — should NOT be fetched
		t.Errorf("unexpected request to %q; URL should have been overridden by APIEndpoint", r.URL.Path)
		fmt.Fprint(w, "<html>page</html>")
	}))
	defer srv.Close()

	s := newTestScraper()
	resp, err := s.Fetch(context.Background(), &pipeline.Request{
		URL:         srv.URL + "/page",
		APIEndpoint: srv.URL + "/api/product",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ContentType != "application/json" {
		t.Errorf("got ContentType %q, want application/json", resp.ContentType)
	}
	if string(resp.Body) != jsonBody {
		t.Errorf("got body %q, want %q", resp.Body, jsonBody)
	}
}

// TestFetchContextCancellation verifies that a cancelled context causes Fetch
// to return an error promptly.
func TestFetchContextCancellation(t *testing.T) {
	ready := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(ready)
		// Block until the client disconnects or the test times out.
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	s := newTestScraper()

	done := make(chan error, 1)
	go func() {
		_, err := s.Fetch(ctx, &pipeline.Request{URL: srv.URL})
		done <- err
	}()

	// Wait until the server has received the request, then cancel.
	select {
	case <-ready:
		cancel()
	case <-time.After(3 * time.Second):
		t.Fatal("server did not receive request in time")
	}

	select {
	case err := <-done:
		if err == nil {
			t.Error("expected error from cancelled context, got nil")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Fetch did not return after context cancellation")
	}
}

// TestFetchRequestTimeout verifies that Request.Timeout is respected.
func TestFetchRequestTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		fmt.Fprint(w, "late")
	}))
	defer srv.Close()

	s := newTestScraper()
	_, err := s.Fetch(context.Background(), &pipeline.Request{
		URL:     srv.URL,
		Timeout: 20 * time.Millisecond,
	})
	if err == nil {
		t.Error("expected timeout error, got nil")
	}
}

// TestFetchSetsUserAgent verifies that Fetch injects a non-default User-Agent.
func TestFetchSetsUserAgent(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	s := newTestScraper()
	_, err := s.Fetch(context.Background(), &pipeline.Request{URL: srv.URL})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotUA == "" {
		t.Error("User-Agent header was empty")
	}
	if gotUA == "Go-http-client/1.1" || gotUA == "Go-http-client/2.0" {
		t.Errorf("User-Agent %q looks like a default Go UA; expected a browser UA", gotUA)
	}
}

// TestFetchBodySizeCap verifies that responses larger than 10 MB are truncated
// and do not cause an error.
func TestFetchBodySizeCap(t *testing.T) {
	const overLimit = maxBodySize + 1<<20 // 11 MB
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", overLimit))
		w.Write(bytes.Repeat([]byte("x"), overLimit))
	}))
	defer srv.Close()

	s := newTestScraper()
	resp, err := s.Fetch(context.Background(), &pipeline.Request{URL: srv.URL})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Body) > maxBodySize {
		t.Errorf("body len %d exceeds cap %d", len(resp.Body), maxBodySize)
	}
}

// TestFetchContentTypeStripped verifies that charset and other parameters are
// removed from ContentType, leaving only the MIME type token.
func TestFetchContentTypeStripped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "hi")
	}))
	defer srv.Close()

	s := newTestScraper()
	resp, err := s.Fetch(context.Background(), &pipeline.Request{URL: srv.URL})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ContentType != "text/html" {
		t.Errorf("got ContentType %q, want %q", resp.ContentType, "text/html")
	}
}

// TestUserAgentPoolRoundRobin verifies that all UA strings appear and the
// sequence repeats correctly.
func TestUserAgentPoolRoundRobin(t *testing.T) {
	pool := newUserAgentPool()
	n := len(pool.agents)
	if n == 0 {
		t.Fatal("UA pool is empty")
	}

	seen := make(map[string]int)
	rounds := 3
	for i := 0; i < n*rounds; i++ {
		seen[pool.Next()]++
	}

	if len(seen) != n {
		t.Errorf("expected %d distinct UAs, got %d", n, len(seen))
	}
	for ua, count := range seen {
		if count != rounds {
			t.Errorf("UA %q appeared %d times, want %d", ua, count, rounds)
		}
	}
}
