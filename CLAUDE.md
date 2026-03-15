# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
make up           # build Docker image and run docker compose
make run          # run detach
make test         # run all tests
make swag         # regenerate docs/ from Swagger annotations (run after changing annotations)
make tidy         # go mod tidy

go test ./internal/tier1/... -run TestFetchBasicSuccess   # run a single test
go build ./...    # verify everything compiles
go mod vendor     # sync vendor/ after go.mod changes
```

After adding new dependencies always run `go mod vendor` — the repo vendors all deps.

## Architecture

bextract is a **5-tier cost-escalation web scraping pipeline**. Each tier attempts data extraction; if it can't satisfy the request it returns one of four decisions: `Done`, `Escalate`, `Abort`, or `Backoff` (defined in `internal/pipeline/types.go`). The tiers are ordered by cost: Tier 1 is a plain HTTP fetch (~free), Tier 5 is a stealth residential-proxy browser (~$0.05/req).

**Tiers 1, 2, and 3 are implemented.** Tiers 4 and 5 are documented in `architecture.md`.

### Request flows

```
POST /api/v1/tier1/fetch
  → internal/api/tier1/handler.go     (HTTP binding, response shaping)
  → internal/tier1/scraper.go         (HTTP client, UA rotation, body cap)
  → pipeline.Response

POST /api/v1/tier2/analyze
  → internal/api/tier2/handler.go     (HTTP binding, response shaping)
  → internal/tier1/scraper.go         (fetch raw HTML)
  → internal/tier2/analyzer.go        (5-stage extraction pipeline)
      Stage 1: stage1_headers.go      (status codes, tech fingerprints)
      Stage 2: goquery parse once     (shared document passed to all extractors)
      Stage 3: stage3_hollow.go       (JS-shell detection via penalty weights)
      Stage 4: 10 concurrent extractors (see below)
      Stage 5: stage5_merge.go        (priority-ordered field merge + decision)
  → pipeline.AnalysisResult

POST /api/v1/tier3/render
  → internal/api/tier3/handler.go     (HTTP binding, response shaping)
  → internal/tier1/scraper.go         (fetch raw HTML)
  → internal/tier2/analyzer.go        (if Tier 2 returns Done/Abort/Backoff → return early)
  → internal/tier3/renderer.go        (if Tier 2 returns Escalate → launch Chrome)
      • pooled Chrome pages via go-rod
      • blocks: stylesheets, images, media, fonts, analytics domains
      • waits for network idle (500ms window, 8s hard cap)
      • detects escalation signals (cookie walls, login walls, Cloudflare)
      • re-runs Tier 2 extraction on rendered HTML
  → pipeline.RenderResult
```

### Tier 2 extractors (Stage 4, run concurrently)

| Priority | File | Source |
|---|---|---|
| 1 | extract_jsonld.go | `<script type="application/ld+json">` |
| 2 | extract_nextdata.go | `<script id="__NEXT_DATA__">` |
| 3 | extract_globals.go | `window.__REDUX_STATE__` etc. |
| 4 | extract_inlinevar.go | `var x = {...}` in inline `<script>` |
| 5 | extract_meta.go | `<head>` `og:*`, `twitter:*`, `<title>` |
| 6 | extract_microdata.go | `itemprop`, `itemscope`, `property` attributes |
| 7 | extract_dataattr.go | `data-*` on product/listing containers |
| 8 | extract_hiddeninput.go | `<input type="hidden">` |
| 9 | extract_csshidden.go | `display:none` / `visibility:hidden` elements |
| 10 | extract_domtext.go | CSS selector → visible text (caller-supplied selectors) |

### Key structural decisions

- **`internal/api/router/router.go`** is the single place to register all routes. Import it from `cmd/server/main.go` only.
- **Handler files** own their request/response types and Swagger model annotations (`internal/api/tier{1,2,3}/handler.go`).
- **Swagger annotations** live in two places: API-level (`@title`, `@host`, `@BasePath`) in `cmd/server/main.go`; endpoint/model annotations in handler files. After any annotation change, run `make swag` to regenerate `docs/`.
- **`internal/tier1/scraper.go`** is safe for concurrent use. `tier1.New(0)` uses the 15s default timeout.
- **`internal/tier2/analyzer.go`** is safe for concurrent use. `tier2.New(0)` uses the 5s extraction timeout.
- **`internal/tier3/renderer.go`** holds a persistent pooled Chrome instance. `tier3.New(0, 0, 0)` uses defaults (pool=2, render=8s). Returns `errNoBrowser` if `ROD_BROWSER_BIN` or a system Chrome binary is not found.
- The vendor directory is committed. Do not use `-mod=mod` in commands.

## Project structure

```
bextract/
├── cmd/server/main.go              # entry point, Swagger API annotations
├── internal/
│   ├── pipeline/types.go           # shared types: Request, Response, Decision, AnalysisResult, RenderResult
│   ├── api/
│   │   ├── router/router.go        # all route registration
│   │   ├── tier1/handler.go        # POST /api/v1/tier1/fetch
│   │   ├── tier2/handler.go        # POST /api/v1/tier2/analyze
│   │   └── tier3/handler.go        # POST /api/v1/tier3/render
│   ├── tier1/
│   │   ├── scraper.go              # HTTP client, UA rotation, 10 MB body cap
│   │   └── useragent.go            # User-Agent pool
│   ├── tier2/
│   │   ├── analyzer.go             # orchestrates 5-stage pipeline
│   │   ├── extractor.go            # Extractor interface
│   │   ├── stage1_headers.go       # status code + tech fingerprint detection
│   │   ├── stage3_hollow.go        # hollow page detection (penalty weights)
│   │   ├── stage5_merge.go         # priority merge + Done/Escalate/Abort/Backoff decision
│   │   └── extract_*.go            # 10 extractor implementations
│   └── tier3/
│       ├── renderer.go             # go-rod Chrome pool, request blocking, Tier 2 re-run
│       └── errors.go               # errNoBrowser sentinel
├── docs/                           # generated Swagger (do not edit manually)
├── Dockerfile                      # multi-stage: golang:1.26-bookworm builder + debian:bookworm-slim runtime with chromium
├── docker-compose.yml              # single-service compose; sets ROD_BROWSER_BIN, seccomp:unconfined
├── Makefile
└── architecture.md                 # full pipeline design spec for all 5 tiers
```

## Docker notes

- The runtime image installs `chromium` from Debian bookworm repos (at `/usr/bin/chromium`).
- `ROD_BROWSER_BIN=/usr/bin/chromium` is set in both the Dockerfile and docker-compose.yml so go-rod's `launcher.LookPath()` finds the binary without attempting an auto-download.
- `seccomp:unconfined` is required for Chrome's sandboxing syscalls inside Docker; `NoSandbox(true)` is set programmatically in the renderer.

## Logging

The project uses `pkg/logger` for structured logging
