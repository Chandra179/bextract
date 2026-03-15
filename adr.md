# Architecture Decision Records

**Project:** bextract
**Last Updated:** March 2026

This document records the significant architectural decisions made in bextract, the reasoning behind them, the alternatives considered, and the known trade-offs.

---

## ADR-001: Cost-Escalation Ladder over Parallel Execution

**Decision:** Execute tiers sequentially, escalating to the next tier only on confirmed failure of the current one.

**Context:**
The pipeline must serve 10–30 RPS with costs ranging from $0.00 (plain HTTP) to $0.05+ (residential proxy + stealth browser). At 30 RPS, running all tiers in parallel for every request would be prohibitively expensive and resource-intensive.

**Alternatives considered:**

| Approach | Why rejected |
|---|---|
| Always use the highest tier | Cost is 50× higher per request; most pages are static |
| Parallel tier execution, take first winner | Wastes browser resources on pages that static scraping handles fine |
| Heuristic pre-classification (ML model to predict required tier) | Adds latency on every request; mispredictions are expensive in both directions |

**Trade-offs accepted:**
- Multi-tier escalation adds latency on pages that require a higher tier (~1–3s to discover Tier 2 cannot satisfy Tier 3's need). The cost saving is worth this penalty for the expected traffic distribution (majority of pages resolvable at Tier 1–2).
- The pipeline assumes that evidence of failure (hollow page, empty extraction, post-render 403) is more reliable than predictive classification.

---

## ADR-002: Single HTML Parse with Shared `goquery.Document`

**Decision:** Parse the response body into a `goquery.Document` exactly once in Tier 2 Stage 2, then pass a read-only reference to all 10 concurrent extractors.

**Context:**
Tier 2 runs up to 10 extractors in parallel goroutines. The naive approach would have each extractor independently parse the HTML it needs.

**Alternatives considered:**

| Approach | Why rejected |
|---|---|
| Each extractor parses independently | 10× parse cost per request; non-trivial at 30 RPS with 200KB+ pages |
| Pre-parse a partial DOM for each extractor type | Complex to maintain; goquery doesn't support partial parsing |

**Trade-offs accepted:**
- All extractors must be read-only against the shared document. Any extractor that mutates the DOM could corrupt results for concurrent extractors. This is enforced by convention, not by the type system.
- Unparseable HTML (rare, but possible for binary responses) triggers an immediate `Escalate` rather than a partial extraction attempt.

---

## ADR-003: Two-Phase Extractor Grouping (A+B), Not Pure Concurrency

**Decision:** Split the 10 extractors into Phase A (script-tag sources, priorities 1–4: JSON-LD, `__NEXT_DATA__`, state globals, inline variables) and Phase B (DOM sources, priorities 5–10: meta tags, microdata, data attributes, hidden inputs, CSS-hidden, DOM text). Skip Phase B on hollow pages when Phase A found fields.

**Context:**
JavaScript-shell pages (app shells with no visible text content) still carry full data payloads in `<script>` tags. Running DOM-heavy extractors (meta tags, microdata, data attributes, hidden inputs, CSS-hidden elements, DOM text) on these pages adds latency and goroutine overhead for no return.

**Alternatives considered:**

| Approach | Why rejected |
|---|---|
| Always run all 10 extractors | Wastes CPU on hollow pages where Phase B will find nothing |
| Run Phase B only when Phase A found nothing | Too conservative — some pages benefit from both phases |

**Trade-offs accepted:**
- Phase A and Phase B still run concurrently within each phase. The phase boundary adds a small synchronization overhead (wait for Phase A to complete before deciding whether to launch Phase B).
- The skip decision depends on **both** the hollow signal (computed in Stage 3, before extractors run) **and** Phase A yield (computed at runtime). Phase B is only skipped when the page is hollow AND Phase A returned at least one field. A hollow page where Phase A found nothing still runs Phase B as a last resort before escalating.

---

## ADR-004: Go-Rod for Tier 3 (no Node.js)

**Decision:** Use [go-rod](https://go-rod.github.io/) to control Chrome directly from Go via the Chrome DevTools Protocol, rather than Puppeteer or Playwright.

**Context:**
The application is written in Go. The two dominant browser automation libraries (Puppeteer, Playwright) require a Node.js runtime.

**Alternatives considered:**

| Approach | Why rejected |
|---|---|
| Puppeteer (Node.js) | +150 MB Node.js runtime; cross-process RPC between Go and Node; two runtimes to monitor |
| Playwright (Node.js) | Same Node.js overhead; Playwright is harder to embed in a non-JS server |
| Selenium (Java/Python) | Wrong runtime entirely; adds JVM or Python dependency |
| chromedp (Go) | Viable alternative; go-rod chosen for its pool API and resource interception primitives |

**Trade-offs accepted:**
- go-rod is a smaller community than Puppeteer/Playwright. Bug fixes and feature additions are slower.
- go-rod's Chrome pool API requires careful lifecycle management. A leaked page handle causes the pool to shrink permanently until restart.
- go-rod must be updated in sync with Chrome version changes. The Dockerfile pins Chromium from Debian bookworm which auto-updates on `apt upgrade`, creating a potential compatibility gap.

---

## ADR-005: Persistent Chrome Pool in Tier 3 (not per-request)

**Decision:** Maintain a pool of `N` warm Chrome instances (default N=2) across all requests, rather than launching a new Chrome process per render request.

**Context:**
Cold-starting a Chrome process takes 600–900ms. At 10–30 RPS with a 1–3s per-render SLA, per-request Chrome launches would dominate latency.

**Alternatives considered:**

| Approach | Why rejected |
|---|---|
| New Chrome per request | 600–900ms launch overhead is unacceptable at target RPS |
| Single Chrome instance, one page at a time | Concurrency bottleneck at N=1; a slow page blocks all others |
| Unlimited Chrome instances | Unbounded RAM; 200 MB per instance × 30 concurrent = 6 GB |

**Trade-offs accepted:**
- Pool size is a static configuration value, not auto-scaling. Tuning it incorrectly (too small) causes Tier 3 to queue, increasing tail latency. Tuning it too large wastes RAM.
- A crashed Chrome instance removes a slot from the pool until the pool detects the failure and replaces it. Recovery adds latency spikes.
- Persistent Chrome instances accumulate state (cookies, cache) across requests from different domains. This is intentional for same-domain session flows but requires cache clearing between unrelated domains if cookie pollution is a concern.

---

## ADR-006: Browserless Docker Container for Tier 4

**Status:** Implemented (`internal/tier4/renderer.go`)

**Decision:** Use a Dockerized [Browserless](https://www.browserless.io/) instance for Tier 4 rather than another local go-rod instance. Use per-request pages (no pooling) since the remote container handles lifecycle.

**Context:**
Tier 4 handles session-complex scenarios that Tier 3 cannot manage. At this point in the escalation, the risk of Chrome crashing or leaking memory is higher (more complex flows, more JS execution). A crash in a local Chrome instance can propagate to the Go process.

**Alternatives considered:**

| Approach | Why rejected |
|---|---|
| Another local go-rod pool | Same crash isolation problem as Tier 3; no container restart on failure |
| Puppeteer in a sidecar container | Requires Node.js runtime; Docker isolation benefit is the same |
| Direct CDP to remote Chrome (no Browserless) | Requires managing Chrome container lifecycle manually |

**Trade-offs accepted:**
- Adds a Docker dependency and a network hop (WebSocket to the container). The network hop latency is negligible on localhost.
- Browserless has a license cost at scale. The self-hosted community version is free but has rate limits.
- Container restart time (5–10s) means brief unavailability if the Browserless container crashes under load.
- No page pooling in Tier 4 (unlike Tier 3) — each request creates and closes a fresh page. This trades cold-start overhead for clean state isolation, which is acceptable given Tier 4's higher per-request latency budget (15s default vs 8s).

---

## ADR-007: Source Priority Beats Confidence in Field Merging

**Decision:** In Stage 5 merge, source priority is the primary sort key. Confidence is only a tiebreaker within the same source. A DOM-text result with 0.95 confidence always loses to a JSON-LD result with 0.60 confidence.

**Context:**
Each extractor reports a confidence score. The naive merge would pick the highest-confidence value for each field. However, extractors with high structural reliability (JSON-LD, `__NEXT_DATA__`) produce more accurate values even when their per-field confidence is lower, because their data comes from maintainer-structured formats rather than inferred from page layout.

**Alternatives considered:**

| Approach | Why rejected |
|---|---|
| Pure confidence ranking | DOM-text extractors with tight CSS selectors report high confidence but break on redesigns |
| Pure priority ranking (no confidence) | Within the same source, multiple results for the same field need a tiebreaker |
| Hybrid weighted score | Harder to reason about; confidence calibration across extractors would need empirical tuning |

**Trade-offs accepted:**
- Source priority encodes a subjective judgment about extractor reliability. A misconfigured JSON-LD tag (with wrong price) will silently beat a correctly-scraped DOM price. The conflict preservation feature mitigates this by keeping the runner-up in the output for inspection.
- Per-domain confidence overrides (configurable via `tier2.extractors`) allow adjusting the effective ordering when a specific extractor is known to be unreliable for a particular site.

---

## ADR-008: LLM Phase C as Opt-In Fallback

**Decision:** Add a Phase C LLM semantic fallback that fires only when all structured extractors return zero fields and the decision is `Escalate`. Disable it by default.

**Context:**
Some pages have no structured data, no useful meta tags, no data attributes, and no hidden inputs — but do contain readable text from which fields like `title`, `description`, `price`, and `author` can be semantically inferred. Without Phase C, these pages escalate to Tier 3 (browser render), which may also find nothing if the issue is sparse markup rather than JS-required rendering.

**Alternatives considered:**

| Approach | Why rejected |
|---|---|
| Always run LLM on every page | ~$0.0001–0.001 per request adds up at 30 RPS; structured data is almost always sufficient |
| Run LLM in parallel with other extractors | LLM result would rarely win due to lower priority (11); wasted cost |
| Use a local embedding model instead | Higher RAM cost; lower extraction quality for this use case |
| Always escalate to Tier 3 before LLM | Browser rendering cannot help when the problem is sparse markup, not missing JS execution |

**Trade-offs accepted:**
- LLM output is non-deterministic. The same page may produce different field values on repeated calls.
- The LLM is prompted to extract a fixed set of fields (`title`, `description`, `price`, `author`, `date`). Domain-specific fields outside this set are not extractable without prompt customization.
- Phase C adds up to 15s of latency (configurable `llm.timeout_ms`) in the rare worst case. This is acceptable because it only fires when all other options have failed and the alternative is escalation.
- Model dependency: the default `claude-haiku-4-5` is cost-optimized. Substituting a larger model improves extraction quality but increases cost and latency.

---

## ADR-009: Vendor All Dependencies

**Decision:** Commit the `vendor/` directory. All builds use `-mod=vendor` (enforced by Go's module toolchain when `vendor/` is present).

**Context:**
The pipeline runs in environments without reliable internet access (CI, air-gapped deployment). go-rod downloads a Chromium binary during build if it cannot find one — this is disabled by setting `ROD_BROWSER_BIN` explicitly, but module downloads at build time are a separate problem.

**Alternatives considered:**

| Approach | Why rejected |
|---|---|
| Module proxy / GOPROXY cache | Adds infrastructure dependency; less reproducible across environments |
| Docker layer cache | Module changes invalidate cache; slow on first build in new environment |
| No vendoring, rely on go.sum | Requires network access at build time |

**Trade-offs accepted:**
- `vendor/` adds 10–50 MB to the repository size and must be kept in sync with `go.mod` via `go mod vendor` after any dependency change.
- Pull requests that add dependencies require two commits (one for `go.mod`/`go.sum`, one for `vendor/`). The CLAUDE.md instructions enforce this workflow.

---

## ADR-010: ArangoDB as the Persistence Layer

**Decision:** Use ArangoDB (multi-model graph + document database) to store tier results.

**Context:**
Scraped data is inherently graph-like: pages link to other pages, products belong to categories, articles have authors and related articles. A document store that also supports graph queries allows modeling these relationships without a schema migration for each new relationship type.

**Alternatives considered:**

| Approach | Why rejected |
|---|---|
| PostgreSQL + JSONB | Strong choice for structured data; less natural for graph traversal across scraped entities |
| MongoDB | Document store without native graph support; poor fit for link-graph analysis |
| Neo4j | Pure graph database; document queries are awkward; higher operational overhead |
| SQLite | Not suitable for 10–30 RPS multi-writer workload |

**Trade-offs accepted:**
- ArangoDB is less widely operated than PostgreSQL. Fewer engineers will be familiar with AQL (ArangoDB Query Language).
- ArangoDB's Go driver is community-maintained and less mature than the PostgreSQL ecosystem.
- Multi-model flexibility can lead to inconsistent data modeling if not governed. Document collections and graph edge collections can diverge without discipline.
