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

1. Fetch raw HTML via `internal/tier1/scraper.go`
2. Run Tier 2 analysis — if decision is `Done`/`Abort`/`Backoff`, return early (no browser needed)
3. If `Escalate`: acquire a warm Chrome page from the pool
4. Navigate with resource interception active (block CSS, images, fonts, analytics)
5. Wait for target selector → network idle (500ms) → hard 8s cap
6. Re-run Tier 2 extraction on the rendered HTML
7. Detect escalation signals (cookie wall, login redirect, headless fingerprint detection)

---

## Dependencies

| Dependency | Role |
|---|---|
| `go-rod` | Chrome DevTools Protocol client (Go-native, no Node.js) |
| `internal/tier1/scraper.go` | Initial HTML fetch |
| `internal/tier2/analyzer.go` | Pre-render gate + post-render extraction |
| `ROD_BROWSER_BIN` env var | Path to Chromium binary for go-rod's launcher |

---

## Responsibility

Execute JavaScript and return the fully rendered DOM. Used **only** when Tier 2 has confirmed that content cannot be obtained statically.

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

Do not wait for the full page `load` event. Use the following strategies in order of preference:

1. **Wait for target element** — wait for the specific CSS selector that contains the required data to appear in the DOM
2. **Wait for network idle** — no pending requests for 500ms, with a hard **8-second cap**
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

## Escalation Criteria to Tier 4

Tier 3 escalates when it encounters session complexity that Go-Rod cannot handle cleanly:

| Scenario | Indicator |
|---|---|
| Cookie consent wall | Overlay present; DOM interaction blocked before content loads |
| Multi-step authentication | Redirect to `/login` or auth form detected in rendered DOM |
| Canvas / WebGL fingerprinting | Request blocked despite `--disable-gpu` flag |
| Headless detection | Site detects Go-Rod's Chrome via `navigator.webdriver` or similar |

---