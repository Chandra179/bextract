# Tier 3 — Lightweight Browser Render

**Cost:** ~200 MB RAM · 1–3s · $0.00
**Technology:** Go-Rod (native Go, no Node.js)

---

## Goal

Execute JavaScript and return a fully rendered DOM when static extraction (Tier 2) has confirmed the page requires it.

---

## Input / Output

**Input:** `pipeline.Request` — URL + optional CSS selectors
**Output:** `pipeline.RenderResult` — extracted fields + decision (`Done`, `Escalate`, `Abort`, `Backoff`)

---

## Process

The renderer is invoked by the cascade runner only after Tier 2 returns `Escalate`. Tier 1 fetch and Tier 2 pre-analysis are handled by the runner before calling `Render()`.

1. Acquire a warm Chrome page from the pool
2. Set up request interception to block heavyweight resources (CSS, images, media, fonts) and configured analytics/tracking domains
3. Navigate to the target URL with a render timeout context (default 8s hard cap)
4. Wait for network idle (no pending requests for 500ms), bounded by the render timeout
5. Detect escalation signals (cookie consent walls, login redirects, Cloudflare challenges)
6. Extract the fully rendered HTML from the page
7. Build a synthetic Tier 1 response and re-run Tier 2 extraction on the rendered HTML
8. Return the page to the pool (navigated to `about:blank` for cleanup)

---

## Dependencies

| Dependency | Role |
|---|---|
| `go-rod` | Chrome DevTools Protocol client (Go-native, no Node.js) |
| `internal/tier2/analyzer.go` | Post-render extraction (re-runs full Tier 2 pipeline on rendered HTML) |
| `ROD_BROWSER_BIN` env var | Path to Chromium binary for go-rod's launcher (not needed when `browserless_url` is configured) |

---

## Responsibility

Execute JavaScript and return the fully rendered DOM. Used **only** when the cascade runner determines that Tier 2 returned `Escalate`, confirming content cannot be obtained statically.

---

## Why Go-Rod

Go-Rod controls Chrome directly from Go via the Chrome DevTools Protocol. The alternative — Node.js-based Puppeteer — requires a 150 MB Node.js runtime that adds no value for a Go application. Go-Rod eliminates that overhead entirely.

---

## Browser Launch Flags

```
--disable-gpu             No GPU required in a headless VPS environment
--disable-images          Images are never needed for text data extraction
--no-sandbox              Required in Docker environments
--disable-dev-shm-usage   Prevents crashes under memory pressure
```

---

## Resource Interception

All requests are intercepted at the network layer before they leave the browser. This reduces bandwidth, latency, and idle wait time significantly.

| Resource Type | Block? | Reason |
|---|---|---|
| CSS stylesheets | **Yes** | Not needed for data extraction |
| Images / video | **Yes** | Not needed |
| Analytics scripts | **Yes** | Reduces idle wait time significantly |
| Tracking pixels | **Yes** | Unnecessary network requests |
| Font files | **Yes** | Not needed |
| First-party JS | **No** | Required to render the page |
| First-party XHR/Fetch | **No** | May contain the data being extracted |

---

## Wait Policy

Do not wait for the full page `load` event. The implementation uses:

1. **Wait for network idle** — no pending requests for 500ms window, bounded by the render timeout (default **8-second** hard cap)
2. **Timeout is not fatal** — if the render timeout fires before network idle, extraction proceeds with whatever has loaded
3. **Never use `time.Sleep`** — unconditional sleeps are fragile and waste time on fast pages

---

## Instance Pool

Maintain a pool of **persistent browser instances** rather than launching a new browser per request.

| Approach | Cold-start time | Notes |
|---|---|---|
| New Chrome per request | 600–900ms | Unacceptable at 10–30 RPS |
| Pooled warm instances | <100ms | Each request gets a pre-warmed page |

The pool size should be tuned to the concurrency limit set in the cross-cutting rate limiter. A domain concurrency cap of 2 means you need at most 2 warm instances dedicated to that domain at any time.

---

## Escalation Signals

The renderer checks for escalation signals after the page loads, before extracting HTML. If any signal is detected, the result is `Escalate` with the reason string — the cascade runner then falls through to Tier 4 if available.

| Signal | Detection Method |
|---|---|
| Cookie consent wall | Presence of known overlay selectors: `#onetrust-banner-sdk`, `.fc-dialog-container`, `#CybotCookiebotDialog`, `.cmp-popup` |
| Login wall (URL) | Final URL contains `/login`, `/signin`, or `/sign-in` |
| Login wall (form) | `input[type="password"]` present in rendered DOM |
| Cloudflare challenge | `#cf-challenge-running` or `.cf-error-type-1010` present in rendered DOM |

If Tier 2 re-analysis on the rendered HTML also returns `Escalate` (e.g. the rendered page is still hollow with no extractable fields), that decision propagates to the runner as well.

---