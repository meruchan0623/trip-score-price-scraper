---
name: trip-sku-price-scraper-operations
description: "Use when running, resuming, stopping, validating, diagnosing, or packaging Trip.com SKU, price, rating, or vendor scraping on the Hermes Agent host."
---

# Trip SKU Price Scraper Operations

Use this skill when the user asks to run, resume, stop, benchmark, merge, validate, or package Trip.com SKU/price/vendor scraping with the Go CLI `trip-score-cdp` in `/Users/example/Develop/trip-score-price-scraper`.

This is a different workflow from Trip outerlist scraping. It produces vendor SKU/price XLSX files under `/Users/example/Documents/trip-sku-price-scrape/YYMMDD-HHMM/` and uses the Go binary `/tmp/trip-score-cdp`.

## Repository entrypoint policy

As of 2026-06-02, this project is **Go CDP-only**. The only supported action component and entry point is:

```bash
go run ./cmd/trip-score-cdp --help
# or
go build -o /tmp/trip-score-cdp ./cmd/trip-score-cdp
```

Hermes Remote Debugging allocation: this daily SKU/price cron does not reserve a fixed shared `--remote-debugging-port` such as `9222`, `9223`, or `9224`. Keep those ports for signed-in Ctrip/Tripcom browser sessions; this workflow should continue using its own Go CDP browser management and run-directory artifacts.

Do not restore or recommend the removed Python CLI, Python GUI, Selenium/undetected-chromedriver workflow, or PyInstaller spec as project entry points. This project-local file is the only authoritative Hermes operations skill. The Hermes host must discover it through `skills.external_dirs`; do not copy it into or load an older version from `~/.hermes/skills/`, because a same-name local skill shadows the project version. Keep README focused on Go CDP-only usage.

## Canonical paths

- Repo: `/Users/example/Develop/trip-score-price-scraper`
- Binary: `/tmp/trip-score-cdp`
- **Authoritative daily scrape target table** (ID form, worksheet `Sheet1`, columns = vendors `TSIM 網址/BC/TGT/FR/JY/其他`): `/Users/example/Develop/trip-score-price-scraper/docs/日常抓取目标.xlsx`. When building URL lists from it, follow the「URL 拼接约定」below.
- Output root: `/Users/example/Documents/trip-sku-price-scrape`
- Each run must write into a timestamped run folder under the output root, named with local time `YYMMDD-HHMM`, for example:
  - `/Users/example/Documents/trip-sku-price-scrape/260529-1841/`
- Each run folder must include a short Markdown description file, e.g. `任务说明.md`, containing a few sentences or bullet lines describing:
  - task purpose/scope
  - input workbook or URL source
  - vendors included
  - CLI mode / delay profile / worker count
  - whether this is a fresh run, resume, benchmark, merge, or scheduled run
- Typical state file: `/Users/example/Documents/trip-sku-price-scrape/current_state.json` or a run-local `current_state.json`
- Legacy run folders may still exist under `/Users/example/Documents/trip_vendor_results`; treat them as historical inputs only, and place new outputs under `/Users/example/Documents/trip-sku-price-scrape/` unless the user explicitly says otherwise.

## URL 拼接约定（重要）

When constructing scrape URLs from product IDs / a target table (`docs/日常抓取目标.xlsx` or ID-only prompts), **default to only `https://{site}.trip.com/things-to-do/detail/<product-id>/` — do NOT append query parameters** such as `?locale=zh-HK&curr=HKD`.

- Append `?locale=...&curr=...` (or pass `--site`/`--locale`/`--currency`) **only when the user explicitly requests a specific currency / site / language**.
- Rationale: the CLI defaults to `hk` and normalizes the real navigation via its returned-context gate (host/site/locale/currency). Extra default query params on the URL are unnecessary and can diverge from the actual request. Let the CLI normalize; do not hard-code `zh-HK`/`HKD` unless asked.

## Standard command shape

Build and verify the CLI:

```bash
cd /Users/example/Develop/trip-score-price-scraper
go build -o /tmp/trip-score-cdp ./cmd/trip-score-cdp
/tmp/trip-score-cdp --help >/tmp/trip-score-cdp-help.txt
```

Run vendor Excel mode:

```bash
RUN_DIR="/Users/example/Documents/trip-sku-price-scrape/$(date +%y%m%d-%H%M)"
mkdir -p "$RUN_DIR"
cat > "$RUN_DIR/任务说明.md" <<'MD'
# Trip SKU Price Scrape Run

- 任务：抓取 Trip.com SKU/价格/厂商结果。
- 输入：INPUT.xlsx
- 参数：mode1，有头浏览器，fast 延迟，timeout=1800，workers=2。
- 说明：按厂商列并发抓取，输出 XLSX 到本运行目录；cron 场景默认使用 fast + workers=2。
MD

/tmp/trip-score-cdp \
  --run-mode mode1 \
  --delay-profile fast \
  --timeout 1800 \
  --excel-input INPUT.xlsx \
  --workers 2 \
  --log-level verbose \
  --output-dir "$RUN_DIR"
```

For a partial resume with only TSIM/FR/JY, use `--workers 2` when the resume input has only those three vendor columns. Resume run folders should still use the same timestamped `/Users/example/Documents/trip-sku-price-scrape/YYMMDD-HHMM/` convention and include `任务说明.md` describing the previous run, resume boundary, and pending vendor counts.

## Input table pattern (Excel vendor mode)

Excel vendor mode expects:

- Active sheet only.
- Row 1 contains vendor/group names.
- Row 2 onward contains Trip product URLs.
- Cells are treated as URLs only if they contain both `http` and `trip.com`.

For target tables with metadata columns such as `TSIM ID`, `分類`, `目的地`, keep them in the workbook for traceability, but note that the Go CLI will only scrape columns containing Trip URLs. If the TSim URL column is named `TSIM 網址`, it is treated as the TSim vendor/group column.

Current session-specific target details are in `references/tsim-competitor-target.md`.

## Smoke test before full run

When validating a new target workbook or changed workflow, run a bounded per-vendor subset first. Use the row count the user asks for (e.g. first 5 or first 10 URLs per vendor):

1. Generate a temporary workbook with columns `TSIM`, `BC`, `TGT`, `FR`, `JY`, `其他`.
2. Put at most the requested first `N` Trip URLs under each vendor.
3. Run the same Go CLI command with `--workers 2 --delay-profile fast --timeout 1800` for cron-default smoke tests. If testing legacy speed/stress behavior, explicitly label any non-default profile such as `normal`, `safe`, or `slow` in the run folder notes.
4. Verify exit code `0`, all URL counts completed, and price records appear in output XLSX or terminal `combination price` lines.
5. If the user asks to delete a previous smoke run before re-running, delete only the exact smoke run directories involved; never delete `_state/`.

Reference examples:
- `references/2026-05-29-smoke-test.md` — first-5 headed/initial smoke and log reconstruction issue.
- `references/2026-05-29-headless-first10.md` — first-10 headless smoke with minute-by-minute progress pattern.

## Manual single or selected products price scrape

Use this workflow whenever the user supplies, names, or asks Hermes to resolve one product or a small set of specific products. This includes requests such as “抓这个链接”, “抓取 BC 日本的产品价格”, or “只抓这几个指定产品”.

### Hard trigger and user-visible output contract

Treat these operations prompts as direct triggers for this workflow:

```text
【請抓取產品價格71827107】
請抓取產品價格71827107
请抓取产品价格71827107
```

Extract the decimal product ID following `產品價格` / `产品价格`. If the prompt contains several IDs separated by `、`, comma, or whitespace, preserve their order and run them together. For an ID-only prompt, construct the input URL **following the repo's「URL 拼接约定」：默认只拼 host 与 `/detail/<product-id>/`，不追加 `?locale=...&curr=...` 查询参数；只有当用户显式传入需要抓取的货币/站点/语言时，才追加对应参数**。默认构造形如:

```text
https://hk.trip.com/things-to-do/detail/<product-id>/
```

The valid success response contains only the native workbook's bare absolute path:

```text
/Users/example/Documents/trip-sku-price-scrape/manual-<ids>/trip_scoreandprice_results_<YYMMDD-HHMM>.xlsx
```

Hermes Gateway converts the bare path into a native attachment and removes the path from visible chat text. Before the path, send no acknowledgement, progress message, product title, price, SKU count, minimum/maximum price, Markdown table, plain-text list, run summary, or output-path explanation. After it, send nothing else. Tool/runtime logs remain internal and are not copied into the user-facing response.

The project plugin `hermes-agent/plugins/trip-manual-delivery-guard/` enforces this contract with `pre_llm_call` and `transform_llm_output`. It removes any Agent-generated summary immediately before delivery. Do not bypass, disable, or work around the guard. Workbook reads are permitted only for the structural validation required below; never inspect the workbook to compose product, price, rating, booking, promotion, SKU-count, minimum-price, or comparison prose.

The delivered file is always the Go CLI native workbook. Never create or attach a secondary workbook named like `product_<id>_pricelist_HKD.xlsx`. A workbook with a `價格清單` sheet, top summary fields such as `產品名稱 / 產品ID / 幣種 / 最低價 / SKU總數`, or a rebuilt `套餐名稱 / 實價 / 原價 / resourceId` table is an invalid Agent-generated deliverable.

For this manual workflow, every supplied Trip.com URL is a product identifier only. Before navigation, the Go CLI forcibly rewrites any Trip.com host and query to `https://hk.trip.com/...?...&locale=zh-HK&curr=HKD`. The native workbook must therefore record the canonical Hong Kong URL and HKD prices only; do not follow, preserve, compare, or export `CNY` / RMB prices even if the user supplied a mainland, Singapore, or another Trip.com URL. If a page/API still returns a non-HKD price, the CLI clears that row's price fields, marks it `currency_mismatch`, and the manual run must not be delivered as a success.

1. Treat the request as a direct-input Go CDP SKU-price scrape, not an Excel-vendor batch and not the Trip outerlist scraper.
2. Resolve every requested product to its exact Trip.com detail URL. Pass one `-input` argument per product, preserving the user's order. Do not generate an input workbook or URL-list file for this workflow.
3. Use an explicit manual output directory under `/Users/example/Documents/trip-sku-price-scrape/`. Name it `manual-<product-id>` for one product. For several products, join all requested IDs in order with `-`, producing `manual-<id-1>-<id-2>-...`.
4. From the repository root, build the project-local manual binary before the run:

```bash
go build -o ./trip-score-price-scraper ./cmd/trip-score-cdp
```

5. Run with this fixed parameter contract. Repeat only the `-input` line when several products are requested:

```bash
./trip-score-price-scraper \
  -delay-profile fast \
  -input "https://hk.trip.com/things-to-do/detail/109223304/" \
  -output-dir /Users/example/Documents/trip-sku-price-scrape/manual-109223304 \
  -product-info \
  -no-quality-rerun
```

For several selected products, the command shape is:

```bash
./trip-score-price-scraper \
  -delay-profile fast \
  -input "<product-url-1>" \
  -input "<product-url-2>" \
  -output-dir /Users/example/Documents/trip-sku-price-scrape/manual-<id-1>-<id-2> \
  -product-info \
  -no-quality-rerun
```

Do not replace these manual-task defaults with `-headless`, another delay profile, `-excel-input`, `-urls-file`, `-output`, or quality-rerun flags. Run selected products sequentially with the CLI's single worker. Batch and scheduled workflows keep their separate parameters elsewhere in this skill.

6. Verify the final `run_done` event and the generated workbook at `<output-dir>/trip_scoreandprice_results_<YYMMDD-HHMM>.xlsx`. Success requires `success=true`, exit code `0`, and at least one captured row for every requested product.
7. Verify that the workbook has exactly one sheet named `Trip商品数据` and that its header follows the native field contract below. Every product must have a product name, at least one SKU level, currency, a non-empty display/real price, and only `trusted_base` or `trusted_promo` price statuses. Any `risk_blocked`, Cloudflare/Whaleguard note, unknown/untrusted status, missing required field, or visibly incomplete product makes the run unsuccessful even if the workbook exists.
8. On valid success, emit only the bare absolute native workbook path specified above. The delivery guard performs the final allowlist check.

### Native manual-product workbook contract

The delivered workbook is the unmodified Go CLI output for all requested products. It has exactly one worksheet named `Trip商品数据`. Keep the native row values and native column order; do not fill down, delete, rename, reorder, or summarize fields.

The native columns are:

```text
商品ID, URL, 商品名称, SKU层级1...N, 货币, 外显价格, 实价, 促销标签,
[价格可信标签 when present], 商品资讯, 总促销标签,
预定数, 评价数, 评分, 客户服务, 方便程度, 讯号状态, 中差评价数,
[抓取备注 when present]
```

`SKU层级1...N` expands to the actual maximum SKU depth. `价格可信标签` appears only when the native export has price-quality data; internal diagnostic fields `资源ID`, `接口ItemID`, `促销命中`, 原始捕获价格 and `价格来源` are not exported. `总促销标签` follows `商品资讯`; `抓取备注` appears only when at least one row has a note.

**SKU identity contract (2026-08 verified):** every exported SKU row—including continuation rows after the first SKU of a product—must retain non-empty `厂商` (when available), `商品ID`, `URL`, and `商品名称`. Only optional repeated product-display fields may be blanked. Never accept or deliver an XLSX with SKU rows lacking product identity; validate this invariant after a compatibility repair or a new page-structure rollout.

The following are not manual-product deliverables: one workbook per requested URL, `product_<id>_pricelist_HKD.xlsx`, a `價格清單` sheet, a two/four-column rebuilt price list, a cleaned workbook, or a workbook with Agent-created `摘要` / `SKU价格明细` sheets.

Validate the workbook with normal `openpyxl.load_workbook(path)` mode before delivery. If the native workbook exists but has no data rows, missing required columns, an unexpected sheet, or other anomalies, the response may contain exactly two lines: `抓取结果异常：<short reason>` followed by the bare absolute native workbook path. If the scrape encountered partial SKU-level click failures, anti-bot/API blocking, timeout/interruption, or partial product failures, use one `抓取重要警告：<short reason>` line followed by the bare path. Never include captured prices, product names, ratings, bookings, promotions, SKU counts, minimum prices, or comparisons. If the native workbook does not exist, report exactly `抓取失败：未生成原生Excel（<run-dir>）`; do not create a replacement workbook from terminal or JSONL logs.

## Ad-hoc multi-destination price scrape

When the user asks to scrape multiple specific destinations (e.g. "抓取泰国、高加索三国、亚洲多国") across all vendors from the fixed product table:

1. Read the canonical config workbook (`config/非TSIM厂商价格抓取输入.xlsx`) and filter rows by 目的地 keyword match.
2. Create a filtered input workbook with only matching rows (copy headers + matched rows, preserve all vendor columns).
3. Launch with default parameters (`mode1 + headed + normal + timeout=1800 + workers=2 + compare-vendor`).
4. For small runs (<50 URLs), use `nohup` background process directly; no launcher/watcher architecture needed.

Reference: `references/ad-hoc-multi-destination-sku-scrape-2026-06-17.md`.

## Stop without losing results

When the user asks to stop an active run but preserve already scraped rows:

1. Locate the run folder and runner PID from `runner.pid` or `/Users/example/Documents/trip-sku-price-scrape/current_state.json`.
2. Identify only processes for that run directory matching `run_daily_sku_vendor_trend.sh`, `/tmp/trip-score-cdp`, launcher/finalizer/final-delivery helpers.
3. Send `SIGTERM` to the Go child and wrapper PID.
4. Wait a short grace period so the CLI can export partial vendor XLSX files; use `SIGKILL` only if run-specific processes linger.
5. Confirm no matching processes remain and inspect the run directory for partial outputs.
6. If the run-local/global state still says `running`, update both `current_state.json` files to `status=stopped_by_user`, add `stopped_at` and `stop_reason`, and append a `STOPPED_BY_USER <timestamp>` marker to `terminal_output.txt`.

Do **not** delete the run folder. Do **not** clean worker profiles before you have confirmed partial XLSX files exist. See `references/jsonl-failure-diagnosis-and-stop-2026-06-18.md` for the JSONL-era stop and failure-report workflow.

## Resume from unfinished vendor positions

To continue from where a stopped full run left off:

1. Parse the original input Excel:
   - Row 1 contains vendor names.
   - Row 2 onward contains Trip URLs.
2. Parse the previous `terminal_output.txt` for lines like:

```text
[TSIM] url done 55/104 rows=4 elapsed=15.8s url=https://...
```

3. For each vendor being resumed, use the largest completed index as the slice boundary:
   - completed `55/104` means the next input row is original URL index `56`.
   - Build a new resume input workbook containing only `urls[completed:]` under that vendor column.
4. Save a `resume_plan.json` beside both the old and new run folders with:
   - previous run dir
   - resume run dir
   - resume input path
   - per-vendor total/completed/pending counts
   - first pending URL
5. Launch a new run against the resume input and new output folder.

## Progress updates for long runs

This section applies to long-running batch, resume, and scheduled workflows only. It does not apply to `Manual single or selected products price scrape`, which sends no user-visible acknowledgement, progress, or summary before its native attachment.

The daily launcher script is now long-running and handles progress reporting internally. For ad-hoc batch runs outside the launcher, use the same pattern: keep a monitoring loop that parses `terminal_output.txt` every 20 minutes and prints concise per-vendor progress to stdout.

Report format preference: concise plain text, high information density, minimal verbosity. One header line + one line per vendor. No verbose per-vendor breakdowns with elapsed times, row counts, and trend statistics — those belong in the final report only.

## Benchmark after code changes

If the user asks whether a Go CLI change improved speed:

1. Record old per-vendor average/median URL elapsed time from the original log's `url done ... elapsed=Xs` lines.
2. Let the new run execute for about 5 minutes.
3. Parse new `url done` lines for the same vendors.
4. Report old vs new average/median seconds per URL and percentage improvement:

```text
speedup_pct = (1 - new_avg / old_avg) * 100
```

Store the benchmark as `speed_check_after_5min.json` in the resume run folder.

## Progress and delivery architecture (quick launcher + recurring watcher)

As of 2026-06-03, the daily SKU cron must use a **quick launcher + recurring watcher** architecture. Do **not** make the launcher wait in a long poll loop: Hermes cron has a short per-run execution budget, so a long-running no-agent launcher can successfully start the scrape but still be marked `error` after timing out.

Architecture:
1. Launcher (`trip_sku_non_tsim_daily_launcher.py`) creates a timestamped run folder, builds `/tmp/trip-score-cdp`, writes `current_state.json`, starts `run_daily_non_tsim.sh` as a detached subprocess, prints one startup line, and exits quickly.
2. The detached shell wrapper owns scrape → trend finalizer → state update to `finalized`.
3. After finalization, the detached runner calls `trip_sku_non_tsim_final_delivery.py "$RUN_DIR"` so final report/ZIP delivery happens immediately inside the launcher-owned chain.
4. The final-delivery script sends via `hermes send` to `telegram:YOUR_DATA_GROUP` and `telegram:YOUR_OPERATIONS_GROUP`, retries with backoff, writes `.delivered_daily_trend` on success, and writes `.delivery_pending` / `delivery_error.json` on failure.
5. Watcher cron (`trip_sku_non_tsim_daily_watcher.py`, currently `every 60m` per user request) reads `current_state.json` and `terminal_output.txt` for progress. When a run is `finalized` but not delivered, watcher calls the same final-delivery script as a compensation path (`--max-attempts 1`).
6. If the launcher cron shows `last_status=error`, first inspect the run directory and process state before restarting: the underlying scraper may still be healthy and progressing.
7. Final status checks must inspect run-folder health markers, not just process exit: verify `exit_code.txt`, `current_state.json status=finalized`, ZIP existence, `.delivered_daily_trend`, absence/resolution of `.delivery_pending`, and log counts for `risk challenge detected`, `whaleguard block`, `url failed`, `panic`, `partial export`, and `stopped by signal`.

Progress report format (concise plain text, no verbose multi-line reports):
```
⏳ SKU价格抓取进行中 [YYMMDD-HHMM]
已运行: 40min | URL: 85/455 | 反爬拦截: 12次
  TSIM 網址: 15/128 (11%)
  BC: 30/57 (52%)
  ...
```

Final report format (watcher-generated with trend analysis):

The watcher script (`trip_sku_non_tsim_daily_watcher.py`) generates the final report by analyzing trend XLSX files. It extracts per-product price changes and sales growth, then filters and ranks them. Report structure:

```
📊 SKU非TSIM厂商价格趋势抓取 [YYMMDD-HHMM]

运行信息
- 运行目录：YYMMDD-HHMM
- 对比基准：previous_run_id
- 运行耗时：X分钟
- 单URL平均速度：X.X秒

━━━━ TSIM 網址 ━━━━
实价上调Top5（按SKU平均涨降幅）
（无显著上调）| <product_id> | <name> | <change>
实价下调Top5
...
销量增长Top5（按增长百分比与绝对数50:50权重）
...

━━━━ BC ━━━━
...
(repeat for each vendor: TSIM 網址, BC, TGT, FR, JY, 其他)

━━━━ TSIM 網址 vs 竞对比价 ━━━━
BC | 37地区 1340SKU匹配 | 均价差: +0.85 | 基准更优: 48% | 基准更优
FR | 85地区 1935SKU匹配 | 均价差: +18.24 | 基准更优: 66% | 基准更优
...

MEDIA:/path/to/ZIP
```

The comparison section reads from `vendor_compare_*.json`. All deltas are matched-SKU-only.

**Report filtering thresholds** (user-specified):
- Price increase: only include products where avg SKU price change ≥ **0.2 HKD**
- Price decrease: only include products where avg SKU price change ≤ **-0.1 HKD** (magnitude > 0.1)
- Sales growth: only include products where absolute booking increase > **10**

**Sales growth ranking**: 50% weight on percentage growth + 50% weight on absolute growth. Display shows absolute increase only.

**TSIM inclusion**: TSIM vendor is included in the report AND in the finalize script's `VENDORS` list. TSIM raw XLSX has ~5486 rows of SKU data (same format as other vendors). TSIM now generates a `TSIM 網址_*_含趋势.xlsx` trend file, comparing TSIM prices/bookings against the previous run's TSIM data. The finalize script (`trip_sku_non_tsim_trend_finalize.py`) must include `'TSIM 網址'` in its `VENDORS` list for this to work. On the first run after enabling TSIM, all rows are marked "新产品" (no previous TSIM baseline); subsequent runs will show real trend data.

**Report data source**: The watcher reads trend XLSX files (`*_含趋势.xlsx`) for ALL vendors including TSIM (since TSIM is now in the finalize VENDORS list). It also includes the TSIM raw XLSX as a fallback if no trend file exists. Column mapping (same for all vendors):
- 商品ID: column B (index 1)
- 商品名称: column D (index 3)
- 实价变化: column AA (index 26)
- 预定数变化: column AG (index 32)
- 预定数变化百分比: column AH (index 33)

Do **not** include a "产物清单" section in the report. Do **not** list individual elapsed times, row counts, or trend statistics per vendor in a verbose format — use the scannable Top5 structure above.

The old watcher script is no longer a fallback — it is the primary final report generator. The launcher handles progress monitoring; the watcher handles final trend analysis and report formatting.

If a scheduled delivery accidentally contains only the comparison section, regenerate the plaintext from `趋势对比摘要.json`, per-vendor `*_含趋势.xlsx`, `current_state.json`, and `vendor_compare_*.json` using the standard shape above. Do not redeliver a comparison-only report. See `references/standard-plaintext-report-regeneration-2026-06-16.md`.

### Final plaintext report must use the standard Top5 vendor format

If a daily SKU cron delivers a truncated/plaintext report that only contains the `TSIM 網址 vs 竞对比价` section, rebuild and resend the plaintext from the run folder using the standard format above. The standard report must include every vendor block (`TSIM 網址`, `BC`, `TGT`, `FR`, `JY`, `其他`) with:

1. `实价上调Top5（按SKU平均涨降幅）`
2. `实价下调Top5`
3. `销量增长Top5（按增长百分比与绝对数50:50权重）`
4. Final `TSIM 網址 vs 竞对比价`

Use the trend XLSX files (`*_含趋势.xlsx`) and `vendor_compare_*.json` as the source of truth. Do not summarize only the compare JSON. For sales growth, product-level fields such as `预定数变化` are repeated across SKU rows; aggregate by `商品ID` and use the product-level positive change once (for example max/unique value), not the sum over all SKU rows. Push the corrected plaintext to both `telegram:YOUR_DATA_GROUP` and `telegram:YOUR_OPERATIONS_GROUP`.

## Merge and validate final XLSX outputs

After resume finishes successfully:

1. For resumed vendors, concatenate old partial vendor XLSX + new resumed vendor XLSX, preserving one header row.
2. For vendors already completed before the stop, copy their original XLSX into the final package.
3. Create:
   - one complete XLSX per vendor
   - one all-vendor combined XLSX
   - `完整性校验报告.json`
   - `说明_完整性校验.txt`
   - a final ZIP for delivery
4. Validate URL coverage against the original input workbook:
   - Count input URLs per vendor.
   - Count unique result URLs per vendor.
   - Report missing and extra URL counts.
   - Treat blank URL cells in SKU continuation rows as normal; only the first row for each product carries URL/product metadata.
5. If a vendor has fewer unique result URLs than input count but missing count is zero, explain that the input contained duplicate URLs rather than claiming data loss.

### Merge multiple Codex/manual reruns after quality failures

When a Codex/manual scrape ran several times because some products had `url_quality_failed`, consolidate by product rather than by raw file timestamp:

1. Parse main `run.jsonl`, rerun `rerun.jsonl`, and any single-product rerun JSONL; verify every used run has `run_done.exit_code=0` and no `failed_vendors`.
2. Treat `url_quality_failed` events as the authoritative failed-product set; do not treat `itemId=0` alone as failure.
3. For each failed `(vendor, product_id)`, remove all filled-down rows for that product from the main vendor XLSX and append rows from the latest rerun where the product no longer appears in `url_quality_failed`.
4. Add `合并来源` and `合并说明` columns to merged vendor workbooks.
5. Deliver a ZIP with per-vendor merged XLSX, a summary XLSX/Markdown, and relevant `vendor_compare_*.xlsx` files. See `references/codex-multi-run-sku-merge-2026-06-18.md`.

## XLSX validation pitfall

**DO NOT use `load_workbook(read_only=True)` for row/column counting.** It frequently misreports dimensions as `1 row × 1 column` even when the workbook contains thousands of rows, because Go-generated XLSX files may have stale or missing worksheet dimension metadata. This happened AGAIN 2026-06-12: three separate XLSX files (410KB, 409KB, 21KB) all reported `Rows: 1, Cols: 1` with `read_only=True` but contained 8652 rows when read normally.

**Always use default mode for counting/first-pass inspection:**

```python
from openpyxl import load_workbook
wb = load_workbook(path)  # NO read_only=True
ws = wb.active
print(ws.max_row, ws.max_column)  # correct
```

**Reserve `read_only=True` only for streaming reads** where you never need `max_row`/`max_column`.

## Ad-hoc rating-only score/sales scrape via `--urls-file`

When the user asks to scrape *only* product scores, booking counts, review counts, and product info (not SKU prices) from a flat list of product IDs/URLs, use the fixed Mode2 contract:
- CLI 的 mode2 默认使用 `normal` 延迟（与 mode1 一致），该工作流可直接运行默认参数。如需调整延迟，显式传入 `--delay-profile fast`/`safe`/`slow`。

```bash
/tmp/trip-score-cdp \
  -run-mode mode2 \
  --urls-file /tmp/urls.txt \
  --delay-profile fast \
  --browser-restart-every 20
```

The program auto-generates the standard output filename: `trip_score_results_<YYMMDD-HHMM>.xlsx` under the run directory (or `trip_scoreandprice_results_<YYMMDD-HHMM>.xlsx` for mode1).

**After the run completes successfully (exit_code=0), deliver the program's own output XLSX directly via MEDIA:** — reference the exact file path from the `export_done` / `run_done` event. Do NOT read the XLSX, re-export, modify, or create a second copy. The program's file on disk IS the deliverable.

**Delivery destination (mandatory rule): 任务完成时，将结果文件 MEDIA 发送到「发出本次抓取指令的那个会话/群组」**（即指令来源会话的 origin 群组），或其显式指定的目标。不要投递到无关群组或只保存到本地。若指令来自某群组 A，就发回群组 A；若指令显式指定了其他投递目标，则投递到该指定目标。此规则对所有抓取完成交付（Mode2 成功/失败、厂商 Excel 模式、cron 结果包、手动单产品抓取）一视同仁。

**结果一定不漏发（强制投递 + 成功回执，方案 B）：** 抓取成功（exit 0）后，投递必须用项目脚本 `trip_deliver.py` 走 `hermes send` 完成，并由脚本**返回 exit 0 才算送达**；未确认送达前不得以成功结束任务。方式：

```bash
python3 trip_deliver.py <output_path.xlsx 或 run_dir> \
  --to telegram:<发起该指令的 chat_id> [--subject "携程抓取结果"]
```

- 脚本会校验输出文件存在且非空 → 调用 `hermes send --to <target> "MEDIA:<path>"` → **仅当 hermes send exit 0** 才写 `.delivered.json` 并 exit 0。
- 投递失败/文件缺失 → 脚本写在 run 目录写 `.pending_delivery.json` 并返回非零；此时任务必须标记为「失败待补发」，**绝不静默当作成功**，也不得以「已落盘」冒充送达。
- 目标 `chat_id` = 发起该次抓取请求的会话/群组（当前会话来源），或显式指定项。

Success delivery text (fixed template):

```
✅ Mode2 完成
抓取行数：<processed_count>
MEDIA:<output_path>
```

Failure delivery text (fixed template):

```
❌ Mode2 失败
原因：<error>
```

Mode2 must align with the legacy GUI options `仅获取评分` + `获取商品信息` and output exactly one row per URL/product with these columns only:

```text
商品ID, URL, 商品名称, 商品资讯, 总促销标签, 月销量, 年销量, 总销量, 评价数, 评分, 好评率, 客户服务, 方便程度, 讯号状态, 中差评价数
```

Mode2 must not enter SKU traversal or write `SKU层级`, `货币`, `外显价格`, `实价`, or `促销标签`. Treat accidental price/SKU columns in Mode2 output as a regression.

### `-input` vs `--urls-file`

| Method | Expected Result |
|---|---|
| mode2 + `--urls-file /tmp/urls.txt` | ✅ Rating/product-info only, one row per URL, no price/SKU columns |
| mode2 + repeated `-input URL` | ✅ Same output shape, but be careful not to duplicate arguments in shell arrays |
| mode1 + `--urls-file /tmp/urls.txt` | Full SKU/price/rating traversal; use only when the user asks for price/SKU data |
| No `-run-mode` + `-input` | Ambiguous/legacy behavior; avoid for ad-hoc score-only tasks |

### Validation checklist for Mode2

- Verify row count is input URL count + header.
- Verify headers match the 11-column contract above.
- Verify no SKU/price columns are present.
- Verify XLSX dimensions are correct (`A1:K<N>`). If Excel/openpyxl appears to show only one column, inspect/fix worksheet dimensions before assuming data is missing.

### Mode2 长时间/多站点运行的稳定性保障

Mode2 单 worker 多站点（`--urls-file` + `--sites`）会顺序爬大量页面（如 161×6=966），浏览器长时间运行容易遭遇反爬检测、Cloudflare overlay 或 Chrome 内存崩溃。为防止整个运行中途被杀死而毫无产物，强烈建议同时传入：

- `--checkpoint-every-urls 20`：每 20 个 URL 写一次 partial `.xlsx`
- `--browser-restart-every 25`：每 25 个 URL 重启 Chrome 降低内存压力
- `--browser-memory-limit-mb 4096`（或更低）：在内存达到阈值时主动重启，而不是等系统 OOM kill
- `--kill-orphans`：退出时清理残留 Chrome 进程

如果中途被 kill（没有 `run_done` / 无 `exit 0`），检查 `output-dir` 中是否有 `_partial_*.xlsx` 或最近的 checkpoint 产物，**不是必须从零重跑所有 URL**；可用相同命令但将 `--urls-file` 替换为仅含剩余未覆盖 URL 的文件，CLI 会继续追加到同一输出。**绝不能重新写入包装脚本**，只通过 CLI 参数调整即可完成恢复。

Full reference: `references/trip-score-cdp-input-mode.md`

## Multi-site mode2 product info scrape

当用户只给定产品 ID（例如 `73727601 94238148 46399234`）并要求抓取产品信息时，CLI 支持 `--sites` 参数在一次调用中逐站完成 mode2 抓取，并将所有站点的结果写入一个 workbook，每个站点一个 sheet。

站点默认规则：

1. 用户未指定站点 → 默认抓取 **香港站** (`--site hk` / `locale=zh-HK&curr=HKD`)。
2. 用户指定 `全站点` → 使用 CLI 内置的 33 个标准站点 (`--sites global,hk,sg,us,tw,jp,kr,th,vn,id,my,au,uk,de,fr,es,it,nl,pt,br,ca,ae,sa,in,ph,ua,dk,pl,fi,se,tr,gr,ru`)。
3. 用户指定站点列表（例如“台湾站、日本站、韩国站”） → 用这些站点的 code 填入 `--sites tw,jp,kr`。
4. 输出目录结构：
   - 单站点：由 Agent 指定输出路径，保持与之前一致。
   - 多站点：直接落到 `--output` 指定的单文件，sheet 名为大写的站点 code（例如 `HK`, `JP`, `TW`）。

Product ID to URL 构造规则：
- 对每个 product ID，CLI 内部通过 `--site`/`--sites` 参数解析市场 context，rewrite 导航目标为 `https://{host}/things-to-do/detail/{productID}/?locale={locale}&curr={currency}`。
- Agent 构造 `--urls-file` 时只需填写所有目标 URL（可任选一个来源站，CLI 会按当前市场自动重写 host/query）。

推荐的模式：

```bash
/tmp/trip-score-cdp \
  -run-mode mode2 \
  --urls-file /tmp/urls.txt \
  --sites hk,jp,tw \
  --output <run_dir>/mode2_multi.xlsx
```

若不用 `--sites` 而继续使用单一的 `--site`，行为不变，输出为单 sheet 单市场数据。

验证要点：
- 多站模式的输出 XLSX 的 sheet 名应为大写 site code。
- 每个 sheet 的列仍然是 `商品ID/URL/商品名称/商品资讯/...`（与 mode2 合同一致）。
- 同一产品 ID 在不同站点的评分/评价数可能不同；这是预期行为，不做跨站聚合。

### Mode2 厂商表 × 多站点（多 worker，市场串行）

当同时传入 **`--excel-input <厂商表.xlsx>`** 与 **`--sites <站点列表>`** 时，CLI 进入新增的**多厂商多站点 mode2** 路径（`scrapeVendorMultiSite`）：

- **市场（站点）严格串行**：外层 `for markets` 顺序遍历，先跑完一个站点所有厂商的产品，再进入下一个站点（满足“先跑完一个市场的所有产品，再跑下一个市场”）。
- **同一站点内多厂商并行**：每个站点内用 `--workers N`（≤5）并发多个厂商 worker（同站不同厂商连续跑）；worker 仅回传行数据，**不创建厂商分片 XLSX**。
- **行标记**：协调器为每行写入 `厂商`（来自厂商列）与 `站点`。
- **唯一输出**：无论 worker 数量，协调器只在全部市场完成后写入一个 workbook，**每个站点一个 sheet**（大写 site code），每行带 `厂商` 列；文件名 `trip_mode2_<stamp>.xlsx`（多站）或 `trip_score_<stamp>.xlsx`（单站 rating-only）。
- `--excel-input` 单元格须为 Trip 完整 URL（含 `http` + `trip.com`），列头即厂商标识。若用户给的是产品 ID，Hermes 先按「URL 拼接约定」转成 URL。

```bash
/tmp/trip-score-cdp \
  -run-mode mode2 \
  --excel-input /path/to/日常抓取目标.xlsx \
  --sites hk,tw,jp \
  --delay-profile fast \
  --workers 2 \
  --output-dir <run_dir> \
  --no-headless
```

验证要点：合并 workbook 的 sheet 名 = 大写 site code；每 sheet 首列为 `厂商`；行数 = Σ(各站点 厂商数×产品数)；列仍符合 mode2 合同（无 SKU/价格列）。

## Daily SKU vendor price trend cron

For the user's standing daily SKU vendor price/real-price tracking task, use Hermes cron scripts rather than macOS LaunchAgent. Do **not** describe this cron as a "non-TSIM" task: the fixed input now includes TSIM products and competitor columns. It is a generic vendor trend scrape with one configurable default comparison target; the current default comparison vendor is `TSIM 網址`.

Canonical daily setup:

- Source product-pair document: `/Users/example/Documents/trip-sku-price-scrape/config/携程上的TSim主要产品和同地区竞对.xlsx`
- Config input workbook: `/Users/example/Documents/trip-sku-price-scrape/config/非TSIM厂商价格抓取输入.xlsx` (legacy filename only; it may contain TSIM and competitors)
- Current input policy: the config workbook is allowed to contain `TSIM` because the daily task is built around TSim vs same-region competitors; treat each row as a same-region product-pair row, not as a strict non-TSIM-only input.
- Project-tracked launcher script: `/Users/example/Develop/trip-score-price-scraper/scripts/hermes-cron/trip_sku_daily_launcher.py`
- Project-tracked trend finalizer script: `/Users/example/Develop/trip-score-price-scraper/scripts/hermes-cron/trip_sku_trend_finalize.py`
- Hermes cron wrapper script: `~/.hermes/scripts/trip_sku_daily_launcher.py`
- Completion/delivery watcher: `~/.hermes/scripts/trip_sku_non_tsim_daily_watcher.py` (legacy filename; report title/package detection no longer use non-TSIM wording)
- Daily launcher cron: `0 8 * * *` (UTC+8/local morning 08:00); launcher is quick fire-and-exit (launches detached scrape and exits before Hermes cron timeout)
- Detached runner generated per run: `run_daily_sku_vendor_trend.sh`.
- Default comparison target: launcher constant `DEFAULT_COMPARE_VENDOR = 'TSIM 網址'`; this is a default baseline, not a hard project identity. Change this constant/config when the daily baseline changes.
- Watcher cron (`every 60m`): enabled as fallback; reads current state/logs and sends meaningful progress summaries. Primary final ZIP/report delivery now happens inside the detached runner via `trip_sku_non_tsim_final_delivery.py` immediately after finalization; watcher retries final delivery only when a finalized run lacks `.delivered_daily_trend`.
- Storage/packaging/log policy: scheduled runs must retain `terminal_output.txt` after final delivery, because it is the primary forensic source for Whaleguard/session/SKU-price anomalies. The daily launcher should pass `--json-lines` so `terminal_output.txt` contains machine-readable JSONL events plus terminal messages. Final ZIP should include `terminal_output.txt`; keep excluding `trip-score-cdp-help.txt` and `finalize_output.json`.
- Default scheduled-run parameter policy: cron/default SKU 实价抓取 should follow the current project README/CLI before launch. As of the 2026-08-06 code refresh, the main daily scrape defaults to visible Chrome (headed) with `--delay-profile fast`. The recommended daily command shape is `--run-mode mode1 --delay-profile fast --timeout 1800 --workers 2`. Low-quality URL recovery is handled by the CLI quality-rerun path, which defaults to visible-browser `safe` recovery unless overridden. Keep this convention in launcher/state metadata, generated shell command, and 任务说明.md. `headless` 不建议作为默认参数；如需 headless，显式传入 `--headless`。`normal`/`safe`/`slow` 仅在风控或异常诊断场景临时使用。

### Default speed profile update protocol

- 当前默认只应在两类场景变更：
  1. 用户明确要求整体默认策略切换（如 `fast` -> `normal`，`headed` -> `headless`）。
  2. 团队把历史策略作为长期参数落盘更新（如写入 cron/launcher 或新一轮日常任务规范）。
- **执行顺序（必须同时覆盖）**：
  1) `hermes-agent/skills/trip-sku-price-scraper-operations/SKILL.md` 的任务模板（`任务说明.md`示例）
  2) 项目 launcher 的默认常量与 CLI f-string（`scripts/hermes-cron/trip_sku_daily_launcher.py`）
  3) 本地快速验证（`go build` + `--help`，必要时跑一次 smoke 跑）
- 不要为了“把默认改回来”去改动正在进行中的 run。当前任务只在新建任务生效：
  - 想立刻变慢/变快，先停掉当前任务（如有必要），再在启动新 run 时显式传入 `--delay-profile normal|safe|slow`。
  - 不要把“本次临时覆盖”写入长期默认，除非用户明确要求固定该默认值。
- 风控/异常诊断时，仅对该次跑进行 `--delay-profile normal` 或 `--delay-profile safe`/`slow` 临时覆盖；完成后若无问题回归默认 `fast`。

- Current daily main scrape mode is **headed (visible Chrome)** per the project README/CLI; the quality-rerun recovery path is the place for visible-browser `safe` single-worker retries of low-quality URL results. Do not change an active run when documenting or updating this default; changes apply to newly launched runs only.
- Before editing the daily launcher or cron wrapper, check for merge-conflict markers and config/command drift. The generated launcher must agree across `input_summary.json`/`current_state.json`, generated `任务说明.md`, and the actual `/tmp/trip-score-cdp` command.
- Watcher/final-delivery scripts must discover vendors from run state rather than splitting XLSX filenames on `_`; vendor headers such as `TSIM 網址`, `其他2`, and `其他3` must survive unchanged.
- **FastNoPromoReturn** is `true` for all delay profiles (browser.go:958): SKUs without promo tags return immediately once base price is confirmed, skipping the 2.5s fallback wait. Saves ~15-20 min per 455-URL run. No anti-bot risk increase. See `references/whaleguard-detection-response-chain.md` for implementation details and revert instructions.
- Cron Python environment pitfall: Hermes cron may invoke `.py` scripts with the Hermes venv Python, which can lack `openpyxl`; launcher/finalizer scripts should re-exec via `/usr/local/bin/python3` before importing `openpyxl`, or otherwise ensure the cron interpreter has `openpyxl` installed.
- macOS ChromeDriver pitfall (Chrome 149 observed 2026-06-08): modifying/patching Chrome for Testing `chromedriver` can invalidate the macOS code signature and make execution fail with SIGKILL, surfaced as `chromedriver --version failed for .../undetected_chromedriver_149`. On macOS, keep the downloaded chromedriver binary unmodified and rely on runtime browser stealth patches; remove corrupt `undetected_chromedriver_<major>` and rebuild/relaunch after code fix.
- Concurrent ChromeDriver cache pitfall: vendor workers share the same `~/Library/Application Support/undetected_chromedriver` cache. `prepareUndetectedChromeDriver` must be serialized with a process-wide mutex; otherwise workers can race on `chromedriver_download.zip`, `*.tmp`, and `undetected_chromedriver_<major>` and all fail before URL capture.
- Runner state pitfall: generated `run_daily_sku_vendor_trend.sh` must not let `set -e` exit immediately after a non-zero `/tmp/trip-score-cdp` scrape. Temporarily disable `set -e`, capture `$?`, write `exit_code.txt`, update `current_state.json` to `failed` when non-zero, and only then exit; otherwise failed startup runs remain falsely marked `running`.

When the user updates the product-pair workbook, mirror the latest document into `config/携程上的TSim主要产品和同地区竞对.xlsx` and regenerate or overwrite `config/非TSIM厂商价格抓取输入.xlsx` so the daily task uses the newest same-region product pairs. Always inspect the uploaded workbook first, back up both existing canonical XLSX files under `config/_backups/`, copy the new workbook to both canonical paths, then verify URL counts by vendor before launching. New target workbooks may add extra vendor columns such as `其他2` / `其他3`; treat any row-1 header column containing Trip URLs as a vendor scrape column, and check downstream helpers for hardcoded vendor lists before relying on them. See `references/target-workbook-replacement-and-full-rerun-2026-06-17.md` for the concrete replacement + full-rerun checklist.

Trend comparison rules:

- Current canonical source workbook: `/Users/example/Documents/trip-sku-price-scrape/config/携程上的TSim主要产品和同地区竞对.xlsx`
- Current config input workbook: `/Users/example/Documents/trip-sku-price-scrape/config/非TSIM厂商价格抓取输入.xlsx`
- Compare only columns whose header row is exactly a vendor identifier: `TSIM 網址`, `BC`, `TGT`, `FR`, `JY`, `其他`.
- Treat metadata/header columns such as `分類` and `目的地` as product-pair context columns only; do not treat them as vendor scrape columns even if they are in the same workbook.
- Match by `厂商 + 商品ID` where 商品ID is parsed from `/detail/<完整数字ID>` or read from the `商品ID` column.
- Match SKU by normalized same-name SKU layers (`SKU层级1|SKU层级2|...`), using light normalization only: trim, NFKC, lowercase, whitespace removal, dash normalization.
- Treat product-level fields (`预定数`, `评价数`, `评分`, `客户服务`, `方便程度`, `讯号状态`, `中差评价数`) as repeated across SKU rows; this is expected.
- `预定数变化` means current cumulative Trip booking count minus previous cumulative booking count; it is a sales-dynamics proxy, not guaranteed true daily sales.
- Append trend columns to the current XLSX outputs, including previous values, deltas, percentage changes, and trend status.
- Also write a SKU vendor trend total workbook with sheets for full detail, new product/SKU rows, missing SKU rows, and summary.
- When the user says “每一行作为TSIM（我们公司）和其他家产品的定时任务抓取目标”, treat each input row as one same-region product pair and include `TSIM` as a scraping/vendor comparison target alongside competitors.

## Manual vendor attribution reference

When doing Trip SKU / Trip.com product vendor ownership labeling, first load the latest human-reviewed attribution map before using heuristics:

```text
/Users/example/Documents/trip-operations/vendor-attribution/manual_vendor_attribution_latest.json
```

Match priority:
1. `商品ID` / product ID parsed from `/detail/<id>`
2. exact product URL / canonical URL
3. product name only as a fallback

The map was built from a manually reviewed workbook (`廠商名稱未標注 回傳_Done.xlsx`, first sheet) and contains `商品ID / 完整网址 / 产品名称 -> 厂商名称` records. Preserve user-provided labels such as `TSim`, `BC`, `TGT`, `FR`, `JY`, and `OTH`; do not silently normalize them to a different spelling in outputs unless the user asks.

## Cloudflare/risk interception handling

Trip may display a Cloudflare anti-bot overlay during SKU scraping. The canonical DOM indicator observed by the user is:

```html
#guestLogin-sliderTntLoginBanneribu_nonmemberlogin_online_pic_cf_container
<div class="cf-close-container"><div class="cf-close">×</div></div>
```

Additional Turnstile widget indicators observed by the user include a checkbox label such as `驗證您是人類`, status text such as `正在驗證...`, failure/expiry text such as `驗證失敗` / `驗證已過期`, element `#challenge-error-text`, and links to `cloudflare.com/products/turnstile` or `/cdn-cgi/challenge-platform/help`.

Operational rule: do not auto-click the close button or checkbox and do not bypass the challenge. Mark the affected product as `risk_blocked`, preserve the native workbook/diagnostic note, end the current browser session, and treat the overall selected-product run as unsuccessful. A row carrying `抓取备注=被Cloudflare拦截` must never remain eligible for successful delivery merely because it also contains a trusted price status.

Log marker to inspect in `terminal_output.txt`:

```text
cloudflare_overlay_detected
```

Do not immediately loop on the same product. If an operator explicitly retries after a reasonable cooldown, keep `--delay-profile fast --no-quality-rerun`; after another Cloudflare/risk interception, report `<产品ID> 价格抓取失败，原因：Trip.com/Cloudflare 风险拦截，未获得完整 SKU 价格。` and do not claim success.

## Run teardown and selective rerun

When a Trip SKU run is corrupted by anti-bot detection, session loss, or browser failures:

- First inspect `terminal_output.txt` for `url failed`, `risk challenge detected`, `session not created`, `whaleguard block`, or panic traces.
- If the user asks to **preserve good rows and rerun only bad parts**, stop the run, export whatever partial XLSX the CLI produced, then parse failure/incomplete URLs and build a rerun input workbook.
- If the user explicitly asks to **fully delete** a bad run and its data, kill the wrapper and child processes first, remove the run directory, verify the directory no longer exists, and verify no matching processes remain.
- Do not claim a rerun can be merged back into a deleted run folder. If the user asks both to delete and to merge later, clarify that the merge base must either be preserved before deletion or reconstructed from a fresh clean run.
- For reruns after anti-bot instability, prefer 默认 `--delay-profile fast`，除非用户要求；如出现风控，可改为 `--delay-profile normal` 或 `--delay-profile safe`, 配套 `--timeout 1800`, `--workers 2`。

## Launcher config-CLI mismatch pitfall

The daily launcher script (`scripts/hermes-cron/trip_sku_daily_launcher.py`, invoked by `~/.hermes/scripts/trip_sku_daily_launcher.py`) generates a bash wrapper that writes `current_state.json` AND then invokes the CLI with actual parameters. These two can drift apart:

- The `config` dict in `input_summary.json` / `current_state.json` is set in Python.
- The CLI invocation in the generated shell script is a separate f-string.

When changing parameters (delay profile, workers, etc.), **all three locations** must be updated:
1. The `config` dict (Python, line ~73)
2. The CLI `--delay-profile` / `--workers` flags in the shell script f-string (line ~108)
3. The `任务说明.md` template (line ~83)

Bug precedent (2026-06-02): `config` said `normal` but CLI used `--delay-profile safe`, causing 346 whaleguard blocks in a 455-URL run. Always verify with:
```bash
grep "delay-profile\|workers\|compare-vendor" <run_dir>/run_daily_sku_vendor_trend.sh
```

## Vendor comparison (`--compare-vendor`)

The Go CLI has a built-in generic vendor comparison component (`internal/tripcdp/vendor_compare.go`). It compares any "base vendor" against all other vendors in the input config, using row-based region mapping.

### CLI flag

```bash
/tmp/trip-score-cdp \
  --excel-input INPUT.xlsx \
  --compare-vendor "TSIM 網址" \
  --output-dir "$RUN_DIR" \
  --delay-profile fast --timeout 1800 --workers 2
```

`--compare-vendor <name>`: After scraping completes, automatically compare all vendors against the named base vendor. The base vendor name must match a column header in the input Excel.

### How it works

1. **Input mapping**: Reads the input Excel to build `row → {base URL, competitor URLs}` mapping. Each row = one region group.
2. **SKU matching**: For each region group, parses SKU specs into `有效期天数 + 流量类型 + 流量MB`, so formatting variants such as `總量-10GB`, `總量 10GB`, `总量10GB`, `5G-總量-10GB`, and `5G網絡-總量-10GB` align as one comparable SKU. `总量` and `每日` are not merged by default.
3. **Price comparison**: Uses `实价` (real price) for comparison. Calculates per-product avg/minimum price deltas only from officially matched SKU intersections.
4. **Output**:
   - `vendor_compare_<stamp>.xlsx`: 汇总 + 横向比价 + 单链接均价对比 + vendor pair sheets
   - `vendor_compare_<stamp>.json`: Machine-readable summary for agent/watcher consumption

### Output Excel layout

`vendor_compare_<run>.xlsx` is the single final comparison workbook. It uses a horizontal matrix layout:

- `总表_横向比价`: all vendors in one sheet. Each row = same input-config region/destination + same parsed compatible SKU spec. Vendor data is placed in contiguous column blocks.
- `单链接均价对比`: one destination/input-row-level sheet. Each row = one input workbook row / same-region product group. It does **not** list individual SKUs; instead it evaluates each vendor's price level using matched-SKU average prices. SKU-level horizontal comparison remains in `总表_横向比价`.
- `<vendor>_vs_<base>` sheets: one sheet per competitor vendor, still horizontal, containing the base vendor block + that competitor block.
- `汇总`: aggregate matched-region / matched-SKU / average-delta statistics.

Horizontal row key:

```text
分類 + 目的地 + parsed SKU spec(有效期天数 + 流量类型 + 流量MB)
```

Fallback row key for unparseable SKU rows remains normalized text only, and fallback rows should not contribute to official matched-SKU price deltas unless reviewed.

Vendor block fields are contiguous and repeated per vendor:

```text
商品ID, 商品名称, 原始SKU1, 原始SKU2, 货币, 外显价格, 实价, 促销标签,
预定数, 评价数, 评分, 同规格候选数, 相对基准实价差, 相对基准最低价差, 价格结论
```

Important semantics:

- A competitor-only SKU combination is **not dropped**. If the competitor has a SKU combination that the base vendor lacks, that row remains in the sheet with the base vendor block blank.
- `相对基准实价差` is populated only when both base and competitor have the same matched SKU key.
- `相对基准最低价差` is a product-pair-level minimum-price delta and is populated only on the competitor's lowest-price matched SKU row for that product pair. It is intentionally blank on other rows.
- All average/minimum deltas are computed over matched SKU intersections only. Product-level averages across non-overlapping SKU sets are forbidden.

### Output JSON schema

```json
{
  "base_vendor": "TSIM 網址",
  "output": "/path/to/vendor_compare_260602-0940.xlsx",
  "layout": "horizontal",
  "matched_only": true,
  "vendors": {
    "BC": {
      "matched_regions": 37,
      "matched_skus": 1340,
      "avg_delta": 0.85,
      "base_cheaper_pct": 47.8
    }
  }
}
```

- `avg_delta`: Average (competitor price - base price) across matched SKU pairs only. Positive = base is cheaper.
- `base_cheaper_pct`: Percentage of matched SKU pairs where base price < competitor price.

### Watcher integration

The watcher script reads `vendor_compare_*.json` and appends a comparison section to the final report:

```
━━━━ TSIM 網址 vs 竞对比价 ━━━━
BC | 37地区 1340SKU匹配 | 均价差: +0.85 | 基准更优: 48% | 基准更优
FR | 85地区 1935SKU匹配 | 均价差: +18.24 | 基准更优: 66% | 基准更优
...
```

### Design notes

- The component is **vendor-agnostic**: any column header can be the base vendor. Not tied to TSIM or any specific company. When building comparison components, always design for generic use — never hardcode company names or assume a specific vendor is the "base."
- It operates on the raw XLSX outputs, not on trend files. No previous-run baseline needed.
- Region mapping comes from the input Excel's row structure (same row = same region).
- **Matched-only comparison principle**: Price comparisons MUST be restricted to matched SKU pairs only (same effective duration + compatible data specification). Do NOT compare product-level averages across different SKU sets — products from different vendors often have different SKU combinations (e.g., TSIM has 155 SKUs for Europe while BC has 145). Averaging across unmatched SKUs produces misleading results. All deltas (avg, min) must be computed exclusively from the matched intersection.
- **SKU compatibility matching**: Prefer structured SKU spec parsing over simple string keys. Parse effective duration, data type (`总量` vs `每日`), and data amount normalized to MB. Treat punctuation/spacing/prefix variants such as `總量-10GB`, `總量 10GB`, `总量10GB`, `5G-總量-10GB`, and `5G網絡-總量-10GB` as the same specification. Strip marketing/network prefixes (`5G網絡-`, `5G-`, `4G-`, `4G網絡-`, `高速-`) from spec identity. Do not merge `总量` and `每日` packages by default.
- **Match-rule transparency**: Track the rule used for each alignment (`exact_spec`, `normalized_text`, `unit_converted`, `near_equivalent`, `unmatched`). Only high-confidence matches should contribute to official price deltas; near-equivalent candidates should be shown as diagnostics unless the user explicitly asks for looser comparison.
- **Competitor-only SKU retention**: Competitor-only SKU combinations are never dropped. Keep the row and leave the base vendor block blank; mark conclusion fields such as `TSIM无此SKU` / `基准无此SKU` when appropriate.
- **Lowest-price delta placement**: `相对基准最低价差` / `相对TSIM最低价差` is a product-pair metric and should appear only on the lowest-price matched SKU row for that product pair and competitor. It should remain blank on other SKU rows.
- **Implemented layout**: Per-SKU price gap fields are written into `vendor_compare_<run>.xlsx` as horizontal vendor column blocks. Use `总表_横向比价` for SKU-level all-vendor comparison, `<vendor>_vs_<base>` sheets for pair-specific SKU review, and `单链接均价对比` for one-row-per-input-destination matched-SKU average-price vendor-level assessment.

## Delivery format

This section applies to batch, comparison, trend, and scheduled deliveries only. It does not override the attachment-only success response required by `Manual single or selected products price scrape`.

The user prefers XLSX deliverables. Keep all generated files for this workflow under `/Users/example/Documents/trip-sku-price-scrape/YYMMDD-HHMM/`, with `任务说明.md` in the run folder. Cron/default scheduled runs for SKU 实价抓取 should use `--delay-profile fast`, `--timeout 1800`, and `--workers 2` unless the user explicitly asks for a different mode. For delivery, send a ZIP containing XLSX files and concise validation artifacts. In Telegram, include the final package as:

```text
MEDIA:/absolute/path/to/package.zip
```

Keep the final human summary concise:

- what was stopped/resumed/built
- benchmark result
- completion/exit code
- validation coverage by vendor
- package path/media attachment

## Batch/historical XLSX export may be empty while logs contain data

This section applies only to batch or historical diagnostic recovery. It does not apply to the manual single or selected products workflow: that workflow always delivers the native workbook unchanged, or reports that the native workbook is missing.

Observed in the 2026-05-29 smoke test: the Go CLI completed successfully and logged thousands of `combination price` records, but the per-vendor XLSX files contained only the header row `厂商` and no detail rows.

Do **not** conclude that the scrape failed solely from empty XLSX output. Verify the JSON log first:

- `url_done` events contain completed URL counts and row counts.
- `combination price:` log messages contain SKU combination, display price, real price, promo label, price source, resourceId, and itemId.

If a batch or historical XLSX output is empty, reconstruct a diagnostic workbook from `run.jsonl`:

- Track current URL by `(worker_id, vendor)` from `url_started`.
- Parse `log` messages starting with `combination price:`.
- Extract fields: 厂商, URL, 导航URL, SKU组合, 外显价格, 实价, 促销标签, price_source, resourceId, itemId, promo_matched, 时间, worker_id.
- Add sheets for URL completion summary, vendor summary, and error events.

This reconstruction is a diagnostic fallback for batch or historical artifacts, not a single-product user-delivery format. For the current daily diagnostic workflow, JSONL logging (`--json-lines`) is enabled by default and `terminal_output.txt` is retained.

## Project Markdown handoff

When a workflow default changes, write it into the project Markdown so future Agents can discover it before running commands:

- `go-readme.md` is the main place for Go CLI command defaults, delay tables, logging rules, and Agent handoff notes.
- `README.md` can carry a short high-level Agent note near CLI startup instructions.
- Routine examples should omit `--json-lines`; keep JSONL examples only under an explicit machine-readable/diagnostic logging section.
- If fast-mode timing values change, update the delay profile table in `go-readme.md` in the same session as the code change.

## When results look unstable

If Trip shows challenge/risk events, missing SKU containers, suspiciously low URL completion counts, or repeated fixed prices such as `HKD22` / `HKD33` with `resourceId=0,itemId=0,promo_matched=false`:

1. Preserve the current run folder and `terminal_output.txt`; do not rely only on final XLSX. Some failed/aborted runs may have no vendor XLSX but still contain the first evidence in logs.
2. Parse both XLSX and JSONL/terminal logs by run id. Distinguish:
   - Whaleguard hard block: `risk challenge detected`, `whaleguard block`, `cooling down and retrying URL`.
   - Cloudflare/Turnstile: `cloudflare`, `turnstile`, captcha/verification markers. Do not attribute anomalies to Cloudflare without these markers.
   - SKU/price API abnormal state: `combination price` rows where most/all SKU combinations have identical real price and `resourceId=0,itemId=0,promo_matched=false`.
   - Browser/session recovery failure: `Session with given id not found`, `url failed`, panic/fatal traces.
3. For diagnosis runs, ensure the launcher command includes `--json-lines` and that final delivery does not delete `terminal_output.txt`.
4. Re-run a small subset with `--workers 1` or `--workers 2`.
5. Use `--delay-profile safe` or `slow` if Whaleguard/risk events continue.
6. If necessary, temporarily switch delay/profile settings for diagnosis: reduce workers and use `safe`/`slow`; keep visible browser as the default unless headless itself is being tested.
7. Compare URL completion, `combination price` counts, `resourceId=0` frequency, and repeated fixed-price PID counts before scaling back to the current daily default `workers=2`.
5. For historical “when did the anomaly start?” audits, inspect both final vendor XLSX files **and** run logs/ZIP-contained logs. A run may leave only `terminal_output.txt` and no vendor XLSX; those log-only runs can contain the earliest evidence. See `references/price-anomaly-start-audit-2026-06-17.md`.

### Fixed-price identity anomaly audit

When the reported symptom is repeated prices such as many rows at HKD22/HKD33/HKD0.75 plus `resourceId=0`, `itemId=0`, and `promo_matched=false`, parse `terminal_output.txt` in addition to XLSX outputs. Group log `combination price` lines by `(vendor, product_id)` using preceding `url started ... /detail/<id>/` lines, then flag products where one real price dominates at least 80% of rows and `resourceId=0,itemId=0,promo_matched=false` also dominate. Keep this separate from single-row/captcha/incomplete-output anomalies in the final report.

## Run-mode definitions

- `mode1`: headed full SKU/price/rating scrape.
- `mode2`: headed rating-only + product-info scrape aligned to the legacy GUI options `仅获取评分` + `获取商品信息`. It outputs one row per URL/product with `商品ID/URL/商品名称/商品资讯/预定数/评价数/评分/客户服务/方便程度/讯号状态/中差评价数` only. It must not output SKU layers, currency, display price, real price, or promo labels. If Excel appears to show only one column, inspect/fix XLSX sheet dimensions before assuming data is missing.

### 模式路由规则（自然语言）

- 用户点名「SKU」（如"抓取SKU价格"、"抓SKU"、或要求价格/SKU层级/套餐/实价外显价/比价）→ 使用 `mode1`。
- 用户未点名 SKU、或只提「商品信息/评分/商品资讯/评价数/预订数」（如"只抓评分"、"抓商品信息"、"Mode2 抓评分"）→ 使用 `mode2`。

### 手动抓取执行铁律（禁止脚本与成功刷屏）

这是对「Manual single or selected products price scrape」的强制补充，任何手动/指定产品抓取都必须遵守：

1. **只用命令行传参直接运行 Go CLI**：`./trip-score-price-scraper -input <URL> -input <URL> -output-dir <dir> -delay-profile fast -product-info -no-quality-rerun`。**绝不编写/运行任何 shell 包装脚本、临时调用脚本或 wrapper 来执行抓取。** 若一次传参的终端命令因网关生命周期守卫误报（`ValueError: embedded null byte`）被拒绝，改用后台方式重发同一条**原样命令行**即可，不要改写成脚本文件绕行。
2. **抓取成功（exit 0）后禁止二次读表/打印数据**：不要用 openpyxl/python 重新读取工作簿来"验证"，更不要 `print()` 出 SKU 行、价格、评分、行数等明细到群聊。CLI 自身的 `run_done`/exit code 已是成功判定。
3. **成功交付只回一条内容**：原生工作簿的裸绝对路径（`MEDIA:<path>`）+ 现有固定成功文案。不附产品名、价格、SKU 数、行数、评分摘要。投递必须经 `trip_deliver.py` 走 `hermes send` 并取得 exit 0 回执才算送达（见上节「结果一定不漏发」），未送达前不以成功收尾。
4. 仅当 `exit≠0` 或 CLI 明确报告失败/风险拦截时才做最小化诊断，且诊断日志不得原文粘贴进用户正文。

## References

- `references/automation-skill-cron-refresh-plan-2026-06-20.md` — session-derived checklist for reconciling updated SKU code, launcher cron, quality-rerun defaults, JSONL retention, dynamic vendor discovery, and skill/cron documentation before implementation.
- `references/codex-multi-run-sku-merge-2026-06-18.md` — consolidate Codex/manual Trip SKU runs with `url_quality_failed` products by replacing failed product rows from successful reruns, then package merged vendor XLSX and summaries.
- `references/jsonl-failure-diagnosis-and-stop-2026-06-18.md` — stop active daily SKU runs safely, retain partial outputs, parse JSONL events (`url_quality_failed`, `stop_requested`, `run_done`), and produce Markdown failure-cause reports without over-attributing to Whaleguard/Cloudflare.
- `references/jsonl-retention-and-price-anomaly-diagnosis-2026-06-18.md` — diagnose fixed-price/zero-identity Trip SKU anomalies, preserve JSONL terminal logs, and verify cron relaunch with `--json-lines`.
- `references/standard-plaintext-report-regeneration-2026-06-16.md` — regenerate the scheduled SKU plaintext report in the user's standard format when a delivery accidentally contains only comparison lines; includes vendor order, Top5 sections, parsing cautions, and redelivery targets.
- `references/ad-hoc-korea-sku-comparison-2026-06-16.md` — one-row fixed-list Korea SKU price scrape and `TSIM 網址` vendor comparison pattern, including source-row lookup, command, verification, and delivery package.
- `references/default-delay-profile-defaults-2026-06-17.md` — current daily default policy (`normal`/`timeout`/`workers`) and the required cross-file update sequence for safe persistence.
- `references/cloudflare-note-only-and-safe-delay-tuning-2026-06-17.md` — historical Cloudflare/Turnstile DOM indicators and delay-tuning notes; its note-only success semantics are superseded by the current interception handling section.
- `references/mode2-rating-product-info-contract-2026-06.md` — exact Mode2 contract, output columns, guardrails, and XLSX dimension pitfall.
- `references/vendor-compare-architecture-2026-06-02.md` — generic vendor comparison component design, files changed, matched-only comparison principle, SKU normalization rules, JSON schema, and test results.
- `references/go-cdp-only-repository-consolidation-2026-06-02.md` — project consolidation rules after the user accepted Go CDP output: remove/stop tracking old Python CLI/GUI/spec, keep Go CDP as the only action component/entry point, sync skill into project, and update README.
- `references/vendor-sku-compatible-matching-and-price-board-2026-06-02.md` — session-derived requirements for structured SKU spec parsing, robust cross-vendor SKU compatibility (`總量-10GB` vs `總量 10GB`), competitor-only SKU retention, lowest-price-delta placement, SKU-level `总表_横向比价`, and destination-level `单链接均价对比` sheet.
- `references/resume-merge-benchmark-2026-05-29.md` — concrete session pattern for stopping TSIM/FR/JY, rebuilding the Go CLI, resuming pending URLs, benchmarking speed, merging XLSX, and validating coverage.
- `references/product-pair-input-migration-2026-06-01.md` — pattern for migrating the daily scrape input from a non-TSIM-only target list to a TSim-vs-competitor same-region product-pair workbook, including launcher/watcher/finalizer expectations and canonical config file locations.
- `references/non-tsim-rerun-progress-delivery-2026-05-29.md` — concrete session pattern for rerunning non-TSIM vendors, writing the new `trip-sku-price-scrape/YYMMDD-HHMM` run layout, sending 20-minute meaningful progress summaries, forwarding the final ZIP to an operations data group, and cleaning temporary cron jobs.
- `references/non-tsim-rerun-progress-cron-2026-05-29.md` — pattern for rerunning all non-TSIM vendors into the canonical `trip-sku-price-scrape/YYMMDD-HHMM/` folder and sending 20-minute meaningful progress summaries via Hermes no-agent cron.
- `references/run-teardown-and-selective-rerun-2026-06-01.md` — pattern for stopping a corrupted Trip SKU run, preserving enough state to inspect failures, or performing a full destructive teardown when the user explicitly asks to delete a bad run and start clean.
- `references/whaleguard-antibot-and-launcher-mismatch-2026-06-02.md` — `fast` delay profile causes near-universal whaleguard blocking; launcher config-CLI mismatch pitfall; stop procedure for long-running scrape tasks.
- `references/cron-launcher-timeout-and-watcher-recovery-2026-06-03.md` — diagnosis and fix for launcher cron false `error` caused by a long-running poll loop; quick launcher + recurring watcher recovery pattern.
- `references/trend-analysis-report-format-2026-06-02.md` — watcher-generated final report with per-vendor price/sales Top5 analysis, filtering thresholds, column mapping, and delivery targets.
- `references/whaleguard-detection-response-chain.md` — Go CLI internals: whaleguard detection triggers, retry/cooldown logic, auto-escalation from normal→safe delay profile, per-worker isolation, and delay profile comparison table.
- `references/agent-driven-cron-failure-diagnosis.md` — pattern for diagnosing agent-driven cron failures where the scraper succeeds but LLM session crashes before post-processing (401 auth errors, rate limits); includes recovery options and real example.
- `references/tsim-competitor-target.md` — session-specific competitor target table details.
- `references/active-run-fixes.md` — active long-run fix/rebuild patterns and fast-mode no-promo optimization.
- `references/macos-chromedriver-signing.md` — macOS undetected chromedriver code-signing pitfall (`Killed: 9` / `invalid signature`).
- `references/2026-05-29-smoke-test.md` — first-5 headed smoke test and XLSX empty export / log reconstruction issue.
- `references/2026-05-29-headless-first10.md` — first-10 headless smoke with minute-by-minute progress pattern.
- `references/2026-05-29-full-run-monitoring.md` — all-vendor headed+fast run pattern, source workbook merge approach, and 10-minute progress cron script shape.
- `references/2026-05-29-fast-headed-optimization.md` — no-`run.jsonl` default, headed 5-worker smoke-test pattern, fast-mode SKU delay optimization.
- `references/daily-cron-recovery-and-vendor-attribution-2026-06-08.md` — recovery pattern for false-running daily cron failures caused by `set -e`, macOS Chrome 149 signed chromedriver pitfalls, concurrent driver-cache races, and the manual vendor attribution import/reference path.
- `references/tsim-price-chart-generation-2026-06-10.md` — generate price-change bar charts and multi-day trajectory charts from TSIM trend XLSX files using matplotlib with Chinese font support on macOS.
- `scripts/parse_trip_sku_run_failures.py` — parse a run folder's log for failed/risk/incomplete URLs and generate a rerun input workbook.

## Daily Mode2 Multi-Site Cron Report Prompt

This is the **fixed prompt template** that runs inside the second-stage cron job (agent mode) every day
after the no_agent data-collection script finishes. The cron context_from chain injects the output JSON
of the first stage, which looks like (actual data from the latest run):

```json
{"run_dir": "/Users/example/Documents/trip-sku-price-scrape/YYMMDD-mode2-daily",
 "output_path": "/Users/example/Documents/trip-sku-price-scrape/YYMMDD-mode2-daily/trip_mode2_daily_6site.xlsx",
 "processed_count": 966,
 "url_count": 161,
 "sites": "tw,hk,ru,jp,vn,th"}
```

### Prompt

```
你是携程商品信息分析助手。你已经拿到了今天凌晨 03:00 自动完成的 Mode2 六站抓取结果。

前置任务的输出：
- 原生 XLSX 路径：<output_path>
- 已处理行数：<processed_count>（站点：TW/HK/RU/JP/VN/TH，覆盖 161 个商品）

任务：
1. 确认 EXCEL 文件存在无误（执行 `ls -la <output_path>` 输出文件信息）
2. 使用 `python3 trip_deliver.py <output_path> --to telegram:YOUR_CHAT_ID --subject "携程 Mode2 六站日常列表"` 把原生 XLSX 发到运营自动化执行组
   — 这个脚本自身返回 exit 0 才表示投递成功（回执写入 .delivered.json）；失败则写 .pending_delivery.json 并退出非零
3. **重要**：模式2输出仅包含评分/评价数/商品资讯，不含价格或 SKU；报告中只引用原列数据
4. 发送完成确认到运营自动化执行组，说明成功投递 + 处理行数

交付模板（运营自动化执行组）：
```
✅ 携程日常列表 Mode2 六站抓取完成
📅 日期：<today>
📊 处理：<processed_count> 条（6 站 × 161 产品）
📁 文件：已通过 trip_deliver 安全投递到本群

各站概要（从原生 XLSX 读取）：
| 站点 | 商品数 | 平均评分 |
|------|--------|----------|
| TW   | N      | X.X      |
| HK   | N      | X.X      |
| RU   | N      | X.X      |
| JP   | N      | X.X      |
| VN   | N      | X.X      |
| TH   | N      | X.X      |
```
**如果任何步骤失败：**
- 投递失败 → 输出 `.pending_delivery.json` 标记、不静默成功、报告原因
- 分析失败 → 明确说明原因、不输出假数据
```

These prompt instructions are loaded by the cron job as an agent-mode task that receives context_from the first stage's output and follows them exactly.
- `scripts/trip_sku_pair_region_compare.py` — build same-region TSim vs competitor comparison sheets from a completed run directory and the canonical product-pair workbook.
