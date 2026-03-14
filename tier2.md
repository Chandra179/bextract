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

## Stage 3 — Hollow Page Detection

Before running extractors, the pipeline determines whether the page is a **JavaScript shell** — a page that renders its content client-side and has no useful data in the static HTML.

This does **not** immediately trigger escalation. Even hollow pages can contain data in `<script>` tags (JSON-LD, `__NEXT_DATA__`). The hollow signal is recorded and factored into the Stage 5 decision.

### Hollow Signals and Penalty Weights

Each signal has a penalty weight. Penalties accumulate. A page is classified as hollow when the **total penalty ≥ 0.70**.

| Signal | Detection Condition | Penalty |
|---|---|---|
| Empty app shell | `<div id="app"></div>` with no children | 0.85 |
| No-script message | Text "enable JavaScript" or "JavaScript required" in body | 0.90 |
| Tiny body | Raw body size < 5 KB | 0.50 |
| Low text density | Visible text < 5% of total HTML byte size | 0.70 |
| CAPTCHA present | reCAPTCHA / hCaptcha / Turnstile widget detected in DOM | 0.95 |
| Cloudflare challenge | `cf-mitigated: challenge` header (already handled in Stage 1) | 1.00 |

> **Note:** Body size alone is not a reliable hollow signal. A 3 KB page can be a valid small article; a 200 KB page can be a React shell with all content deferred. The **text-density ratio** is the more reliable metric.

## Stage 4 — Concurrent Extraction

All extractors run **simultaneously in separate goroutines**. They all receive the same shared `goquery.Document` and emit independent results into a results channel.

- Total extraction time = time of the **slowest single extractor**, not the sum of all
- A hard **5-second context timeout** cancels all goroutines if extraction stalls
- Extractors are organized into two phases to allow early exit on high-confidence hits

### Two-Phase Grouping

```
Phase A — High-signal, near-instant (run first):
  JSON-LD, __NEXT_DATA__, Meta Tags

  → If all required fields are satisfied by Phase A results:
    skip Phase B entirely

Phase B — Slower / noisier (run only if Phase A is insufficient):
  Framework globals, inline variables, data attributes,
  hidden inputs, CSS-hidden elements, DOM text
```

Most pages that have `JSON-LD` or `__NEXT_DATA__` won't need Phase B at all. This saves goroutine overhead on the noisier extractors for the majority of requests.

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

#### B1 — Meta Tags `Priority 5 · Confidence 0.80`

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

#### B2 — Microdata / Schema.org `Priority 6 · Confidence 0.78`

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

#### B3 — Data Attributes `Priority 7 · Confidence 0.72`

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

#### B4 — Hidden Inputs `Priority 8 · Confidence 0.65`

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

#### C1 — CSS-Hidden Elements `Priority 9 · Confidence 0.55`

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

#### E1 — DOM Text (Fallback) `Priority 10 · Confidence 0.60`

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

### List Page Extraction

When `PageMode = "list"` (triggered by `IsLinkRich = true` from Stage 3), the standard single-entity extractors are replaced by a **ListExtractor** that returns an array of items instead of a single field map.

#### How the ListExtractor works

**Step 1 — Find the repeating container**

The extractor scans candidate elements for structural repetition:

```
Candidates: li, article, tr, div[class]

For each candidate selector:
  Count how many elements match
  If count ≥ 5: score this selector as a candidate

Score each candidate by:
  - Repetition count (higher = better signal)
  - Text density per item (items with only <a> tags score lower)
  - Presence of href in each item
  - Item text length > 20 chars (filters nav links and footer items)
```

The highest-scoring selector becomes the `item_container`.

**Step 2 — Extract fields per item**

Each matched container element is treated as a mini-document. The same extractors (data attributes, meta, DOM text) run scoped to that element's subtree.

**Step 3 — Output shape**

```json
{
  "fields": {
    "title": { "value": "Hacker News", "source": "meta-tags" }
  },
  "items": [
    {
      "title": { "value": "Ask HN: ...", "source": "dom-text" },
      "url":   { "value": "https://...", "source": "dom-attr" },
      "score": { "value": "312",         "source": "dom-text" }
    }
  ],
  "item_count": 30,
  "item_container": "tr.athing"
}
```

`fields` contains page-level data (canonical URL, page title). `items` is the array of extracted records. `item_container` is the resolved CSS selector — useful for debugging and for pinning a per-domain config.

**False positive prevention:**

Nav menus and footers are also structurally repetitive. The extractor filters them out by requiring that each candidate item contain at least one non-navigational text node (text length > 20 chars) or have `data-*` attributes. A container of bare `<a>` tags with short text is treated as navigation, not content.

---

## Stage 5 — Merge & Decide

After all extractors complete, results are merged into a single output.

### Merge Rules

1. Results with confidence below the minimum threshold (default: **0.50**) are discarded
2. Remaining results are sorted: **source priority first, confidence second**
3. Fields are merged by priority: when two extractors return the same field, the higher-priority source wins
4. If `RequiredFields` is configured, the merged output is checked for completeness

### Sort Order Detail

```
Primary key:   source priority (lower number = wins)
               JSON-LD (1) always beats DOM-text (10)

Secondary key: confidence score (higher = wins, tiebreaker within same source)
               used when the same source appears twice for the same field

Tertiary key:  first-seen (deterministic last resort)
```

Confidence is never the primary sort key. A DOM-text extractor with 0.95 confidence still loses to JSON-LD with 0.60 confidence — the source hierarchy exists because DOM-text is structurally fragile regardless of how certain the extractor is.

### Conflict Preservation

When two **different high-priority sources** disagree on a field value, the runner-up is preserved in the output rather than silently discarded:

```json
"price": {
  "value": "$29.99",
  "source": "json-ld",
  "confidence": 0.95,
  "conflict": {
    "value": "29.99",
    "source": "next-data",
    "confidence": 0.90
  }
}
```

Useful during development for tuning per-domain source trust. Can be suppressed in production via config.

### Decision Logic

| Condition | Decision |
|---|---|
| Header hard signal (403, 404, 429) | `Abort` or `Backoff` — bypasses extractors entirely |
| Page is hollow AND no valid extractions | `Escalate` to Tier 3 |
| Page is not hollow AND required fields missing | `Escalate` to Tier 3 |
| Page is hollow BUT data found in script tags | `Done` — hollow detection does not block extraction |
| All required fields present | `Done` |

---

## Complete Flow Diagram

```
HTTP Response (from Tier 1)
         │
         ▼
┌─────────────────────┐
│  Stage 1            │  404       → Abort
│  Header Analysis    │  403       → Abort
│                     │  429       → Backoff
│                     │  cf-mitigated: challenge → Escalate Tier 5
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
│  Hollow Detection   │  Check IsLinkRich → set PageMode
│                     │  Hollow if total penalty ≥ 0.70
└────────┬────────────┘
         │
         ├── PageMode = list ──────────────────┐
         │                                     ▼
         │                          ┌─────────────────────┐
         │                          │  ListExtractor       │
         │                          │  Find repeating      │
         │                          │  container, extract  │
         │                          │  items[]             │
         │                          └─────────┬───────────┘
         │                                    │
         ▼                                    │
┌─────────────────────────────────────────────────────────┐
│  Stage 4 — Concurrent Extraction                        │
│                                                         │
│  Phase A (high-signal, run first):                      │
│    goroutine: JSON-LD Extractor        → result         │
│    goroutine: Next.js __NEXT_DATA__    → result         │
│    goroutine: Meta Tag Extractor       → result         │
│                                                         │
│    ── if required fields satisfied: skip Phase B ──     │
│                                                         │
│  Phase B (if Phase A insufficient):                     │
│    goroutine: State Global Extractor   → result         │
│    goroutine: Inline Var Extractor     → result         │
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
│  Merge & Decide     │  Check required fields
│                     │  Preserve conflicts
└────────┬────────────┘
         │
    ┌────┴──────────────────────┐
    │                           │
 ActionDone               ActionEscalate
 Return merged data       → Tier 3
```

---

## Extractor Priority Quick Reference

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