package tier1

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"bextract/internal/pipeline"
)

const (
	defaultTimeout  = 15 * time.Second
	maxBodySize     = 10 << 20 // 10 MB
)

// Scraper fetches raw HTTP responses for the pipeline.
// A single Scraper instance is safe for concurrent use across goroutines.
type Scraper struct {
	client         *http.Client
	ua             *userAgentPool
	defaultTimeout time.Duration
}

// New constructs a Scraper with a shared HTTP client and UA pool.
// defaultTimeout is used when Request.Timeout is zero; pass 0 to use the
// package default of 15 seconds.
func New(defaultTimeout time.Duration) *Scraper {
	if defaultTimeout == 0 {
		defaultTimeout = 15 * time.Second
	}

	transport := &http.Transport{
		MaxIdleConnsPerHost:   10,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
	}

	// http.Client.Timeout is a hard deadline per request including redirects
	// and body read. We set it per-call via context instead (see Fetch), so
	// the client timeout stays at zero (unlimited) here.
	client := &http.Client{
		Transport: transport,
	}

	return &Scraper{
		client:         client,
		ua:             newUserAgentPool(),
		defaultTimeout: defaultTimeout,
	}
}

// Fetch executes the HTTP request described by req and returns the raw response.
//
// If req.APIEndpoint is non-empty, that URL is fetched instead of req.URL.
// Tier 1 makes no decisions — it either returns a populated Response or an
// error. All status-code interpretation is left to Tier 2.
func (s *Scraper) Fetch(ctx context.Context, req *pipeline.Request) (*pipeline.Response, error) {
	targetURL := req.URL
	if req.APIEndpoint != "" {
		targetURL = req.APIEndpoint
	}

	// Apply per-request timeout on top of the parent context.
	timeout := s.defaultTimeout
	if req.Timeout > 0 {
		timeout = req.Timeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("User-Agent", s.ua.Next())

	start := time.Now()
	resp, err := s.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize))
	if err != nil {
		return nil, err
	}
	elapsed := time.Since(start)

	// resp.Request is set by net/http to the last request in the redirect
	// chain, giving us the final URL without a CheckRedirect closure.
	finalURL := resp.Request.URL.String()

	ct := resp.Header.Get("Content-Type")
	if before, _, found := strings.Cut(ct, ";"); found {
		ct = strings.TrimSpace(before)
	}

	return &pipeline.Response{
		OriginalRequest: req,
		StatusCode:      resp.StatusCode,
		Headers:         resp.Header,
		Body:            body,
		FinalURL:        finalURL,
		ContentType:     ct,
		Elapsed:         elapsed,
	}, nil
}
