# Tier 2 — Content Detection & Extraction

**Cost:** ~1 MB RAM · <150ms · $0.00  
**Technology:** `goquery` for HTML parsing · parallel goroutines for extraction

---

## Responsibility

Tier 2 is the **intelligence layer** of the pipeline. Given a raw HTTP response from Tier 1, it must:

1. Detect whether the page requires JavaScript to render
2. Exhaustively search every static data location before escalating
3. Emit exactly one decision: `Done`, `Abort`, `Backoff`, or `Escalate`

The guiding principle: **most pages can be resolved here**. Only escalate when there is confirmed evidence that static extraction cannot work.

---

## Stage Overview

Tier 2 is composed of five sequential stages:

```
Stage 1: Header Analysis         sync, pre-parse — cheapest possible gate
Stage 2: HTML Parse              single parse, shared document
Stage 3: Hollow Page Detection   sync, uses shared document
Stage 4: Concurrent Extraction   parallel goroutines, all extractors
Stage 5: Merge & Decide          priority-ordered merge, final decision
```

---

## Stage 1 — Header Analysis

Headers are inspected **before the HTML body is touched**. This eliminates entire categories of work before any parsing occurs.

### Status Code Hard Decisions

| Status | Decision | Reason |
|---|---|---|
| `404` | **Abort** | Resource does not exist — no tier can fix this |
| `401` / `403` | **Abort** | Authentication required — a browser won't help |
| `429` | **Backoff** | Rate limited — respect `Retry-After` header |
| `503` + Retry-After | **Backoff** | Temporary server error |
| `200` + tiny body | **Continue** | May be a hollow JS shell — check in Stage 3 |

`Abort` and `Backoff` bypass all remaining stages entirely. There is no reason to parse HTML or launch extractors when the status code already tells us the outcome.

### Technology Fingerprinting

Response headers reveal the site's framework, which informs which extractors are most likely to succeed. These are passed as hints into Stage 4.

| Header | Value | Implication |
|---|---|---|
| `x-powered-by` | `Next.js` | `__NEXT_DATA__` extractor will almost certainly succeed |
| `cf-ray` present | — | Cloudflare is in front; watch for challenge pages |
| `cf-mitigated` | `challenge` | Active Cloudflare challenge — **skip to Tier 5 immediately** |
| `content-type` | `application/json` | Response is already JSON — Tier 1 missed a hidden API |
| `x-powered-by` | `PHP` | Traditional server-side render — DOM text extraction likely works |

---

## Stage 2 — HTML Parse

The raw response body is parsed into a single `goquery.Document` **exactly once**. This shared document is passed as a read-only reference to every extractor in Stage 4. No extractor re-parses the HTML independently.

**Why this matters at 30 RPS:**

- HTML parsing is not free. A 200 KB HTML document takes measurable time to tokenize and build a DOM tree.
- Parsing once and sharing a reference means 10 extractors running in parallel all operate on the same pre-built tree.
- All extractors are guaranteed to see an identical document state — no drift from re-parsing.

---

## Stage 3 — Hollow Page Detection & Classification

Before running extractors, the pipeline determines whether the page is a **JavaScript shell** — a page that renders its content client-side and has no useful data in the static HTML — and classifies the page type.

This does **not** immediately trigger escalation. Even hollow pages can contain data in `<script>` tags (JSON-LD, `__NEXT_DATA__`). The hollow signal is recorded and factored into the Stage 5 decision.

### Hollow Signals and Penalty Weights

Each signal has a penalty weight. Penalties accumulate. A page is classified as hollow when the **total penalty ≥ 0.70** (configurable via `hollow.threshold`).

| Signal | Detection Condition | Default Penalty |
|---|---|---|
| CAPTCHA present | reCAPTCHA / hCaptcha / Turnstile widget detected in DOM | 0.95 |
| No-script message | Text "enable JavaScript" or "JavaScript required" in `<noscript>` | 0.90 |
| Empty app shell | `<div id="app">`, `<div id="root">`, or `<div id="__next">` with no children | 0.85 |
| Low text density | Visible text < 5% of total HTML byte size | 0.70 |
| Tiny body | Raw body size < 5 KB | 0.50 |
| Cloudflare challenge | `cf-mitigated: challenge` header (injected from Stage 1 TechHints) | 1.00 |

All penalty values are configurable via `hollow.penalties` in the config.

> **Note:** The low-text-density and tiny-body signals are **suppressed on link-rich pages** (pages with ≥ 10 text-bearing `<a>` tags). Link-heavy pages like news aggregators have low text/HTML ratios by design, not because JS is required.

### Page Type Classification

Stage 3 also classifies the page into one of four types. This is stored in the result for observability but does **not** directly influence the escalation decision in the current implementation.

| Page Type | Condition |
|---|---|
| `app-shell` | Hollow score ≥ threshold (0.70), or Cloudflare challenge detected |
| `link-rich` | ≥ 10 text-bearing links and hollow score below threshold |
| `content-rich` | Not link-rich and hollow score is 0 |
| `mixed` | Partial hollow signals but below threshold |

## Stage 4 — Concurrent Extraction

All extractors run **simultaneously in separate goroutines**. They all receive the same shared `goquery.Document` and emit independent results into a results channel.

- Total extraction time = time of the **slowest single extractor**, not the sum of all
- A hard **5-second context timeout** cancels all goroutines if extraction stalls
- Extractors are organized into two phases to allow early exit on high-confidence hits

### Two-Phase Grouping

```
Phase A — Script-tag sources (run first, priorities 1–4):
  JSON-LD, __NEXT_DATA__, Framework State Globals, Inline Variables

  → If page is hollow AND Phase A yielded any fields:
    skip Phase B entirely (structured script data is sufficient)

Phase B — DOM-heavy sources (run only if Phase A is insufficient):
  Meta Tags, Microdata, Data Attributes,
  Hidden Inputs, CSS-Hidden Elements, DOM Text
```

Hollow pages (JS shells) rarely have useful data in the DOM itself but often carry full data payloads in `<script>` tags. When Phase A finds fields on a hollow page, running DOM-heavy Phase B extractors would add latency for no benefit.

**Phase C — LLM semantic fallback (fires after merge, not concurrent):**

When structured extraction (Phases A + B) yields zero fields and the decision is `Escalate`, the pipeline optionally invokes an LLM against the page's cleaned text. This is disabled by default and controlled by `tier2.llm.enabled` in config.

See the [LLM Phase C](#llm-phase-c--semantic-fallback-priority-11--confidence-configurable) section below.

---

### Extractor Reference

#### A1 — JSON-LD `Priority 1 · Confidence 0.95`

**Location:** `<script type="application/ld+json">`

**What it does:**
Parses all `application/ld+json` script tags in the document. JSON-LD is a Google-mandated structured data format sites maintain for rich search results. Because it's maintained for Google's crawler, it is the most **stable** data on the page — CSS classes change with redesigns; JSON-LD schema fields do not.

**Fields commonly available:**
`name`, `description`, `sku`, `price`, `priceCurrency`, `availability`, `aggregateRating`, `reviewCount`, `author`, `datePublished`, `address`, `image`

**Commonly found on:**
E-commerce product pages, news articles, recipes, job postings, local business listings, events.

**How it works:**
1. Select all `script[type="application/ld+json"]` elements
2. Parse each tag's text content as JSON
3. Map `@type` to a known schema (Product, Article, JobPosting, etc.)
4. Extract typed fields by schema key name

**Why it's Priority 1:**
Schema fields are semantic and stable. A site can completely redesign its CSS and HTML structure without changing its JSON-LD. No other extractor has this property.

---

#### A2 — Next.js `__NEXT_DATA__` `Priority 2 · Confidence 0.92`

**Location:** `<script id="__NEXT_DATA__" type="application/json">`

**What it does:**
Next.js serializes the complete `getServerSideProps` or `getStaticProps` payload into this tag. It contains every field that was fetched server-side before the page rendered — often the **entire product, article, or listing object** as a typed JSON tree.

**How it works:**
1. Check for `x-powered-by: Next.js` header (hint from Stage 1) or scan for `script#__NEXT_DATA__` directly
2. Parse the tag's JSON content
3. Navigate to `props.pageProps` (the standard Next.js payload location)
4. Walk the tree to find target fields

**What to expect:**
This is a complete server payload, not a fragment. On a product page, `pageProps.product` will likely contain every field the site's database has for that product.

**Watch out for:**
Deeply nested paths vary by site. A per-domain path config (e.g. `pageProps.initialData.product.price`) improves precision over generic tree-walking.

---

#### A3 — Framework State Globals `Priority 3 · Confidence 0.85`

**Location:** Inline `<script>` tags (no `src` attribute)

**What it does:**
Frameworks that predate Next.js-style data serialization hydrate the client by assigning state to `window` globals before the app boots. These globals contain the full application state at page load time.

**Patterns scanned:**
```
window.__NUXT__
window.__REDUX_STATE__
window.__INITIAL_STATE__
window.__APP_STATE__
window.__PRELOADED_STATE__
window.__SERVER_DATA__
window.__STORE__
```

**How it works:**
1. Iterate all inline `<script>` elements (those without a `src` attribute)
2. For each pattern above, check if the script text contains the assignment
3. Extract the JSON value using a targeted regex: `window\.__KEY__\s*=\s*({.*?});`
4. Parse extracted JSON and walk for target fields

**Why lower priority than `__NEXT_DATA__`:**
These blobs are less structured — they often contain the full Redux store or Vuex state including UI state, not just page data. The target fields may be buried several levels deep in unpredictable paths.

---

#### A4 — Inline Variable Assignments `Priority 4 · Confidence 0.75`

**Location:** Inline `<script>` tags

**What it does:**
Non-framework sites inject data as plain JavaScript variable assignments. These are less structured than framework state objects and require **regex extraction** rather than direct JSON parsing, since they are JavaScript syntax, not JSON.

**Patterns scanned:**
```javascript
var productData = {...}
const CONFIG = {...}
let PAGE_DATA = {...}
```

**How it works:**
1. Iterate all inline `<script>` elements
2. Apply pattern: `(?:var|const|let)\s+([A-Z_][A-Z0-9_]*)\s*=\s*(\{[\s\S]*?\});`
3. Extract the variable name (used as the field namespace) and the value string
4. Attempt `JSON.parse` on the value; fall back to a JS-to-JSON normalizer for single-quoted strings and unquoted keys
5. Walk the resulting object for target fields

**Lower confidence because:**
The value may be JavaScript syntax that isn't valid JSON. Regex extraction is brittle against minified code. Variable names don't map cleanly to semantic field names.

---

#### B1 — Meta Tags `Priority 5 · Confidence 0.88`

**Location:** `<head>` section

**What it does:**
Extracts page-level metadata maintained for social sharing (Open Graph, Twitter Cards) and SEO. These are a reliable source of title, description, canonical URL, and preview image.

**Sources extracted:**

| Tag | Fields |
|---|---|
| Open Graph | `og:title`, `og:description`, `og:image`, `og:url`, `og:type`, `og:price:amount` |
| Twitter Cards | `twitter:title`, `twitter:description`, `twitter:image` |
| Standard meta | `description`, `author`, `keywords` |
| Title tag | `<title>` text content |
| Canonical | `<link rel="canonical" href="...">` |

**How it works:**
1. Select all `<meta>` tags in `<head>`
2. For each, check `property` attribute (Open Graph uses `property`) and `name` attribute (standard meta uses `name`)
3. Extract `content` attribute value
4. Normalize to a flat field map: `og:title` → `title`, `og:description` → `description`

**Why it's reliable:**
Sites break their CSS regularly. They rarely break their Open Graph tags because that would break link previews on Slack, Twitter, and iMessage.

---

#### B2 — Microdata / Schema.org `Priority 6 · Confidence 0.82`

**Location:** Scattered throughout the body as HTML attributes

**What it does:**
Microdata and RDFa embed structured data directly in HTML elements using `itemprop`/`itemscope` (Microdata) or `property="schema:..."` (RDFa) attributes. Both are maintained for Google's structured data parser.

**How it works:**
1. Find the root `itemscope` element (e.g. `<div itemscope itemtype="https://schema.org/Product">`)
2. Within that scope, find all `[itemprop]` elements
3. For each `itemprop`, extract the value in priority order:
   - `content` attribute first (used on `<meta>` and `<span>`)
   - `datetime` attribute (used on `<time>`)
   - Text content of the element (fallback)
4. Map `itemprop` key name to the output field

**Why lower priority than JSON-LD:**
The same schema fields are available, but Microdata is harder to parse correctly because the value location varies by element type. JSON-LD puts everything in one place; Microdata scatters it across the DOM.

---

#### B3 — Data Attributes `Priority 7 · Confidence 0.78`

**Location:** Any DOM element, typically on product/listing containers

**What it does:**
React, Vue, and Angular applications often render component props directly onto DOM elements as `data-*` attributes. These attributes survive static scraping because they are present in the **server-rendered HTML**.

Example: A product card may have `data-sku="ABC123"`, `data-price="29.99"`, `data-in-stock="true"` even when the displayed price text is rendered by client-side JavaScript.

**How it works:**
1. Select elements likely to carry product/listing data — containers whose `class` attribute contains: `product`, `listing`, `item`, `card`, `result`
2. For each matched element, extract all `data-*` attributes
3. Normalize hyphenated keys to underscore format: `data-product-id` → `product_id`
4. Filter out generic utility attributes: `data-v-*` (Vue scoped styles), `data-reactid`, `data-testid`

**Lower confidence because:**
`data-*` key names are developer-defined and not standardized. `data-id` on one site may mean a product ID; on another it may be a UI element ID.

---

#### B4 — Hidden Inputs `Priority 8 · Confidence 0.72`

**Location:** `<input type="hidden">` elements

**What it does:**
Hidden inputs carry internal identifiers, page state, and metadata that forms need to function. Useful for extracting product IDs, variant IDs, category IDs, and page context tokens.

**How it works:**
1. Select all `input[type="hidden"]`
2. Extract `name` and `value` attribute pairs
3. Filter out security tokens automatically: `csrf_token`, `_token`, `authenticity_token`, `__RequestVerificationToken`
4. Return remaining pairs as a field map

**Limitations:**
Good for IDs and internal tokens. Poor for content fields like title, description, or price — those are rarely stored in hidden inputs.

---

#### C1 — CSS-Hidden Elements `Priority 9 · Confidence 0.60`

**Location:** Elements with inline `display:none` or `visibility:hidden`

**What it does:**
Some sites render original prices, inventory counts, or raw data values in DOM elements that are hidden from the user via inline CSS. Static scrapers have an **advantage** over browser automation here: hidden elements are present in the raw HTML but are sometimes stripped from the browser's accessibility tree.

**Selectors checked:**
- `[style*="display:none"]`
- `[style*="visibility:hidden"]`
- `.hidden`, `.sr-only`
- `[aria-hidden="true"]`

**How it works:**
1. Select all elements matching the above
2. Attempt to infer a field name from the element's `id` or `class` attribute
3. Extract text content as the value
4. Assign low confidence (0.55) because the inferred key is ambiguous

**Lower confidence because:**
The field name is inferred from CSS class names, which are not semantic. `class="price--original"` probably means the original price; `class="d-none my-custom-thing"` may mean anything. The merger will prefer any higher-priority source over this extractor.

---

#### E1 — DOM Text (Fallback) `Priority 10 · Confidence 0.55`

**Location:** Visible text nodes matched by CSS selectors

**What it does:**
Extracts text content from DOM elements using a **caller-supplied CSS selector map**. This is the most direct way to scrape a page but also the most fragile.

**How it works:**
1. Caller provides a per-domain selector config:
   ```json
   {
     "price":  ".product-price__amount",
     "title":  "h1.pdp-title",
     "rating": "[data-testid='rating-value']"
   }
   ```
2. For each field, run the selector against the shared document
3. Extract the first matching element's text content
4. Trim whitespace, normalize currency symbols

**Why it's the last resort:**
CSS class names change with every frontend redesign. A selector that works today may return nothing after the site ships a new UI. This extractor requires active maintenance per domain.

If data has been found by any higher-priority extractor, the merger will prefer that source over DOM text even when both are present.

---

#### LLM Phase C — Semantic Fallback `Priority 11 · Confidence configurable (default 0.70)`

**When it fires:** After the Stage 5 merge, **only** when:
1. Structured extraction (Phases A + B) yielded zero fields, AND
2. The merge decision is `Escalate`

**Disabled by default.** Enable with `tier2.llm.enabled: true` in config. Requires `ANTHROPIC_API_KEY` env var or `tier2.llm.api_key` in config.

**What it does:**
1. Runs `go-readability` on the shared `goquery.Document` to extract clean article text (strips nav, ads, boilerplate)
2. Truncates the text to `max_text_bytes` (default 4000) to control token cost
3. Sends the text to the configured model (default `claude-haiku-4-5`) with a structured extraction prompt
4. Parses the JSON response into a field map
5. If fields are returned, flips the decision from `Escalate` to `Done`

**Config knobs:**

| Key | Default | Purpose |
|---|---|---|
| `llm.enabled` | `false` | Must be explicitly enabled |
| `llm.model` | `claude-haiku-4-5` | Model to use; Haiku is the cost-optimised choice |
| `llm.max_tokens` | `512` | Sufficient for a flat JSON field map |
| `llm.timeout_ms` | `15000` | Separate timeout from the 5s extraction timeout |
| `llm.max_text_bytes` | `4000` | ~1000 tokens; controls per-request LLM cost |
| `llm.confidence` | `0.70` | Applied to all LLM-extracted fields |

**Cost implication:** Each LLM call adds ~$0.0001–0.001 per request at Haiku pricing depending on page text length. Phase C only fires when all structured extractors returned nothing, so it is rare in practice.

**Why it's Priority 11 (lowest):**
LLM extraction is always the last resort. It cannot distinguish between semantically similar fields with the precision of JSON-LD or structured data formats, and its output is model-dependent. Any structured extractor that succeeds at any priority will always win.

---

## Stage 5 — Merge & Decide

After all extractors complete, results are merged into a single output.

### Merge Rules

1. Results with errors, zero fields, or confidence below the minimum threshold (default: **0.50**) are discarded
2. Remaining results are sorted by **source priority ascending** (lower number = higher precedence)
3. Fields are merged first-write-wins: when two extractors return the same field, the higher-priority source claims it

### Sort and Merge Order

```
Sort key:   source priority ascending (lower number = wins)
            JSON-LD (1) always beats DOM-text (10)

Merge:      first-write-wins — results are iterated in priority order,
            and the first extractor to provide a field name claims it
```

Results below the minimum confidence threshold (default 0.50) are filtered out before sorting. Confidence is never the primary sort key. A DOM-text extractor with 0.95 confidence still loses to JSON-LD with 0.60 confidence — the source hierarchy exists because DOM-text is structurally fragile regardless of how certain the extractor is.

### First-Write-Wins

The merge uses a first-write-wins strategy: fields are iterated in priority order and the first extractor to provide a value for a given field name wins. Lower-priority extractors that return the same field are silently discarded. There is no conflict tracking in the current implementation.

### Decision Logic

| Condition | Decision |
|---|---|
| Header hard signal (404, 401, 403) | `Abort` — bypasses extractors entirely |
| Header hard signal (429, 503 + Retry-After) | `Backoff` — bypasses extractors entirely |
| Cloudflare challenge (`cf-mitigated: challenge`) | `Escalate` — bypasses extractors, skip to higher tier |
| Page is hollow AND no valid extractions | `Escalate` to Tier 3 |
| Page is hollow BUT data found in script tags (Phase A) | `Done` — hollow detection does not block extraction |
| Any fields extracted (non-hollow page) | `Done` |

> **Known limitation:** The current `decideOutcome` implementation returns `Done` for non-hollow pages even when zero fields are extracted. This means the LLM Phase C fallback only fires for hollow pages that produced no script-tag data. A non-hollow page with no extractable structured data will return `Done` with an empty field map rather than escalating.

---

## Complete Flow Diagram

```
HTTP Response (from Tier 1)
         │
         ▼
┌─────────────────────┐
│  Stage 1            │  404       → Abort
│  Header Analysis    │  401/403   → Abort
│                     │  429       → Backoff
│                     │  503+Retry → Backoff
│                     │  cf-mitigated: challenge → Escalate
│                     │  x-powered-by: Next.js   → hint for Stage 4
└────────┬────────────┘
         │ Continue
         ▼
┌─────────────────────┐
│  Stage 2            │
│  Parse HTML Once    │  Single goquery.Document
│  (shared ref)       │  passed read-only to all extractors
└────────┬────────────┘
         │
         ▼
┌─────────────────────┐
│  Stage 3            │  Accumulate hollow penalty signals
│  Hollow Detection   │  Classify page type (app-shell, content-rich,
│  + Classification   │  link-rich, mixed)
│                     │  Hollow if total penalty ≥ 0.70
└────────┬────────────┘
         │
         ▼
┌─────────────────────────────────────────────────────────┐
│  Stage 4 — Concurrent Extraction                        │
│                                                         │
│  Phase A (script-tag sources, priorities 1–4):          │
│    goroutine: JSON-LD Extractor        → result         │
│    goroutine: Next.js __NEXT_DATA__    → result         │
│    goroutine: State Global Extractor   → result         │
│    goroutine: Inline Var Extractor     → result         │
│                                                         │
│    ── if hollow AND Phase A yielded fields: skip B ──   │
│                                                         │
│  Phase B (DOM sources, priorities 5–10):                │
│    goroutine: Meta Tag Extractor       → result         │
│    goroutine: Microdata Extractor      → result         │
│    goroutine: Data Attr Extractor      → result         │
│    goroutine: Hidden Input Extractor   → result         │
│    goroutine: CSS Hidden Extractor     → result         │
│    goroutine: DOM Text Extractor       → result         │
│                                                         │
│  All goroutines bounded by 5s context timeout           │
└────────┬────────────────────────────────────────────────┘
         │ all results via channel
         ▼
┌─────────────────────┐
│  Stage 5            │  Sort by priority, then confidence
│  Merge & Decide     │  First-write-wins field merge
└────────┬────────────┘
         │
    ┌────┴──────────────────────┐
    │                           │
  Done                     Escalate ──┐
  Return merged data                  │
                                      ▼
                           ┌────────────────────┐
                           │  Phase C (optional) │
                           │  LLM Fallback       │
                           │  Fires only if:     │
                           │  - 0 fields found   │
                           │  - llm.enabled=true │
                           └────────┬────────────┘
                                    │
                              ┌─────┴─────┐
                              │           │
                            Done      Escalate
                            (LLM      → Tier 3
                            fields)
```

---

## Extractor Priority Quick Reference

| Priority | Phase | Extractor | Location | Confidence |
|---|---|---|---|---|
| 1 | A | JSON-LD | `<script type="application/ld+json">` | 0.95 |
| 2 | A | `__NEXT_DATA__` | `<script id="__NEXT_DATA__">` | 0.92 |
| 3 | A | State Globals | `window.__REDUX_STATE__` etc. | 0.85 |
| 4 | A | Inline Variables | `var x = {...}` in `<script>` | 0.75 |
| 5 | B | Meta Tags | `<head>` `og:*`, `twitter:*` | 0.88 |
| 6 | B | Microdata | `itemprop`, `itemscope`, `property` | 0.82 |
| 7 | B | Data Attributes | `data-*` on DOM elements | 0.78 |
| 8 | B | Hidden Inputs | `<input type="hidden">` | 0.72 |
| 9 | B | CSS-Hidden Elements | `display:none` elements | 0.60 |
| 10 | B | DOM Text | CSS selector → text content | 0.55 |
| 11 | C | LLM Semantic Fallback | `go-readability` clean text → LLM | 0.70 (configurable) |

---