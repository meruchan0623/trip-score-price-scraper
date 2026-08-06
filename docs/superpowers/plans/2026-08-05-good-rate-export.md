# Good Rate Export Implementation Plan

> **For agentic workers:** Execute this plan with the repository native Codex workflow; do not use Superpowers execution sub-skills.

**Goal:** Export the API-backed Trip good-review rate as a readable percentage in normal and Mode2 workbooks.

**Architecture:** Reuse the captured `listComments` response, convert `commentAggregation.goodRate` from a 0–1 ratio to a one-decimal percentage string, merge it into the product row only when present, and add a product-level column after `评分`.

**Tech Stack:** Go, existing `capturedAPIFields` parser, excelize, Go tests.

## Global Constraints

- Accept only successful 2xx `listComments` responses already captured by the page hook.
- Treat `goodRate` as a 0–1 ratio and output `91.1%` for `0.911`.
- Leave the value blank when missing or non-positive; never derive it from DOM content.
- Preserve existing dirty changes.

### Task 1: Parse and merge the good-review rate

**Files:** modify `internal/tripcdp/browser.go`; test `internal/tripcdp/api_fields_test.go`.

- [ ] Write failing tests: add `goodRate: 0.911` to the list-comments fixture, expect `fields.GoodRate == "91.1%"`, and expect the API merge to set `row["好评率"]`.
- [ ] Run `go test ./internal/tripcdp -run 'Test(ParseCapturedAPIFieldsUsesListCommentsSubItemScores|ApplyCapturedAPIFieldsPrefersCompleteAPIValues)' -count=1`; expect failure because `GoodRate` is absent.
- [ ] Add `GoodRate string` to `capturedAPIFields`; parse `GoodRate float64`; format a positive rate with `fmt.Sprintf("%.1f%%", rate*100)`; merge `好评率`; add it to `clearRepeatedProductFields`.
- [ ] Re-run the focused test and expect pass.

### Task 2: Export and accept the column

**Files:** modify `internal/tripcdp/export.go` and `internal/tripcdp/input_export_test.go`.

- [ ] Write failing complete and rating-only header assertions with `好评率` after `评分`, and add `好评率: 91.1%` to their row fixtures.
- [ ] Run `go test ./internal/tripcdp -run 'Test.*Export.*' -count=1`; expect missing-header or dimension failure.
- [ ] Insert `好评率` after `评分` in `exportRows`.
- [ ] Run `go test ./internal/tripcdp -count=1`, `go test ./...`, `go build ./cmd/trip-score-cdp`, and `git diff --check`.
- [ ] Run Mode2 for product `45892282`, inspect the generated XLSX, and confirm `好评率` is after `评分` with the API-derived percentage.
