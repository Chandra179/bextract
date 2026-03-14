# Tier 5 — Stealth Protocol

**Cost:** ~512 MB RAM · 3–8s · $0.05+/request  
**Technology:** Residential proxy network + stealth-patched Chromium

---

## Responsibility

Bypass active bot detection systems (Cloudflare, Akamai, PerimeterX) that block all previous tiers.

---

## Trigger Conditions

Tier 5 is **only triggered by confirmed evidence** from Tiers 1–4. It is never triggered speculatively.

| Trigger | Evidence Required |
|---|---|
| 403 post-render | HTTP 403 returned after full page render in Tier 4 |
| CAPTCHA challenge | CAPTCHA widget detected in rendered DOM (reCAPTCHA, hCaptcha, Turnstile) |

A static 403 at Tier 1 does **not** trigger Tier 5 — that is an authentication or IP block that a residential proxy might bypass, but the cost is only justified if cheaper tiers have already confirmed the block is bot-detection-related.

---

## Technology

**Residential proxy network:**
Exit IPs sourced from real consumer connections. Bot detection systems maintain reputation scores on datacenter IP ranges; residential IPs have no such penalty.

**Stealth-patched Chromium:**
Standard headless Chrome exposes several fingerprint markers — `navigator.webdriver`, specific CDP artifacts, missing browser APIs — that anti-bot systems detect. The stealth-patched build removes or spoofs these markers.

---

## Proxy Strategy

### Sticky vs. Rotating Sessions

**Use sticky sessions** (same exit IP for the full flow) for:
- Any flow requiring login
- Pagination across multiple pages of the same site
- Any flow where the server tracks session state by IP

**Use rotating IPs** for:
- Single-page extractions with no session state
- Sites that rotate CAPTCHAs based on IP reputation

### Cost Control

Residential proxy traffic is billed **per megabyte**. Tier 5 must block images, fonts, and analytics at the network layer — the same resource interception rules as Tier 3 — to minimize egress cost.

At $0.05+ per request, Tier 5 requests should be logged with full detail (URL, proxy cost, bytes transferred) for cost attribution.

---/