# Tier 4 — Managed Browser Container

**Cost:** ~512 MB RAM · 2–5s · $0.01/request
**Technology:** Browserless running in Docker

---

## Goal

Handle session-complex scenarios Go-Rod (Tier 3) cannot manage cleanly, with Chrome isolated from the Go process to prevent crashes or memory leaks from affecting the scraper.

---

## Input / Output

**Input:** `pipeline.Request` — URL + optional CSS selectors (same shape as Tier 3)
**Output:** `pipeline.RenderResult` — extracted fields + decision (`Done`, `Escalate`, `Abort`, `Backoff`)

---

## Process

1. Connect to a Browserless Docker container via WebSocket (CDP)
2. Execute the same render/extraction flow as Tier 3 — same CDP interface, Chrome is remote
3. Run Tier 2 extraction on the rendered HTML
4. Detect post-render 403 or CAPTCHA challenge page → escalate to Tier 5

---

## Dependencies

| Dependency | Role |
|---|---|
| Browserless Docker image | Containerized Chrome with hard memory ceiling and process isolation |
| CDP / WebSocket | Same protocol as go-rod, just targeting a remote process |
| `internal/tier2/analyzer.go` | Post-render extraction (identical to Tier 3) |

---

## Comparison with Tier 3

| | Tier 3 | Tier 4 |
|---|---|---|
| Chrome location | Local, pooled | Remote Docker container |
| Failure mode handled | Session complexity | Active bot detection |
| Crash isolation | None (kills Go process) | Full (container restarts independently) |
| Cost | ~$0.00 | ~$0.01/req |

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

## Escalation Criteria from Tier 3

| Scenario | Indicator |
|---|---|
| Cookie consent wall | Overlay blocks DOM interaction before content is visible |
| Multi-step login | Redirect to `/login` or presence of auth form |
| WebGL / Canvas fingerprint block | HTTP 403 returned after page renders |
| Complex session state | Session cookie required that wasn't established in Tier 3 |

---

## Escalation Criteria to Tier 5

Tier 4 escalates when Browserless returns a **confirmed 403 Forbidden** or a **CAPTCHA challenge page**. These indicate active bot detection that requires a stealth-patched browser and residential IP.

A 403 received at Tier 4 is treated differently from a 403 received at Tier 1:

| Source of 403 | Meaning | Correct response |
|---|---|---|
| Tier 1 (static HTTP) | Authentication or block on the raw request | Abort — no rendering will fix this |
| Tier 4 (post-render) | Bot detection triggered after full JS execution | Escalate to Tier 5 |

---