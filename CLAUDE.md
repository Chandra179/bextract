# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
make run          # start the server on :8080
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

**Only Tier 1 is currently implemented.** The remaining tiers are documented in `architecture.md`.

### Request flow

```
POST /api/v1/tier1/fetch
  → internal/api/tier1/handler.go   (HTTP binding, response shaping)
  → internal/tier1/scraper.go       (HTTP client, UA rotation, body cap)
  → pipeline.Response               (shared response type)
```

### Key structural decisions

- **`internal/api/router/router.go`** is the single place to register all routes. Import it from `cmd/server/main.go` only.
- **`internal/api/tier1/handler.go`** owns the `FetchRequest` / `FetchResponse` / `ErrorResponse` types. These are also the Swagger model types — keep Swagger annotations on them.
- **Swagger annotations** live in two places: API-level (`@title`, `@host`, `@BasePath`) in `cmd/server/main.go`; endpoint/model annotations in handler files. After any annotation change, run `make swag` to regenerate `docs/`.
- **`internal/tier1/scraper.go`** is safe for concurrent use. `tier1.New(0)` uses the 15 s default timeout; pass a non-zero `time.Duration` to override.
- The vendor directory is committed. Do not use `-mod=mod` in commands.
