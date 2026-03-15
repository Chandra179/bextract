# Tier 1 — Static Scraper

**Cost:** ~1 MB RAM · <100ms · $0.00  
**Technology:** Go `net/http` + Colly

---

## Responsibility

Issue the raw HTTP request and collect the response. **No parsing. No decision-making.** Tier 1's only job is to get bytes back as fast as possible and hand them to Tier 2.

---

## Process

1. Send a `GET` request with a realistic `User-Agent` header (desktop browser string, not a bot identifier)
2. Follow redirects, record the final URL
3. Read the response status, headers, and body
4. Pass the complete response object to Tier 2

Tier 1 does not inspect or interpret the response in any way. All decision logic lives in Tier 2.

---

## The Hidden API Shortcut

Before falling back to HTML scraping, inspect the site's Network tab for JSON API calls. If the site loads its data via a first-party JSON endpoint (common on React/Vue SPAs), call that endpoint directly in Tier 1. This bypasses all rendering and HTML parsing entirely, producing cleaner and more stable data.

**Signs that a hidden API endpoint exists:**

| Signal | Example |
|---|---|
| XHR/Fetch calls to internal paths | `/api/products/123`, `/v1/listings`, `/graphql` |
| JSON content-type on internal responses | `Content-Type: application/json` |
| Framework-specific query params | `?format=json`, `?_data=routes/product` (Remix) |

When a hidden API is identified, it should be registered in a per-domain config so future requests call it directly without going through HTML extraction at all.

---

## Implementation Details

| Parameter | Value | Notes |
|---|---|---|
| Default timeout | 15s | Configurable per-request via `Request.Timeout` or globally via `tier1.timeout_ms` |
| Max body size | 10 MB | Body is capped via `io.LimitReader`; larger responses are truncated |
| Max idle conns per host | 10 | HTTP transport setting for connection reuse |
| TLS handshake timeout | 5s | |
| Response header timeout | 10s | |
| User-Agent | Rotated from pool | Realistic desktop browser strings via `internal/tier1/useragent.go` |

The `Scraper` struct holds a shared `http.Client` and is safe for concurrent use across goroutines.

---

## Exit Conditions

Tier 1 always exits immediately after receiving the response. It does not retry, it does not parse, and it does not make any escalation decisions. All of that is Tier 2's job.

```
Tier 1 output → pipeline.Response:
  - StatusCode      int
  - Headers         http.Header
  - Body            []byte (capped at 10 MB)
  - FinalURL        string (after following all redirects)
  - ContentType     string (MIME type only, parameters stripped)
  - Elapsed         time.Duration
```

---

## Escalation

Tier 1 does not escalate. It unconditionally passes its output to Tier 2.

---