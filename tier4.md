# Tier 4 — Managed Browser Container

**Cost:** ~512 MB RAM · 2–5s · $0.01/request
**Technology:** Browserless running in Docker · go-rod CDP client
**Status:** Implemented (`internal/tier4/renderer.go`)

---

## Goal

Handle session-complex scenarios Go-Rod (Tier 3) cannot manage cleanly, with Chrome isolated from the Go process to prevent crashes or memory leaks from affecting the scraper.

---

## Input / Output

**Input:** `pipeline.Request` — URL + optional timeout override
**Output:** `pipeline.RenderResult` — extracted fields + decision (`Done`, `Escalate`, `Abort`, `Backoff`)

---

## Process

The renderer is invoked by the cascade runner only after Tier 3 returns `Escalate`.

1. Create a new page on the remote Browserless Chrome instance via CDP
2. Set up request interception — block stylesheets, images, media, fonts, and configured analytics/tracking domains
3. Navigate to the target URL with a render timeout context (default 15s hard cap)
4. Wait for network idle (no pending requests for 500ms), bounded by the render timeout
5. Detect escalation signals (CAPTCHA widgets, login walls)
6. Extract the fully rendered HTML
7. Build a synthetic Tier 1 response and re-run Tier 2 extraction on the rendered HTML
8. Close the page (no pooling — each request gets a fresh page)

---

## Dependencies

| Dependency | Role |
|---|---|
| Browserless Docker image | Containerized Chrome with hard memory ceiling and process isolation |
| `go-rod` + CDP / WebSocket | Same library as Tier 3, targeting a remote Chrome process |
| `internal/tier2/analyzer.go` | Post-render extraction (identical pipeline to Tier 3) |

---

## Key Differences from Tier 3

| | Tier 3 | Tier 4 |
|---|---|---|
| Chrome location | Local, pooled | Remote Docker container |
| Page lifecycle | Pooled — pages returned to pool after use | Per-request — fresh page created and closed |
| Default render timeout | 8s | 15s (allows more complex session flows) |
| Crash isolation | None (kills Go process) | Full (container restarts independently) |
| Cost | ~$0.00 | ~$0.01/req |
| Configuration | `tier3.browserless_url` or local Chrome | `tier4.browserless_url` (required) |

---

## Responsibility

Handle complex session scenarios that Go-Rod (Tier 3) cannot manage cleanly. Isolate the heavy Chrome process from the main Go application so that a crash or memory leak in Chrome cannot affect the scraper process.

---

## Why Docker Isolation

At 10–30 RPS, a memory leak or crash in a Chrome instance must not bring down the Go application.

Docker provides:
- **Hard memory ceiling** — Chrome cannot consume unbounded RAM
- **Process isolation** — a crashing container is restarted without affecting the scraper
- **Clean state** — each container restart gives a fresh Chrome with no accumulated state

The Go application connects to Browserless via WebSocket using the standard Chrome DevTools Protocol (CDP). From Go's perspective, this is the same interface as Go-Rod — just the Chrome process is remote and containerized rather than local.

---

## Escalation Signals

Tier 4 checks for escalation signals after the page loads, before extracting HTML:

| Signal | Detection Method |
|---|---|
| CAPTCHA (reCAPTCHA) | `[class*=recaptcha]` present in rendered DOM |
| CAPTCHA (hCaptcha) | `[class*=hcaptcha]` present in rendered DOM |
| CAPTCHA (generic) | `[data-sitekey]` attribute present |
| Login wall (URL) | Final URL contains `/login`, `/signin`, or `/sign-in` |

If an escalation signal is detected, the result is `Escalate` with a reason string. Since Tier 5 is not yet implemented, the cascade runner returns the Tier 4 result as-is.

---

## Escalation to Tier 5

Tier 4 escalates when it encounters active bot detection that requires a stealth-patched browser and residential IP. These signals indicate the page is intentionally blocking automated access.

A 403 received at Tier 4 is treated differently from a 403 received at Tier 1:

| Source of 403 | Meaning | Correct response |
|---|---|---|
| Tier 1 (static HTTP) | Authentication or block on the raw request | Abort — no rendering will fix this |
| Tier 4 (post-render) | Bot detection triggered after full JS execution | Escalate to Tier 5 |

---

## Configuration

```yaml
tier4:
  browserless_url: "ws://localhost:3000"  # required — no fallback to local Chrome
  render_timeout_ms: 15000                # default 15s
  block_domains:                          # analytics/tracking domains to block
    - "*google-analytics.com*"
    - "*googletagmanager.com*"
    - "*doubleclick.net*"
    - "*facebook.com/tr*"
    - "*hotjar.com*"
```

If `browserless_url` is not configured, `tier4.New()` returns `errNoBrowserless` and the cascade runner skips Tier 4.

---
