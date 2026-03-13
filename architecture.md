# Data Extraction Pipeline — Architecture Documentation

**Version:** 1.0  
**Target Throughput:** 10–30 RPS  
**Runtime:** Go  
**Last Updated:** March 2026

---

## Table of Contents

1. [Overview](#1-overview)
2. [Pipeline Philosophy](#2-pipeline-philosophy)
3. [System Architecture Diagram](#3-system-architecture-diagram)
4. [Tier 1 — Static Scraper](#4-tier-1--static-scraper)
5. [Tier 2 — Content Detection & Extraction](#5-tier-2--content-detection--extraction)
6. [Tier 3 — Lightweight Browser Render](#6-tier-3--lightweight-browser-render)
7. [Tier 4 — Managed Browser](#7-tier-4--managed-browser)
8. [Tier 5 — Stealth Protocol](#8-tier-5--stealth-protocol)
9. [Cross-Cutting Concerns](#9-cross-cutting-concerns)
10. [Decision Reference](#10-decision-reference)

---

## 1. Overview

This pipeline extracts structured data from web pages at 10–30 requests per second. It operates as a **cost-escalation ladder**: each tier is more capable but also more expensive in RAM, latency, and money. A request only advances to the next tier when the current one definitively cannot fulfill it.

The guiding principle is that most data extraction happens unnecessarily late in the stack. By exhaustively checking every static data location before launching a browser, the majority of pages resolve at Tier 1 or Tier 2 — at a fraction of the cost of a headless Chrome instance.

---

## 2. Pipeline Philosophy

### Escalate by evidence, not by assumption

A tier advances a request only when it has confirmed evidence that the current tier cannot fulfill the extraction. Guessing or defaulting to a heavier tier is explicitly disallowed.

### Four possible outcomes at every tier

| Decision | Meaning | Next Step |
|---|---|---|
| **Done** | Data found, quality sufficient | Return to caller |
| **Escalate** | This tier cannot get the data | Move to next tier |
| **Abort** | Resource is unavailable (404, 401) | Stop entirely, do not escalate |
| **Backoff** | Rate limited or server error | Retry same tier after delay |

The `Abort` and `Backoff` decisions are critical. Without them, a pipeline wastes resources launching a headless browser for a page that simply doesn't exist or that has rate-limited the caller.

### Cost hierarchy

```
Tier 1   ~1 MB RAM    <100ms    $0.00
Tier 2   ~1 MB RAM    <150ms    $0.00   (runs within Tier 1 response)
Tier 3   ~200 MB RAM  1–3s      $0.00   (Go-Rod, local)
Tier 4   ~512 MB RAM  2–5s      $0.01   (Dockerized Browserless)
Tier 5   ~512 MB RAM  3–8s      $0.05+  (Residential proxies)
```

---

## 3. System Architecture Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│                        REQUEST ENTRY                            │
│                    URL + Target Fields                          │
└──────────────────────────────┬──────────────────────────────────┘
                               │
                               ▼
┌─────────────────────────────────────────────────────────────────┐
│  TIER 1 — Static HTTP                                           │
│  Go net/http + Colly                                            │
│                                                                 │
│  • Send GET with quality User-Agent                             │
│  • Check Network Tab for hidden JSON APIs → hit directly        │
│  • Pass raw response to Tier 2 for analysis                     │
└──────────────────────────────┬──────────────────────────────────┘
                               │
                               ▼
┌─────────────────────────────────────────────────────────────────┐
│  TIER 2 — Content Detection & Multi-Extractor Pipeline          │
│  (See full breakdown in Section 5)                              │
│                                                                 │
│  Done ──────────────────────────────────────────► Return Data   │
│  Abort ─────────────────────────────────────────► Stop          │
│  Backoff ───────────────────────────────────────► Retry Tier 1  │
│  Escalate ──────────────────────────────────────► Tier 3 ▼      │
└──────────────────────────────┬──────────────────────────────────┘
                               │ Escalate
                               ▼
┌─────────────────────────────────────────────────────────────────┐
│  TIER 3 — Lightweight Browser Render                            │
│  Go-Rod (native Go, no Node.js)                                 │
│                                                                 │
│  • Pooled Chrome instances (no cold-start per request)          │
│  • Block: images, CSS, tracking, analytics                      │
│  • Wait for target element OR network idle (8s cap)             │
└──────────────────────────────┬──────────────────────────────────┘
                               │ Escalate (session complexity)
                               ▼
┌─────────────────────────────────────────────────────────────────┐
│  TIER 4 — Managed Browser Container                             │
│  Browserless (Docker)                                           │
│                                                                 │
│  • Handles: consent flows, multi-step auth, canvas fingerprint  │
│  • Go app stays lean; heavy lifting isolated in container       │
│  • Strict memory limits on Docker container                     │
└──────────────────────────────┬──────────────────────────────────┘
                               │ Escalate (403 / CAPTCHA)
                               ▼
┌─────────────────────────────────────────────────────────────────┐
│  TIER 5 — Stealth Protocol                                      │
│  Residential Proxies + Patched Chromium                         │
│                                                                 │
│  • Triggered only by confirmed 403 or CAPTCHA in Tiers 1–4     │
│  • Stealth patches hide headless fingerprint from Cloudflare    │
│  • Sticky sessions required for multi-page flows                │
└──────────────────────────────┬──────────────────────────────────┘
                               │
                               ▼
                         Return Data / Fail
```

---

## 4. Tier 1 — Static Scraper

### Responsibility

Issue the raw HTTP request and collect the response. No parsing, no decision-making. Tier 1's only job is to get bytes back as fast as possible.

### Technology

Go standard `net/http` or Colly for crawl management.

### Process

1. Send a `GET` request with a realistic `User-Agent` header (desktop browser string, not a bot identifier)
2. Follow redirects, record the final URL
3. Read the response status, headers, and body
4. Pass the complete response object to Tier 2

### The Hidden API Shortcut

Before scraping HTML, inspect the site's Network tab for JSON API calls. If the site loads its data via a first-party JSON endpoint (common on React/Vue SPAs), call that endpoint directly in Tier 1. This bypasses all rendering entirely and produces cleaner, more stable data than any HTML parsing strategy.

Signs an API endpoint exists:
- XHR/Fetch calls to `/api/`, `/v1/`, `/graphql` in the browser's Network tab
- `content-type: application/json` on responses to internal URLs
- Query parameters like `?format=json` or `?_data=...` (Remix framework pattern)

### Exit Conditions

Tier 1 exits immediately in all cases. It does not make decisions — it hands everything to Tier 2.

---

## 5. Tier 2 — Content Detection & Extraction

### Responsibility

Tier 2 is the intelligence layer of the pipeline. Given a raw HTTP response from Tier 1, it must:

1. Determine if the page requires JavaScript to render (hollow detection)
2. Exhaustively search every static data location before escalating
3. Emit a single decision: Done, Abort, Backoff, or Escalate

### Architecture

Tier 2 is composed of five sequential stages. The first two are synchronous and cheap; the third gates whether to run the extractors; the fourth fans out to all extractors in parallel; the fifth merges and decides.

```
Stage 1: Header Analysis         (sync, pre-parse)
Stage 2: HTML Parse              (single parse, shared document)
Stage 3: Hollow Page Detection   (sync, uses shared document)
Stage 4: Concurrent Extraction   (parallel goroutines)
Stage 5: Merge & Decide          (priority-ordered merge)
```

---

### Stage 1 — Header Analysis

Headers are analyzed before the HTML body is touched. This is the cheapest possible gate.

**Status code hard decisions**

| Status | Decision | Reason |
|---|---|---|
| 404 | Abort | Resource does not exist — no tier can fix this |
| 401 / 403 | Abort | Authentication required — launching a browser won't help |
| 429 | Backoff | Rate limited — respect `Retry-After` header |
| 503 + Retry-After | Backoff | Temporary server error |
| 200 + tiny body | Continue | May be hollow shell — check in Stage 3 |

**Technology fingerprinting from headers**

Headers reveal what framework a site uses, allowing the pipeline to prioritize the most likely extractor before running them all.

| Header | Value | Implication |
|---|---|---|
| `x-powered-by` | `Next.js` | `__NEXT_DATA__` extractor will almost certainly succeed |
| `cf-ray` present | — | Cloudflare is in front; watch for challenge pages |
| `cf-mitigated` | `challenge` | Active Cloudflare challenge — escalate to Tier 5 |
| `content-type` | `application/json` | Response is already JSON — Tier 1 hidden API was missed |
| `x-powered-by` | `PHP` | Traditional server-side render — DOM text extraction likely works |

---

### Stage 2 — HTML Parse

The raw response body is parsed into a single document object (goquery) exactly once. This shared document is passed as a read-only reference to every extractor in Stage 4. No extractor re-parses the HTML independently.

This design decision has two benefits:
- Eliminates redundant parsing work (parsing HTML is not free at 30 RPS)
- Guarantees all extractors operate on an identical document state

---

### Stage 3 — Hollow Page Detection

Before running extractors, the pipeline determines whether the page is a JavaScript shell. This does not immediately trigger escalation — even hollow pages can contain data in script tags — but it is recorded as a signal that affects the merge decision in Stage 5.

**Hollow signals and their penalty weights**

| Signal | Condition | Penalty |
|---|---|---|
| Empty app shell | `<div id="app"></div>` with no children | 0.85 |
| No-script message | "enable JavaScript" / "JavaScript required" text | 0.90 |
| Tiny body | Body < 5 KB | 0.50 |
| Low text density | Text content < 5% of total HTML size | 0.70 |
| CAPTCHA present | reCAPTCHA / hCaptcha / Turnstile widget in body | 0.95 |
| Cloudflare challenge | `cf-mitigated: challenge` in headers | 1.00 |

A page is classified as hollow when total penalty ≥ 0.70. A hollow page with no successful extractions triggers immediate escalation to Tier 3.

Note: Body size alone is not a reliable hollow signal. A 3 KB page can be a valid small article; a 200 KB page can be a React shell with all content deferred. The text-density ratio is the more reliable metric.

---

### Stage 4 — Concurrent Extraction

All extractors run simultaneously in separate goroutines. They all receive the same shared document and emit independent results. Total extraction time is bounded by the slowest single extractor, not the sum of all extractors.

A hard timeout (default 5 seconds) cancels all goroutines if extraction takes too long.

The extractors are organized across six data categories:

---

#### Category A — Script Tags

Script tags are the highest-value extraction target on modern websites. JavaScript frameworks serialize their complete server-side data payload into inline script tags so the page can hydrate without an additional API call. This data is structured, typed, and maintained by the site's engineering team.

**A1 — JSON-LD** `Priority 1`

Location: `<script type="application/ld+json">`

JSON-LD is a Google-mandated structured data format used for rich search results. It contains clean, schema-validated, semantically typed fields. Because it's maintained for Google's crawler, it tends to be the most stable data on the page — CSS classes change with redesigns; JSON-LD schema fields do not.

Commonly found on: e-commerce product pages, news articles, recipes, job postings, local business listings, events.

Fields available: name, description, sku, price, currency, availability, rating, review count, author, date published, address.

**A2 — Next.js `__NEXT_DATA__`** `Priority 2`

Location: `<script id="__NEXT_DATA__" type="application/json">`

Next.js serializes the complete `getServerSideProps` or `getStaticProps` payload into this tag. It contains every field that was fetched server-side before the page rendered — often the entire product, article, or listing object. This is a complete server payload, not a fragment.

Detection: Check for `x-powered-by: Next.js` header, or scan for `script#__NEXT_DATA__` selector.

**A3 — Framework State Globals** `Priority 3`

Location: Inline `<script>` tags (no `src` attribute)

Frameworks that predate or don't use Next.js-style data serialization still hydrate the client by assigning state to `window` globals before the app boots.

Common patterns to scan for: `window.__NUXT__`, `window.__REDUX_STATE__`, `window.__INITIAL_STATE__`, `window.__APP_STATE__`, `window.__PRELOADED_STATE__`, `window.__SERVER_DATA__`, `window.__STORE__`.

**A4 — Inline Variable Assignments** `Priority 4`

Location: Inline `<script>` tags

Non-framework sites inject data as plain JavaScript variable assignments. These are less structured than framework state objects and require regex extraction rather than direct JSON parsing, since they are JavaScript syntax, not JSON.

Common patterns: `var productData = {...}`, `const CONFIG = {...}`, `let PAGE_DATA = {...}`.

---

#### Category B — HTML Attributes

**B1 — Meta Tags** `Priority 5`

Location: `<head>` section

Meta tags are maintained for social sharing (Open Graph, Twitter Cards) and SEO. They are a reliable source of title, description, canonical URL, and preview image — fields that are often the highest-quality representation of the page's primary content.

Sources within meta tags:
- Open Graph: `<meta property="og:title" content="...">`, `og:description`, `og:image`, `og:url`, `og:type`
- Twitter Cards: `<meta name="twitter:title" content="...">`
- Standard: `<meta name="description">`, `<meta name="author">`
- Page title: `<title>` tag
- Canonical URL: `<link rel="canonical" href="...">`

**B2 — Microdata / Schema.org** `Priority 6`

Location: Scattered throughout the body as HTML attributes

Microdata embeds structured data directly in HTML elements using `itemprop` and `itemscope` attributes. RDFa uses `property="schema:..."` attributes. Both are maintained for Google's structured data parser.

Value location within microdata varies by element: the `content` attribute is checked first (common on `<meta>` and `<span>` tags), then `datetime` (common on `<time>` tags), then the text content of the element.

**B3 — Data Attributes** `Priority 7`

Location: Any DOM element, typically on product/listing containers

React, Vue, and Angular applications often render component props directly onto DOM elements as `data-*` attributes. A product card component may have `data-sku`, `data-price`, `data-variant-id`, and `data-in-stock` attributes even when the visible price text is rendered dynamically. These attributes survive static scraping because they are present in the server-rendered HTML.

The extractor scans elements likely to carry product/listing data (containers with class names containing "product", "listing", "item") and extracts all `data-*` attributes, normalizing hyphenated keys to underscore format.

**B4 — Hidden Inputs** `Priority 8`

Location: `<input type="hidden">` elements

Hidden inputs carry internal identifiers, page state, and metadata that the site's forms need to function. Commonly found: product IDs, variant IDs, category IDs, page context tokens.

Security tokens (`csrf_token`, `_token`, `authenticity_token`) are filtered out automatically as they are not useful data.

---

#### Category C — CSS-Hidden Elements

**C1 — Inline Hidden Elements** `Priority 9`

Location: Elements with `style="display:none"` or `style="visibility:hidden"`

Some sites render the original price, inventory counts, or raw data values in DOM elements that are hidden from the user via inline CSS. Static scrapers have an advantage over browser automation here: the hidden elements are present in the raw HTML but are sometimes stripped from the browser's accessibility tree.

Also checked: elements with `.hidden`, `.sr-only`, or `aria-hidden="true"` classes.

Confidence is lower for this source because the context of hidden elements is ambiguous — the key is inferred from the element's `id` or `class`, which may not correspond to a meaningful field name.

---

#### Category D — HTTP Response

**D1 — Response Headers** `Priority: Pre-parse`

Analyzed in Stage 1 before body parsing. See Stage 1 for full detail.

Headers provide technology fingerprints, CDN information, cache state, and hard action signals (rate limit, auth required) that eliminate the need to parse the body at all in certain cases.

---

#### Category E — DOM Text (Fallback)

**E1 — Visible DOM Text** `Priority 10`

Location: Visible text nodes matched by CSS selectors

The most fragile extraction method. Requires the caller to supply a per-domain CSS selector map (`{"price": ".product-price__amount", "title": "h1.pdp-title"}`). These selectors break whenever the site redesigns its CSS classes.

This extractor is the last resort. If data has been found by any higher-priority extractor, the merger will prefer that source over DOM text even when both are present.

---

#### Category F — Alternative Sources

These sources are checked outside the main extractor loop, typically before or alongside Tier 1.

**F1 — Sitemap**

Location: `/sitemap.xml`, `/sitemap-products.xml`

Sitemaps provide a complete URL inventory with `lastmod` dates. Useful for discovering all pages to scrape rather than for extracting per-page data.

**F2 — RSS / Atom Feeds**

Location: `/feed.xml`, `/rss`, `/atom.xml`

RSS feeds provide structured article content — often the full text — without any HTML parsing. For news and blog scraping, RSS is preferable to HTML extraction entirely.

**F3 — Embedded iFrame `src` Attributes**

Location: `<iframe src="...">` tags

The `src` attribute of an iframe often contains the data of interest directly as query parameters (e.g., a Google Maps embed URL contains the address; a video player embed URL contains the video ID). The iframe itself doesn't need to be loaded.

---

### Stage 5 — Merge & Decide

After all extractors complete, their results are merged into a single output using priority-ordered field resolution.

**Merge rules**

1. Results with confidence below the minimum threshold (default: 0.50) are discarded
2. Results are sorted by source priority (JSON-LD = 1, DOM text = 10)
3. Fields are merged: when two extractors return the same field, the higher-priority source wins
4. If `RequiredFields` is configured, the merged output is checked for completeness

**Decision logic**

| Condition | Decision |
|---|---|
| Header hard signal (403, 404, 429) | Abort or Backoff (bypasses extractors entirely) |
| Page is hollow AND no valid extractions | Escalate to Tier 3 |
| Page is not hollow AND required fields missing | Escalate to Tier 3 |
| Page is hollow BUT data was found in script tags | Done (hollow detection does not block extraction) |
| All required fields present | Done |

**Field priority reference**

| Priority | Source | Reason |
|---|---|---|
| 1 | JSON-LD | Schema-validated, SEO-maintained, most stable |
| 2 | `__NEXT_DATA__` | Complete server payload, typed |
| 3 | Framework state globals | Full app state, but less structured |
| 4 | Inline variable assignments | Present but requires regex; may be JS not JSON |
| 5 | Meta tags | Stable, maintained for social sharing |
| 6 | Microdata / Schema.org | Maintained for Google, but attribute-scattered |
| 7 | Data attributes | Reliable on React/Vue apps; class-independent |
| 8 | Hidden inputs | Good for IDs; poor for content fields |
| 9 | CSS-hidden elements | Ambiguous context, inferred keys |
| 10 | DOM text | Fragile; breaks on CSS redesigns |

---

### Tier 2 Complete Flow

```
HTTP Response (from Tier 1)
         │
         ▼
┌─────────────────────┐
│  Stage 1            │  Status 404 → Abort
│  Header Analysis    │  Status 403 → Abort
│                     │  Status 429 → Backoff
│                     │  CF challenge → Escalate (Tier 5)
│                     │  x-powered-by: Next.js → flag for Stage 4
└────────┬────────────┘
         │ Continue
         ▼
┌─────────────────────┐
│  Stage 2            │
│  Parse HTML Once    │  Single goquery.Document
│  (shared ref)       │  passed to all extractors
└────────┬────────────┘
         │
         ▼
┌─────────────────────┐
│  Stage 3            │  Checks: empty shell, tiny body,
│  Hollow Detection   │  JS-required text, CAPTCHA,
│                     │  text density ratio
└────────┬────────────┘
         │
         ▼
┌─────────────────────────────────────────────────────────┐
│  Stage 4 — Concurrent Extraction                        │
│                                                         │
│  goroutine: JSON-LD Extractor        → result           │
│  goroutine: Next.js Extractor        → result           │
│  goroutine: State Global Extractor   → result           │
│  goroutine: Inline Var Extractor     → result           │
│  goroutine: Meta Tag Extractor       → result           │
│  goroutine: Microdata Extractor      → result           │
│  goroutine: Data Attr Extractor      → result           │
│  goroutine: Hidden Input Extractor   → result           │
│  goroutine: CSS Hidden Extractor     → result           │
│  goroutine: DOM Text Extractor       → result           │
│                                                         │
│  All goroutines bounded by 5s context timeout          │
└────────┬────────────────────────────────────────────────┘
         │ all results via channel
         ▼
┌─────────────────────┐
│  Stage 5            │  Priority-merge all results
│  Merge & Decide     │  Check required fields
│                     │  Combine with hollow signals
└────────┬────────────┘
         │
    ┌────┴──────────────────────┐
    │                           │
 ActionDone               ActionEscalate
 Return merged data       → Tier 3
```

---

## 6. Tier 3 — Lightweight Browser Render

### Responsibility

Execute JavaScript and return the fully rendered DOM. Used when Tier 2 confirms that content cannot be obtained statically.

### Technology

Go-Rod — a native Go browser automation library. Avoids the "Node.js tax" (150 MB RAM for the Node.js runtime) by controlling Chrome directly from Go.

### Configuration

**Browser launch flags**

- `--disable-gpu` — no GPU required in a headless VPS environment
- `--disable-images` — images are never needed for text data extraction
- `--no-sandbox` — required in Docker environments
- `--disable-dev-shm-usage` — prevents crashes under memory pressure

**Resource interception — block at the network layer**

| Resource Type | Block? | Reason |
|---|---|---|
| CSS stylesheets | Yes | Not needed; saves bandwidth and parse time |
| Images / video | Yes | Not needed for data extraction |
| Analytics scripts | Yes | Reduces idle wait time significantly |
| Tracking pixels | Yes | Unnecessary network requests |
| Font files | Yes | Not needed |
| First-party JS | No | Required to render the page |
| First-party XHR/Fetch | No | May contain the data being extracted |

**Wait policy**

Do not wait for the full page `load` event. Use one of the following strategies in order of preference:

1. Wait for the specific target element selector to appear in the DOM
2. Wait for network idle (no pending requests for 500ms), with a hard 8-second timeout
3. Never use an unconditional sleep — it is fragile and wastes time

**Instance management**

Maintain a pool of persistent browser instances rather than launching a new browser per request. Cold-starting Chrome takes 600–900ms. A pooled page from a warm browser instance is ready in under 100ms.

### Escalation Criteria to Tier 4

Tier 3 escalates when:
- The page requires a cookie consent interaction before content loads
- Login or multi-step authentication is required to access the content
- Canvas or WebGL fingerprinting blocks the request despite `--disable-gpu`
- The page detects Go-Rod's Chrome as headless via navigator properties

---

## 7. Tier 4 — Managed Browser Container

### Responsibility

Handle complex session scenarios that Go-Rod cannot manage cleanly. Isolate the heavy Chrome process from the main Go application.

### Technology

Browserless running in a Docker container. The Go application connects to Browserless via WebSocket (the standard Chrome DevTools Protocol) and drives it remotely.

### Why Docker isolation

At 10–30 RPS, a memory leak or crash in a Chrome instance must not bring down the Go application. Docker provides a hard memory ceiling and process isolation. If a Chrome instance leaks memory or crashes, Docker restarts the container without affecting the scraper.

### Escalation Criteria (from Tier 3)

| Scenario | Indicator |
|---|---|
| Cookie consent wall | Overlay blocks DOM interaction before content is visible |
| Multi-step login | Redirect to `/login` or presence of auth form |
| WebGL / Canvas fingerprint block | HTTP 403 returned after page renders |
| Complex session state | Session cookie required that wasn't established in Tier 3 |

### Escalation Criteria to Tier 5

Tier 4 escalates when Browserless returns a confirmed 403 Forbidden or a CAPTCHA challenge page. These indicate active bot detection that requires a stealth-patched browser and residential IP.

---

## 8. Tier 5 — Stealth Protocol

### Responsibility

Bypass active bot detection systems (Cloudflare, Akamai, PerimeterX) that block all previous tiers.

### Technology

- Residential proxy network (rotating IPs sourced from real consumer connections)
- Stealth-patched Chromium (patches that hide headless browser fingerprint markers)

### Trigger Conditions

Tier 5 is only triggered by confirmed evidence from Tiers 1–4:
- HTTP 403 returned after a full page render (not just a static 403)
- CAPTCHA challenge page detected in the rendered DOM

Tier 5 is never triggered speculatively.

### Proxy Strategy

**Sticky vs. rotating sessions**

Use sticky sessions (same exit IP for the duration of a multi-page flow) for:
- Any flow requiring login
- Pagination across multiple pages of the same site
- Any flow where the server tracks session state by IP

Use rotating IPs for:
- Single-page extractions with no session state
- Sites that rotate CAPTCHAs based on IP reputation

**Cost control**

Residential proxy traffic is billed by the megabyte. Tier 5 must also block images, fonts, and analytics at the network level to minimize egress cost.

---

## 9. Cross-Cutting Concerns

### Rate Limiting

Rate limiting applies at the domain level, not globally. A single domain receiving 30 RPS will trigger bot detection regardless of how politely the rest of the pipeline behaves.

- Per-domain concurrency cap (configurable, default: 2 concurrent requests per domain)
- Jittered delay between requests (randomized within a range, not fixed intervals — fixed intervals are fingerprintable)
- Exponential backoff on consecutive failures from the same domain
- Circuit breaker: if a domain returns 5xx or 429 three consecutive times, pause all requests to that domain for a configured duration

### Caching

A cache layer between Tier 1 and Tier 2 eliminates redundant requests for recently-fetched pages.

Cache key: URL + ETag or Last-Modified header value  
TTL: Configurable per domain based on content update frequency  
Storage: Redis for distributed deployments, in-memory LRU for single-node

### Observability

Each tier emits metrics that allow tuning of the pipeline over time. The most important metric is the **tier hit rate** — what percentage of requests resolve at each tier.

| Metric | Why It Matters |
|---|---|
| Requests resolved at Tier 1/2 | High % here means low infrastructure cost |
| Tier 2 extractor hit rate per source | Identifies which extractors are underperforming |
| Tier 3/4/5 escalation rate | High % here signals misconfigured Tier 2 detection |
| Abort rate by status code | High 403 abort rate may mean IP reputation issues |
| Backoff rate by domain | Identifies domains where rate limits need tuning |
| Per-extractor latency | Identifies slow extractors that block the merge |

### Error Taxonomy

Not all failures are escalation candidates. The pipeline must distinguish between:

| Error Type | Correct Response | Wrong Response |
|---|---|---|
| 404 Not Found | Abort — resource doesn't exist | Escalating tiers (no tier can fix a missing resource) |
| 403 Forbidden (static) | Abort — fix auth or use Tier 5 | Escalating to Tier 3/4 (rendering won't help a 403) |
| 403 Forbidden (post-render) | Escalate to Tier 5 | Aborting (this 403 is from bot detection, fixable) |
| 429 Rate Limited | Backoff same tier | Escalating (a browser won't bypass a rate limit) |
| 503 + Retry-After | Backoff same tier | Escalating |
| Hollow page, no data | Escalate to Tier 3 | Returning empty result |
| Required field missing | Escalate to Tier 3 | Returning partial result as success |

---

## 10. Decision Reference

### When to Escalate vs. Abort vs. Backoff

```
Is the status code 404?
  → Abort. No tier can create a resource that doesn't exist.

Is the status code 401 or 403 (received before rendering)?
  → Abort. This is an authentication issue, not a rendering issue.

Is the status code 429?
  → Backoff. Respect Retry-After. Do not advance tiers.

Is the status code 503?
  → Backoff. Temporary. Retry same tier.

Is there a Cloudflare challenge header?
  → Skip Tiers 2–4. Escalate directly to Tier 5.

Is there a CAPTCHA in the rendered DOM (confirmed by Tier 3 or 4)?
  → Escalate to Tier 5.

Is the page hollow (JS required) AND no data in script tags?
  → Escalate to Tier 3.

Is the page hollow BUT data found in __NEXT_DATA__ or JSON-LD?
  → Done. Hollow detection does not block script tag extraction.

Are required fields present in merged output?
  → Done.

Are required fields missing AND page is not hollow?
  → Escalate to Tier 3. The data may exist but be in a DOM location not covered by current selectors.
```

### Extractor Priority Quick Reference

| Priority | Extractor | Location | Confidence |
|---|---|---|---|
| 1 | JSON-LD | `<script type="application/ld+json">` | 0.95 |
| 2 | `__NEXT_DATA__` | `<script id="__NEXT_DATA__">` | 0.92 |
| 3 | State Globals | `window.__REDUX_STATE__` etc. | 0.85 |
| 4 | Inline Variables | `var x = {...}` in `<script>` | 0.75 |
| 5 | Meta Tags | `<head>` `og:*`, `twitter:*` | 0.80 |
| 6 | Microdata | `itemprop`, `itemscope`, `property` | 0.78 |
| 7 | Data Attributes | `data-*` on DOM elements | 0.72 |
| 8 | Hidden Inputs | `<input type="hidden">` | 0.65 |
| 9 | CSS-Hidden Elements | `display:none` elements | 0.55 |
| 10 | DOM Text | CSS selector → text content | 0.60 |

---
