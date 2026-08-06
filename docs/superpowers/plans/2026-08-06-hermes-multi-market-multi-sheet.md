# Hermes Multi-Market Multi-Sheet Implementation Plan

> **For agentic workers:** REQUIRED EXECUTION MODE: the primary Codex agent uses Codex-native subagents task-by-task, reviews every returned diff, and runs the stated verification before accepting a task. Repository instructions prohibit `superpowers:subagent-driven-development` and `superpowers:executing-plans`.

**Goal:** Preserve HK/HKD defaults and explicit single-market overrides while adding isolated parallel multi-market scraping into one native XLSX with one worksheet per successful market.

**Architecture:** Parse repeatable `--market` values into ordered `MarketContext` jobs, run them through the existing single-market extraction logic with isolated browser state, and export successful results once in request order. Keep the single-market workbook and Hermes attachment-only contract unchanged.

**Tech Stack:** Go 1.24, Rod/CDP, excelize, Python standard-library ZIP/XML parsing, Go tests, Python unittest.

## Global Constraints

- Default remains `hk / hk.trip.com / zh-HK / HKD`.
- Preserve `--site`, `--locale`, and `--currency`; currency recommendations are defaults, not hard bindings.
- `--market` accepts `<site>`, `<site>:<locale>`, or `<site>:<locale>:<currency>` and cannot mix with the single-market flags.
- `--market-workers` is `1..3`, default `2`.
- Multi-market is direct price-input only; reject vendor Excel, comparison, rating-only, and mode2 combinations before browser startup.
- Single-market output remains one `Trip商品数据` worksheet. Multi-market output is one XLSX with ordered market worksheets.
- Each market has a distinct writable runtime directory, session, profile, browser, response buffer, rows, and quality state. Multi-market runs never consolidate market state.
- Preserve returned host/site/locale/currency and trusted-price gates.
- Hermes returns only a workbook path, or one non-price partial-failure warning followed by the path.
- No deployment, cron, credentials, browser-state extraction, or authenticated-business acceptance.

## Tasks and Commit Boundaries

### Task 1: CLI and Request Contract

- Add `TaskRequest.Markets`, `TaskRequest.MarketWorkers`, `MarketJob`, `MarketJobResult`, and multi-market result counters.
- Add tested `parseMarketSpec`, ordered exact-context deduplication, repeatable `--market`, and bounded `--market-workers`.
- Reject all documented conflicting modes with exit code 2 before browser startup.
- Preserve current single-market parsing and output behavior.
- Commit: `feat: add multi-market CLI contract`.

### Task 2: Native Multi-Sheet Export

- Refactor the single-sheet exporter into shared field and sheet writers without changing `exportRows`.
- Add deterministic worksheet naming, Excel sanitization, 31-character truncation, case-insensitive deduplication, and request-order output.
- Add `exportMarketRows` that exports successful non-empty results only and refuses an empty workbook.
- Commit: `feat: export markets to separate worksheets`.

### Task 3: Isolated Parallel Orchestration

- Extract the context-aware single-market scraping core from signal, final export, and state-consolidation ownership.
- Add a semaphore-bounded multi-market orchestrator with preallocated ordered results, one outer signal handler, per-market runtime paths, partial-success semantics, and structured events.
- Keep old single-market export, signal, quality-summary, and state-consolidation behavior unchanged.
- Verify concurrency, cancellation, isolation, result ordering, partial success, and events with injected non-browser tests and the race detector.
- Commit: `feat: orchestrate isolated multi-market scrapes`.

### Task 4: Hermes Intent and Delivery Guard

- Parse default, explicit site, currency-only, explicit override, and ordered multi-market prompts; reject ambiguous prompts before command construction.
- Use existing single-market flags for one resolved context and repeatable `--market` for two or more contexts.
- Validate native XLSX structure with `zipfile` and `xml.etree.ElementTree`: sheet set/order, headers, rows, currency, canonical URL context, and trusted labels.
- Preserve stale-file rejection and removal of all product/price/SKU/summary prose.
- Commit: `feat: guard Hermes multi-market workbooks`.

### Task 5: Documentation Entrypoints

- Update `HERMES.md`, `README.md`, the Hermes operations skill, and the compatibility record.
- Document the canonical site defaults, currency recommendation index, explicit override, ambiguity behavior, CLI examples, worksheets, and partial failure.
- Commit: `docs: document Hermes multi-market delivery`.

### Task 6: Integration, Public Probe, and Adversarial Review

- Run `go test ./... -count=1`, focused race tests, `go vet ./...`, build, Python unittest, compileall, and diff checks.
- Verify conflict invocations return exit code 2 without browser startup or output.
- Run one bounded headed/normal/fresh-profile HK+JP public probe for product `45892282`; verify one XLSX, ordered `HK-HKD` and `JP-JPY` sheets, isolated runtime state, matching currencies/canonical URLs, and trusted rows.
- Perform independent read-only adversarial review of CLI bypasses, result ordering, cancellation, partial success, state isolation, sheet collisions, Hermes workbook validation, response leakage, and documentation drift.
- Fix only reproduced blockers with regression tests, then rerun the full gate.
- Push once with `git push -u origin codex/hermes-multi-market-multi-sheet`.

## Verification Commands

```bash
go test ./... -count=1
go test -race ./internal/tripcdp -run 'TestMultiMarket' -count=1
go vet ./...
go build -o /tmp/trip-score-cdp-multi-market ./cmd/trip-score-cdp
python3 -m unittest -v tests.test_trip_manual_delivery_guard
python3 -m compileall -q hermes-agent/plugins/trip-manual-delivery-guard tests
git diff --check
```

## Completion Claim

Report implementation, automated verification, public-page acceptance, pushed branch, deployment status, and authenticated-business acceptance as separate states.
