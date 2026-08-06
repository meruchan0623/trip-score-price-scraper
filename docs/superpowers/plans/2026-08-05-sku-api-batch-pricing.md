# SKU API Batch Pricing Implementation Plan

**Goal:** Replace the normal per-SKU click-first path with captured product/SKU-tree and price-calendar responses, while retaining the browser traversal as a quality fallback.

**Architecture:** Capture `getProductInfo` and `getProductPriceCalendar` during the existing real product-page navigation. Parse package/property/resource mappings and priced packages in Go, left-join them by package/resource identity, use complete trusted API rows immediately, and fall back to the existing CDP traversal when capture or coverage is not trustworthy.

**Tech Stack:** Go, Rod/CDP page response capture, table-driven Go tests, existing XLSX export pipeline.

## Global Constraints

- Preserve the existing HK default and SG routing.
- Do not persist cookies, authorization headers, tokens, or raw authenticated requests.
- Treat SKU-tree membership separately from current-date price availability.
- Preserve existing browser traversal and quality gates as fallback.
- Do not change rating-only mode.

---

### Task 1: Parse captured SKU tree and price calendar

**Files:**
- Create: `internal/tripcdp/api_sku_prices.go`
- Create: `internal/tripcdp/api_sku_prices_test.go`

**Interfaces:**
- Consumes: `[]capturedAPIResponse`, expected site currency, real-price flag.
- Produces: `parseCapturedAPISKUPrices(...) ([]map[string]string, apiSKUPriceCoverage)`.

- [ ] Write tests for tree/price joining, missing-price boundaries, invalid responses, and property-ID identity.
- [ ] Run the focused test and confirm it fails because the parser does not exist.
- [ ] Implement the minimum parser and coverage model.
- [ ] Run the focused tests and confirm they pass.

### Task 2: Capture and prefer complete API batches

**Files:**
- Modify: `internal/tripcdp/browser.go`
- Modify: `internal/tripcdp/input_export_test.go`

**Interfaces:**
- Consumes: captured `getProductInfo` and `getProductPriceCalendar` bodies.
- Produces: trusted combination rows compatible with the existing export mapping.

- [ ] Write tests proving complete API coverage bypasses fallback and partial coverage requests fallback.
- [ ] Run the focused tests and confirm the expected failures.
- [ ] Extend the existing page response capture to the price-calendar endpoint.
- [ ] Prefer API rows when every tree leaf has a current trusted price; otherwise retain the existing click traversal.
- [ ] Emit coverage diagnostics without request secrets.
- [ ] Run focused tests and confirm they pass.

### Task 3: Verify locally and against a real product

**Files:**
- Modify: `README.md`

**Interfaces:**
- Consumes: product `45892282`, HK site context.
- Produces: current test/build evidence and an isolated real output artifact.

- [ ] Document API-first/fallback behavior and status semantics.
- [ ] Run `gofmt` and focused tests.
- [ ] Run `go test ./... -count=1`, `go vet ./...`, `git diff --check`, and a clean build.
- [ ] Run the final binary against product `45892282` with an isolated output directory.
- [ ] Inspect logs and workbook fields for API source, row count, currency, prices, and fallback behavior.
