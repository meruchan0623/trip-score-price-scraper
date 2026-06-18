---
name: trip-sku-price-scraper-operations
description: Operate and maintain the Trip.com SKU vendor price scraper in this workspace.
---

# Trip SKU Price Scraper Operations

## Defaults

- Use the Go CDP entrypoint only: `go run ./cmd/trip-score-cdp` or a built `/tmp/trip-score-cdp` binary.
- Daily/full SKU scrapes default to:
  - `--run-mode mode1`
  - `--headless`
  - `--delay-profile normal`
  - `--timeout 1800`
  - `--workers 3` for daily vendor Excel runs unless a smaller vendor count applies.
  - `--compare-vendor "TSIM 網址"` unless the input workbook chooses a different baseline.
- Keep `--log-file "$RUN_DIR/run.jsonl"` for machine-readable events. Human terminal output may still be redirected separately.

## Low-Quality Recovery

- The main scrape runs headless by default.
- Low-quality URL results are automatically rerun after the primary vendor pass.
- Recovery reruns use visible Chrome with `safe` delays and a single global rerun worker.
- Only replace a primary result when the rerun is better, such as producing trusted price rows instead of risk/error/untrusted rows.
- If recovery still fails, keep the run as degraded-success: continue report generation, preserve diagnostics, and keep failed rows marked as low quality.

## Vendor Compare Input Semantics

- Vendor compare must read `分類` / `分类` from the named category column and `目的地` from the named destination column.
- Do not infer category/destination from the first two columns. Workbooks may include metadata columns such as `TSIM ID` before `分類` and `目的地`.
- For `/Users/example/Downloads/工作簿2.xlsx`, the correct semantic columns are:
  - `分類`: continent/category, e.g. `亞洲`
  - `目的地`: country/destination, e.g. `Philippines`
  - Vendor URL columns begin at `TSIM 網址`, `BC`, `TGT`, `FR`, `JY`, `其他`.

## Post-Run Review for Hermes Agent

After each live SKU scrape, Hermes Agent should perform a post-run review before reporting completion.

Required inputs:
- `$RUN_DIR/run.jsonl`
- terminal output file, usually `$RUN_DIR/terminal.jsonl` or `terminal_output.txt`
- vendor workbooks: `$RUN_DIR/*.xlsx`, excluding `vendor_compare_*.xlsx`
- diagnostics: `$RUN_DIR/_diagnostics/`
- compare artifacts: `$RUN_DIR/vendor_compare_*.xlsx` and `$RUN_DIR/vendor_compare_*.json`

Review steps:
1. Parse `run.jsonl` and summarize event counts:
   - `url_started`, `url_done`, `url_failed`, `url_quality_failed`
   - `price_quality_recovered`
   - `quality_rerun_planned`, `quality_rerun_started`, `quality_rerun_replaced`, `quality_rerun_summary`
   - `vendor_done`, `run_done`
2. Read the final `run_done` event and report:
   - `success`, `exit_code`, `processed_count`
   - `quality_failed_urls`, `final_quality_failed_urls`
   - `quality_recovered_urls`
   - `quality_rerun_attempted_urls`, `quality_rerun_recovered_urls`
3. Review every `vendor_done` event and list each vendor's:
   - `url_count`, `urls_done`, `rows`
   - `final_quality_failed_urls`
   - `quality_rerun_attempted_urls`, `quality_rerun_recovered_urls`
4. Inspect each vendor workbook:
   - Count data rows.
   - Count rows whose `价格状态` is not `trusted_*`.
   - Check `实价` for zero, negative, blank, or obvious outlier values such as `> 2000 HKD`.
   - Group rows by product/URL and check for suspicious long consecutive runs with the same `实价` and same SKU text.
   - Extract `N天` from `SKU层级1` and report each product's day coverage.
   - Treat `无SKU选项` as an allowed single-row product, but call it out explicitly when it has no day coverage.
5. Inspect `vendor_compare_*.xlsx`:
   - In `总表_横向比价`, use row 2 as the actual header row.
   - Confirm columns include both `分類` and `目的地`.
   - In `单链接均价对比`, verify `目的地` contains country/destination values, not continent/category values.
   - `分類` may remain a category such as `亞洲`; it must not be used as the destination grain.
6. Final review output should include:
   - Run directory.
   - Whether scrape completed successfully.
   - Failure rate and final failed URL count.
   - Low-quality rerun attempted/recovered count.
   - Any anomalies found.
   - Vendor compare destination-field confirmation.
   - Paths to the main JSONL log, vendor workbooks, diagnostics, and compare workbook.

If `final_quality_failed_urls > 0`, `url_failed > 0`, missing output files, or obvious workbook anomalies are found, report the run as requiring human review even if `run_done.success=true`.

## Verification

- Before claiming scraper changes are ready, run:
  - `go test ./...`
  - `go build -o /tmp/trip-score-cdp ./cmd/trip-score-cdp`
- For live validation, use the current input workbook and inspect `run.jsonl`, vendor XLSX files, `_diagnostics/`, and `vendor_compare_*.xlsx/json`.
