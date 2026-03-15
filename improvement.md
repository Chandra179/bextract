● Architecture Analysis

  What's Working Well

  The cost-escalation ladder is sound. The 4-decision model (Done/Escalate/Abort/Backoff) at every tier creates clean boundaries. Each
  tier only fires when the previous one has confirmed evidence it can't fulfill — this prevents wasted compute.

  Tier 2's two-phase extraction is smart. Splitting extractors into Phase A (script-tag, priority 1-4) and Phase B (DOM, priority 5-10)
   with the hollow+hit early exit (stage5_merge.go:11-13) avoids running 6 DOM extractors on SPA shells where the data was already in
  __NEXT_DATA__ or JSON-LD.

  Shared goquery document (analyzer.go:68) — parsing HTML once and passing the doc to all 10 extractors avoids redundant parsing
  overhead.

  Tier 3's resource interception blocking CSS/images/fonts/analytics is the right call for a data-extraction pipeline. It cuts
  bandwidth and idle wait time significantly.

  ---
  Issues & Improvements

  1. decideOutcome is incomplete (stage5_merge.go:79-96)

  func decideOutcome(...) pipeline.Decision {
      if hollow.IsHollow && len(merged) == 0 {
          return pipeline.DecisionEscalate
      }
      if hollow.IsHollow && hasScriptTagData {
          return pipeline.DecisionDone
      }
      return pipeline.DecisionDone  // ← always Done, even with 0 fields on a non-hollow page
  }

  Problem: A non-hollow page with zero extracted fields still returns Done. This means the LLM fallback in Phase C never fires for
  non-hollow pages (it checks len(result.Fields) == 0 && result.Decision == DecisionEscalate). If all 10 extractors return nothing on a
   content-rich page, the pipeline says "Done" with empty fields instead of escalating to Tier 3.

  Fix: Return DecisionEscalate when len(merged) == 0 regardless of hollow status.

  2. valid := results[:0] mutates the input slice (stage5_merge.go:35)

  valid := results[:0]

  This reuses the backing array of results, which silently overwrites elements. It works today because results isn't read again after
  merge(), but it's a latent bug — any future caller that inspects results after merge will see corrupted data. Use
  make([]pipeline.ExtractorResult, 0, len(results)) instead.

  3. Runner creates a new tier2.Analyzer on every request with custom timeout (runner.go:57-62)

  if req.ExtractionTimeout > 0 {
      overrideCfg := r.t2cfg
      overrideCfg.ExtractionTimeoutMs = int(req.ExtractionTimeout.Milliseconds())
      analyzer = tier2.New(overrideCfg, r.log)
  }

  tier2.New() potentially creates a new LLM client each time. If the LLM is enabled and many requests arrive with custom timeouts, this
   allocates a new HTTP client per request. Consider passing the timeout at the Analyze() call level rather than reconstructing the
  whole Analyzer.

  4. Tier 1 header flattening loses multi-value headers (runner.go:174-179)

  for k, vs := range r.Headers {
      if len(vs) > 0 {
          headers[k] = vs[0]  // drops vs[1], vs[2], ...
      }
  }

  Set-Cookie commonly has multiple values. If any extractor or tier ever needs cookies from the stored response, this silently drops
  them. Store []string in store.Tier1Result.Headers.

  5. Duplicated decisionString function

  decisionString is defined identically in both internal/api/handler.go:115 and internal/runner/runner.go:271. Move it to
  pipeline/types.go as a Decision.String() method — it belongs with the type.

  6. Tier 3 blocks analytics domains only for ResourceTypeDocument (renderer.go:142)

  router.Add(pat, proto.NetworkResourceTypeDocument, func(h *rod.Hijack) { ... })

  Analytics domains send scripts, pixels (images), and XHR beacons — not documents. This rule blocks almost nothing. It should match
  all resource types (use a wildcard or omit the type filter).

  7. No backoff handling in the Runner (runner.go:104)

  if analysis.Decision != pipeline.DecisionEscalate {
      return runResultFromAnalysis(analysis, 2, jobID, time.Since(start)), nil
  }

  When Tier 2 returns DecisionBackoff, the runner returns it directly to the caller. There's no retry loop. The RetryAfter duration is
  computed but never acted on. Either:
  - Implement a retry with time.After(retryAfter) in the runner, or
  - Document that the caller is responsible for retrying (and surface RetryAfter in the API response).

  8. MustHas in detectEscalation panics on error (renderer.go:222-250)

  if page.MustHas(sel) {

  MustHas panics if the selector query fails (e.g., page already closed, context cancelled). Use page.Has(sel) and handle the error — a
   panic in a goroutine serving HTTP requests crashes the process.

  9. No connection pooling / retry for Tier 4 Browserless

  The architecture doc describes Tier 4 as a remote Docker container via WebSocket CDP, but there's no reconnection logic. If the
  Browserless container restarts, the WebSocket dies and all subsequent requests fail until the Go process is restarted. Add a
  health-check or reconnect-on-error wrapper.

  ---
  What You Don't Need

  1. PageTypeLinkRich and PageTypeMixed classifications — These are computed in stage3_hollow.go but never influence any decision.
  decideOutcome only checks hollow.IsHollow. Either use these page types to make smarter escalation decisions or remove the
  classification logic to simplify the code.
  2. adjustedDoc variable (stage3_hollow.go:155-156) — Assigned and immediately discarded with _ = adjustedDoc. Dead code.
  3. CleanText field on AnalysisResult — Only set during LLM Phase C (analyzer.go:103) but never surfaced in the API response or used
  downstream. If it's for debugging, gate it behind a debug flag; otherwise remove it.
  4. Separate Tier3Result / Tier4Result store types — They're identical structs (runner.go:237-268). Use a single RenderTierResult with
   a Tier int field.
  5. Request.Timeout field — Tier 1 already has defaultTimeout, and the runner creates a new scraper for custom timeouts
  (runner.go:78-79). The per-request timeout on the Request struct adds a second override path that complicates reasoning about which
  timeout wins.

  ---
  Optimizations

  ┌───────────────┬────────────────────────────────────────────────────┬───────────────────────────────────────────────────────────┐
  │     Area      │                      Current                       │                        Improvement                        │
  ├───────────────┼────────────────────────────────────────────────────┼───────────────────────────────────────────────────────────┤
  │ Tier 1 body   │ io.ReadAll(io.LimitReader(resp.Body, 10MB)) always │ Pre-allocate based on Content-Length header when          │
  │ read          │  allocates up to 10 MB                             │ available: make([]byte, 0, contentLength)                 │
  ├───────────────┼────────────────────────────────────────────────────┼───────────────────────────────────────────────────────────┤
  │ Tier 2        │                                                    │ Use errgroup with a semaphore; for pages where Phase A    │
  │ extractor     │ 10 goroutines spawned per request even for simple  │ finds data and it's hollow, you already skip Phase B —    │
  │ goroutines    │ pages                                              │ extend this: if JSON-LD alone fills all fields, skip      │
  │               │                                                    │ remaining Phase A extractors too                          │
  ├───────────────┼────────────────────────────────────────────────────┼───────────────────────────────────────────────────────────┤
  │ Tier 3 pool   │ Pages are navigated to about:blank on return       │ Also clear cookies and local storage — stale session      │
  │ cleanup       │ (renderer.go:121)                                  │ state from one domain can leak into the next request      │
  ├───────────────┼────────────────────────────────────────────────────┼───────────────────────────────────────────────────────────┤
  │ Runner store  │ Every tier result is saved synchronously           │ Fire store writes in a goroutine — they shouldn't block   │
  │ writes        │ (runner.go:87,101,115)                             │ the response path. The _ = r.store.Save... already        │
  │               │                                                    │ ignores errors                                            │
  ├───────────────┼────────────────────────────────────────────────────┼───────────────────────────────────────────────────────────┤
  │ Hollow        │ Runs classifyPage twice — once in detectHollow and │ Call it once, derive both hollowResult and                │
  │ detection     │  once in classifyPageWithHints (analyzer.go:81-82) │ PageClassification from the same score                    │
  ├───────────────┼────────────────────────────────────────────────────┼───────────────────────────────────────────────────────────┤
  │ HTTP          │ MaxIdleConnsPerHost: 10 but no MaxIdleConns set    │ Set MaxIdleConns (default is 100 in stdlib, but being     │
  │ transport     │ (scraper.go:35)                                    │ explicit avoids surprises at scale)  