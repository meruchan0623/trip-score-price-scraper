# Hermes Multi-Market Multi-Sheet Design

**Date:** 2026-08-06

**Repository:** `/Users/example/Develop/trip-score-price-scraper`

**Status:** Approved conversational design; pending written-spec review

## Goal

Extend manual Trip price scraping so an explicit multi-market or multi-currency request can run market jobs in parallel and deliver one Go-native XLSX containing one worksheet per successful market context. Preserve the existing HK/HKD default, existing single-market CLI overrides, headed/normal quality gates, returned-market validation, and Hermes attachment-only response contract.

## Non-Goals

- Do not bind every currency permanently to one market.
- Do not remove the existing `--site`, `--locale`, or `--currency` single-market overrides.
- Do not infer market intent merely from the host or query of an input URL.
- Do not generate price summaries, comparisons, minimum prices, SKU counts, or Agent-authored workbooks.
- Do not modify deployment, cron, remote hosts, credentials, browser storage, or authenticated business flows.
- Do not change vendor Excel batch output or comparison-workbook semantics unless a directly shared exporter contract requires a compatible internal change.

## Invariants

1. With no explicit market or currency, manual scraping remains `hk / hk.trip.com / zh-HK / HKD`.
2. A single-market request produces the existing native workbook with exactly one `Trip商品数据` worksheet.
3. A multi-market request produces exactly one native XLSX with one data worksheet per successful market context.
4. Every market job retains the current returned host/site/locale/currency hard gate and price-quality gates.
5. Browser profiles, sessions, captured API responses, rows, and runtime directories are isolated between market jobs.
6. Worksheet order follows request order, never completion order.
7. Hermes still sends no product, price, SKU, rating, booking, promotion, or comparison prose.
8. Public probes are not authenticated business acceptance.

## Canonical Default Mapping

The Go market registry remains the only program-authoritative source for site defaults. Documentation and prompt aliases must be tested against it rather than maintaining a second independent table.

| Site | Site ID | Host | Default locale | Default currency |
|---|---|---|---|---|
| global | EN | www.trip.com | en-XX | USD |
| hk | HK | hk.trip.com | zh-HK | HKD |
| sg | SG | sg.trip.com | en-SG | SGD |
| us | US | us.trip.com | en-US | USD |
| tw | TW | tw.trip.com | zh-TW | TWD |
| jp | JP | jp.trip.com | ja-JP | JPY |
| kr | KR | kr.trip.com | ko-KR | KRW |
| th | TH | th.trip.com | th-TH | THB |
| vn | VN | vn.trip.com | vi-VN | VND |
| id | ID | id.trip.com | id-ID | IDR |
| my | MY | my.trip.com | ms-MY | MYR |
| au | AU | au.trip.com | en-AU | AUD |
| uk | GB | uk.trip.com | en-GB | GBP |
| de | DE | de.trip.com | de-DE | EUR |
| fr | FR | fr.trip.com | fr-FR | EUR |
| es | ES | es.trip.com | es-ES | EUR |
| it | IT | it.trip.com | it-IT | EUR |
| nl | NL | nl.trip.com | nl-NL | EUR |
| pt | PT | pt.trip.com | pt-PT | EUR |
| br | BR | br.trip.com | pt-BR | BRL |
| ca | CA | ca.trip.com | en-CA | CAD |
| ae | AE | ae.trip.com | en-AE | AED |
| sa | SA | sa.trip.com | ar-SA | SAR |
| in | IN | in.trip.com | en-IN | INR |
| ph | PH | ph.trip.com | en-PH | PHP |
| ua | UA | ua.trip.com | uk-UA | UAH |
| dk | DK | dk.trip.com | da-DK | DKK |
| pl | PL | pl.trip.com | pl-PL | PLN |
| fi | FI | fi.trip.com | fi-FI | EUR |
| se | SE | se.trip.com | sv-SE | SEK |
| tr | TR | tr.trip.com | tr-TR | TRY |
| gr | GR | gr.trip.com | el-GR | EUR |
| ru | RU | ru.trip.com | ru-RU | RUB |

When a natural-language request names only a currency, Hermes uses a documented recommendation index to choose a default site. This index is a convenience, not a restriction; an explicit site/locale/currency combination remains valid if the current market-context gate confirms it.

| Currency | Recommended default site |
|---|---|
| AED | ae |
| AUD | au |
| BRL | br |
| CAD | ca |
| DKK | dk |
| EUR | de |
| GBP | uk |
| HKD | hk |
| IDR | id |
| INR | in |
| JPY | jp |
| KRW | kr |
| MYR | my |
| PHP | ph |
| PLN | pl |
| RUB | ru |
| SAR | sa |
| SEK | se |
| SGD | sg |
| THB | th |
| TRY | tr |
| TWD | tw |
| UAH | ua |
| USD | us |
| VND | vn |

## CLI Contract

### Existing single-market mode

The existing flags remain valid:

```bash
trip-score-cdp \
  --site sg \
  --locale zh-SG \
  --currency MYR \
  --input "<url>" \
  --output result.xlsx
```

If no site, locale, currency, or market specification is provided, the resolved context remains HK/HKD.

### New multi-market mode

Add repeatable `--market` and bounded `--market-workers` flags:

```bash
trip-score-cdp \
  --market hk \
  --market jp \
  --market "sg:zh-SG:MYR" \
  --market-workers 2 \
  --input "<url>" \
  --output multi-market.xlsx
```

Accepted market specifications are:

```text
<site>
<site>:<locale>
<site>:<locale>:<currency>
```

Resolution examples:

```text
jp              -> jp / ja-JP / JPY
sg:zh-SG        -> sg / zh-SG / SGD
sg:zh-SG:MYR    -> sg / zh-SG / MYR
fr:fr-FR:USD    -> fr / fr-FR / USD
```

Rules:

- `--market-workers` accepts `1..3` and defaults to `2`.
- `--market` cannot be mixed with top-level `--site`, `--locale`, or `--currency`; mixing fails before browser startup with exit code `2`.
- Exact duplicate resolved `MarketContext` values are deduplicated while preserving first occurrence.
- Same site with a different locale or currency remains a separate market job.
- Invalid site, unadvertised explicit locale, or unsupported currency fails argument parsing before browser startup.
- Multi-market mode requires a single `--output` XLSX path or a deterministic native output path under `--output-dir`.

## Runtime Architecture

Introduce a multi-market orchestration layer around the existing single-market scraper rather than replacing its internal extraction logic.

```go
type MarketJob struct {
    Index     int
    Market    MarketContext
    Inputs    []ScrapeItem
    SheetName string
}

type MarketJobResult struct {
    Job          MarketJob
    Rows         []map[string]string
    Success      bool
    FailureClass string
    Error        string
}
```

Each job reuses current single-market navigation, returned-context validation, API/DOM extraction, currency rejection, risk classification, and trusted-price validation.

Isolation requirements:

- One runtime directory per market index.
- One user-data/profile directory per market.
- One session path per market; no cross-market copy or reuse.
- One browser process and captured-response buffer per market.
- One row accumulator and price-quality tracker per market.
- `--fresh-profile` creates a distinct empty profile for every market job.
- Context cancellation stops all jobs through existing bounded shutdown behavior.

Concurrency uses a semaphore capped by `--market-workers`. A failed market job does not cancel unrelated jobs. Results are stored by input index so worksheet order remains deterministic.

## Native Multi-Sheet Export

Keep the current single-market exporter and workbook shape unchanged. Add a native multi-market exporter that accepts ordered successful `MarketJobResult` values and writes one XLSX.

Worksheet naming:

```text
<SITE>-<CURRENCY>
```

Examples:

```text
HK-HKD
JP-JPY
SG-MYR
```

If two jobs share site and currency but differ by locale, append locale:

```text
SG-MYR-zh-SG
SG-MYR-en-SG
```

Names must:

- remove Excel-forbidden characters;
- be at most 31 characters;
- be unique case-insensitively;
- preserve request order;
- receive `-2`, `-3`, and later suffixes only after semantic names still collide.

Every market worksheet uses the existing native `Trip商品数据` headers, SKU-depth expansion, column order, row values, quality labels, and optional-note rules. Do not add summary sheets, comparison sheets, hidden data sheets, or Agent-authored metadata sheets.

## Partial Failure Semantics

### All markets succeed

- Export all expected worksheets.
- Exit code `0`.
- `run_done.success=true`.
- `partial_success=false`.
- Hermes emits only the bare absolute workbook path.

### Some markets succeed

- Export only successful market worksheets.
- Do not create empty or synthetic failure worksheets.
- Exit code `4`.
- `run_done.success=false`.
- `partial_success=true`.
- Hermes may emit one non-price warning line naming failed market labels, followed by the bare workbook path.

Example:

```text
抓取重要警告：SG-MYR 抓取失败，附件仅包含成功市场
/absolute/path/to/trip_scoreandprice_results_<timestamp>.xlsx
```

The warning must not include product names, prices, SKU counts, ratings, bookings, promotions, or comparisons.

### All markets fail

- Do not create an empty workbook.
- Exit code `4`.
- Hermes emits exactly `抓取失败：未生成原生Excel（<run-dir>）`.

### Invalid arguments

- Fail before browser startup.
- Exit code `2`.
- Do not create a workbook.

## Structured Events

Add or extend:

```text
multi_market_started
market_job_started
market_job_done
market_job_failed
multi_market_export_done
run_done
```

Public market fields are limited to:

```text
market_code
market_site_id
market_host
market_locale
market_currency
sheet_name
rows
trusted_rows
untrusted_rows
success
failure_class
```

Final `run_done` adds:

```text
market_count
successful_market_count
failed_market_count
partial_success
market_results
```

Events must not expose cookies, headers, storage, session contents, tokens, credentials, user identifiers, or full environment values.

## Hermes Prompt Contract

### Default request

```text
【请抓取产品价格 45892282】
```

Resolves to HK/HKD and keeps the existing single-sheet attachment-only workflow.

### Explicit market

```text
请抓取产品 45892282 的日本市场价格
```

Resolves to JP/JPY and remains a single-market workbook with `Trip商品数据`.

### Currency only

```text
请抓取产品 45892282 的 JPY 价格
```

Uses the recommended currency index and resolves to JP/JPY.

### Explicit override

```text
请抓取产品 45892282 的新加坡站 MYR 价格
```

Resolves to `sg / en-SG / MYR`. Explicit combinations are not restricted by the currency recommendation index, but the returned-context gate must confirm them before any trusted price is accepted.

### Multiple markets or currencies

```text
请抓取产品 45892282 的 HKD、JPY、USD 价格
```

Resolves to ordered jobs `hk`, `jp`, and `us`, with worksheets `HK-HKD`, `JP-JPY`, and `US-USD`.

The input URL host never implies market intent. Ambiguous phrases such as “美元区”, “欧洲价格”, “几个主要市场”, or alternatives without one deterministic interpretation require user clarification before browser startup.

## Hermes Delivery Guard

The current manual guard must evolve without weakening the default HK behavior.

- No market/currency intent: require the existing HK/HKD context.
- Explicit single market: allow only the resolved single context and require one `Trip商品数据` worksheet.
- Explicit multi-market: require exactly the ordered successful worksheet list represented by the run result.
- Continue accepting one native workbook attachment only.
- Continue removing acknowledgements, progress text, price summaries, tables, comparisons, and output explanations.
- For partial success, permit exactly one short non-price warning line plus one workbook path.

Before delivery, validate the XLSX structurally with Python standard-library ZIP/XML parsing:

- single-market workbook has exactly `Trip商品数据`;
- multi-market workbook has only the expected ordered market sheets;
- every sheet has native headers and at least one data row;
- currency column contains only the sheet's expected ISO currency;
- canonical URL column matches expected host/locale/currency;
- price trust labels contain only `trusted_base` or `trusted_promo`;
- no `currency_mismatch`, risk-blocked, untrusted, unexpected, empty, summary, or rebuilt-price-list sheet is deliverable.

Workbook contents may be read only for structural delivery validation, never to compose user-visible product or price prose.

## Documentation Entrypoints

Update:

- `HERMES.md` with default HK behavior, explicit single-market routing, multi-market trigger, and one-XLSX/multiple-worksheet delivery.
- `README.md` with the canonical site mapping, currency recommendation index, CLI examples, worksheet naming, and partial-failure contract.
- `hermes-agent/skills/trip-sku-price-scraper-operations/SKILL.md` with prompt parsing, command construction, validation, and delivery rules.
- `docs/trip-things-to-do-market-compatibility.md` with the stable default mapping and distinction between defaults, recommendations, overrides, and verified public probes.

## Testing Strategy

### CLI and parsing

- Existing no-argument/default market remains HK/HKD.
- One and multiple `--market` flags resolve correctly.
- Market-spec overrides resolve correctly.
- Exact duplicates deduplicate in first-occurrence order.
- Multi-market and top-level single-market flags cannot mix.
- Invalid site, locale, currency, and market-worker bounds fail before browser startup.

### Concurrency and isolation

- Maximum active jobs never exceeds `--market-workers`.
- Every market uses unique profile, session, and runtime paths.
- Completion order cannot change worksheet order.
- One failed market cannot cancel successful peers.
- Parent cancellation stops all jobs with bounded cleanup.
- Rows, captured API responses, and quality state never cross market boundaries.

### Export

- Single market retains exactly one `Trip商品数据` sheet.
- Multi-market produces one file with ordered market sheets.
- Each sheet retains native headers and field order.
- Same-site/different-currency contexts remain separate.
- Names sanitize, truncate, deduplicate, and remain deterministic.
- Partial success exports successful sheets only.
- Total failure produces no workbook.

### Hermes guard

- Existing default HK prompt and URL-host non-inference remain protected.
- Explicit JP/JPY, explicit SG/MYR, and multi-market prompts parse deterministically.
- Stale workbook, missing/extra/reordered sheets, wrong currencies, wrong canonical URLs, untrusted rows, summaries, and multi-attachment output are rejected.
- Partial success allows only one warning plus path.
- No user-visible price or product prose passes the guard.

### Final verification

```bash
go test ./... -count=1
go vet ./...
go build -o /tmp/trip-score-cdp-multi-market ./cmd/trip-score-cdp
python3 -m unittest tests/test_trip_manual_delivery_guard.py
```

Run a bounded visible public probe for product `45892282` with HK/HKD and JP/JPY in parallel. Acceptance requires isolated profiles, one XLSX, ordered worksheets `HK-HKD` then `JP-JPY`, trusted rows with only expected currencies, matching canonical URLs, `run_done.success=true`, and no market-context, currency, price-quality, or risk failure. This is public-page acceptance only; authenticated business acceptance remains open.

## Rollout Boundary

This design authorizes local implementation and bounded public verification after the written spec and implementation plan are approved. It does not authorize deployment, cron modification, remote writes, credential/session extraction, or authenticated business acceptance claims.
