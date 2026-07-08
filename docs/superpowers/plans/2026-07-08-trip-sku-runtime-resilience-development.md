# Trip SKU Runtime Resilience Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build runtime resilience, failure classification, anti-bot pressure control, and vendor scheduling for the Trip SKU CDP scraper so high-risk runs fail fast, degrade safely, and preserve partial trusted output.

**Architecture:** Add a small set of focused runtime components under `internal/tripcdp`: `FailureClass` classification, runtime policy config, pressure controller, vendor scheduler, watchdog/supervisor hooks, and run summary emission. Integrate them into the existing `scrapeVendorBatches`, `runVendorWorker`, `tripScraper.scrapeOne`, and quality rerun flow without replacing the CDP extraction logic.

**Tech Stack:** Go, standard `testing` package, existing rod/CDP scraper, existing JSONL event `Reporter`, existing XLSX export path.

## Global Constraints

- The implementation must preserve existing CLI compatibility unless this plan explicitly adds a flag.
- `api_blocked_suspected` must not trigger Chrome restart style quality rerun by default.
- Every URL retry, quality rerun, browser restart, vendor skip, and global termination decision must have a finite budget or threshold.
- Cooldown after risk must change at least one behavior parameter: delay, worker start, vendor state, or scheduler choice.
- Deadline termination must gracefully export available rows and emit a machine-readable summary.
- Partial output is preferred over running indefinitely.
- Use existing event style: `Event{ "event": "...", "stage": "..." }`.
- Match existing Go style: package `tripcdp`, table-driven tests where useful, no unnecessary dependencies.

---

## File Structure

### Create

- `internal/tripcdp/failure_class.go`  
  Defines `FailureClass`, `FailureDecision`, row/event classification helpers, and retry/rerun eligibility.

- `internal/tripcdp/failure_class_test.go`  
  Unit tests for classifying resource miss, risk block, page-not-ready, browser/CDP errors, and business unavailable cases.

- `internal/tripcdp/runtime_policy.go`  
  Defines runtime resilience options, defaults, deadline parsing, and helper methods for budgets/thresholds.

- `internal/tripcdp/runtime_policy_test.go`  
  Unit tests for defaults, CLI parsing, deadline parsing, and disabled deadline behavior.

- `internal/tripcdp/pressure_controller.go`  
  Maintains global/vendor pressure scores and mode transitions: `normal`, `cautious`, `safe`, `recovery`, `terminate`.

- `internal/tripcdp/pressure_controller_test.go`  
  Tests pressure score increments/decrements, mode transitions, and effective worker/delay behavior.

- `internal/tripcdp/vendor_scheduler.go`  
  Owns vendor queue state: `active`, `cooling`, `degraded`, `api_blocked`, `paused`, `done`, and returns next schedulable vendor.

- `internal/tripcdp/vendor_scheduler_test.go`  
  Tests vendor cooling, API-block skipping, pressure-aware selection, and deadline-near prioritization.

- `internal/tripcdp/runtime_summary.go`  
  Emits final JSONL summary events and writes `_diagnostics/runtime_summary.jsonl`.

- `internal/tripcdp/runtime_summary_test.go`  
  Tests summary payloads for done, partial, skipped, deadline, and API-blocked vendors.

- `internal/tripcdp/watchdog.go`  
  Tracks worker progress heartbeat and provides cancellable stall detection.

- `internal/tripcdp/watchdog_test.go`  
  Unit tests for progress updates, timeout detection, and no false positive before timeout.

### Modify

- `internal/tripcdp/types.go`  
  Add `RuntimeOptions` to `TaskRequest`; add runtime summary fields to `TaskResult` and `workerResult`.

- `internal/tripcdp/cli.go`  
  Add CLI flags for deadline, timeout/budget thresholds, and resilience mode defaults.

- `internal/tripcdp/quality_rerun.go`  
  Filter rerun candidates through failure classification so suspected API blocks do not launch quality rerun Chrome.

- `internal/tripcdp/browser.go`  
  Add per-URL timeout context, classify quality failure before deciding reload/restart, emit failure class in events, and avoid restart for API-block-suspected cases.

- `internal/tripcdp/runner.go`  
  Integrate deadline context, watchdog, pressure controller, vendor scheduler, vendor/global thresholds, and final runtime summary.

- `scripts/hermes-cron/trip_sku_daily_launcher.py`  
  Add new runtime safety flags to the scheduled command after CLI support exists.

- Existing tests: `internal/tripcdp/cli_test.go`, `internal/tripcdp/quality_rerun_test.go`, `internal/tripcdp/risk_test.go`, `internal/tripcdp/runner_test.go`  
  Extend for new options and changed behavior.

---

### Task 1: Add Failure Classification Core

**Files:**
- Create: `internal/tripcdp/failure_class.go`
- Create: `internal/tripcdp/failure_class_test.go`
- Modify: `internal/tripcdp/types.go`

**Interfaces:**
- Produces: `type FailureClass string`
- Produces: `func ClassifyError(err error, message string) FailureClass`
- Produces: `func ClassifyQualityFailure(reason string, trustedRows int, untrustedRows int, resourceMissRows int, totalRows int, diagnostics productPackageDiagnostics) FailureClass`
- Produces: `func ClassifyRows(rows []map[string]string) FailureClass`
- Produces: `func FailureClassRetryableByQualityRerun(class FailureClass) bool`
- Produces: `func FailureClassAllowsBrowserRestart(class FailureClass) bool`
- Consumes: existing price status constants in `browser.go` and existing `productPackageDiagnostics`.

- [ ] **Step 1: Write failing classification tests**

Add `internal/tripcdp/failure_class_test.go`:

```go
package tripcdp

import (
	"errors"
	"testing"
)

func TestClassifyQualityFailureResourceMissAsAPISuspected(t *testing.T) {
	class := ClassifyQualityFailure(
		"resource_miss_rate",
		0,
		12,
		10,
		12,
		productPackageDiagnostics{NextDataPresent: true, PackageCount: 4, ResourceCount: 4, BatchResponseCount: 12, LastResponseStatus: 200},
	)
	if class != FailureAPIBlockedSuspected {
		t.Fatalf("class = %q, want %q", class, FailureAPIBlockedSuspected)
	}
	if FailureClassRetryableByQualityRerun(class) {
		t.Fatal("api_blocked_suspected must not be quality-rerun retryable")
	}
	if FailureClassAllowsBrowserRestart(class) {
		t.Fatal("api_blocked_suspected must not allow browser restart recovery")
	}
}

func TestClassifyQualityFailurePageNotReady(t *testing.T) {
	class := ClassifyQualityFailure(
		"page_not_ready",
		0,
		1,
		0,
		1,
		productPackageDiagnostics{},
	)
	if class != FailurePageNotReady {
		t.Fatalf("class = %q, want %q", class, FailurePageNotReady)
	}
	if !FailureClassRetryableByQualityRerun(class) {
		t.Fatal("page_not_ready should be quality-rerun retryable")
	}
}

func TestClassifyRowsRiskBlocked(t *testing.T) {
	rows := []map[string]string{{"价格状态": priceStatusRiskBlocked, "抓取备注": "whaleguard block"}}
	if class := ClassifyRows(rows); class != FailureRiskChallenge {
		t.Fatalf("class = %q, want %q", class, FailureRiskChallenge)
	}
}

func TestClassifyRowsBusinessUnavailableForSingleResourceMissWithoutDiagnostics(t *testing.T) {
	rows := []map[string]string{{"价格状态": priceStatusUntrustedResourceMiss, "资源ID": "0", "接口ItemID": "0", "抓取备注": "价格质量检查失败: consecutive_resource_miss"}}
	if class := ClassifyRows(rows); class != FailureBusinessUnavailable {
		t.Fatalf("class = %q, want %q", class, FailureBusinessUnavailable)
	}
}

func TestClassifyErrorBrowserFailure(t *testing.T) {
	class := ClassifyError(errors.New("use of closed network connection"), "")
	if class != FailureBrowserFailure {
		t.Fatalf("class = %q, want %q", class, FailureBrowserFailure)
	}
}

func TestClassifyErrorNavigationBlocked(t *testing.T) {
	class := ClassifyError(nil, "navigation failed: net::ERR_ABORTED")
	if class != FailureNavigationBlocked {
		t.Fatalf("class = %q, want %q", class, FailureNavigationBlocked)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run:

```bash
go test ./internal/tripcdp -run 'TestClassify' -count=1
```

Expected: FAIL because `FailureClass` and classifier functions are undefined.

- [ ] **Step 3: Implement minimal classifier**

Create `internal/tripcdp/failure_class.go`:

```go
package tripcdp

import "strings"

type FailureClass string

const (
	FailureNone                FailureClass = "none"
	FailureBrowserFailure      FailureClass = "browser_failure"
	FailureWorkerStalled       FailureClass = "worker_stalled"
	FailureNavigationBlocked   FailureClass = "navigation_blocked"
	FailureRiskChallenge       FailureClass = "risk_challenge"
	FailureAPIBlockedSuspected FailureClass = "api_blocked_suspected"
	FailureBusinessUnavailable FailureClass = "business_unavailable"
	FailurePageNotReady        FailureClass = "page_not_ready"
	FailureDataQualityLow      FailureClass = "data_quality_low"
)

func ClassifyError(err error, message string) FailureClass {
	text := strings.ToLower(strings.TrimSpace(message))
	if err != nil {
		text = strings.TrimSpace(text + " " + strings.ToLower(err.Error()))
	}
	switch {
	case text == "":
		return FailureNone
	case strings.Contains(text, "closed network connection"), strings.Contains(text, "websocket"), strings.Contains(text, "context canceled"):
		return FailureBrowserFailure
	case strings.Contains(text, "err_aborted"), strings.Contains(text, "navigation failed"), strings.Contains(text, "tcp"):
		return FailureNavigationBlocked
	case strings.Contains(text, "whaleguard"), strings.Contains(text, "cloudflare"), strings.Contains(text, "captcha"):
		return FailureRiskChallenge
	case strings.Contains(text, "页面未加载"), strings.Contains(text, "page not ready"), strings.Contains(text, "product dom not ready"):
		return FailurePageNotReady
	default:
		return FailureDataQualityLow
	}
}

func ClassifyQualityFailure(reason string, trustedRows int, untrustedRows int, resourceMissRows int, totalRows int, diagnostics productPackageDiagnostics) FailureClass {
	reason = strings.TrimSpace(reason)
	if trustedRows > 0 {
		return FailureNone
	}
	if reason == "page_not_ready" {
		return FailurePageNotReady
	}
	if reason == "consecutive_resource_miss" || reason == "resource_miss_rate" {
		if totalRows >= minSamplesForResourceRate && resourceMissRows > 0 && diagnostics.NextDataPresent && diagnostics.PackageCount > 0 && diagnostics.BatchResponseCount > 0 {
			return FailureAPIBlockedSuspected
		}
		return FailureBusinessUnavailable
	}
	if untrustedRows > 0 || totalRows > 0 {
		return FailureDataQualityLow
	}
	return FailureNone
}

func ClassifyRows(rows []map[string]string) FailureClass {
	if len(rows) == 0 {
		return FailureNone
	}
	trusted, untrusted, resourceMiss := 0, 0, 0
	for _, row := range rows {
		status := strings.TrimSpace(row["价格状态"])
		note := strings.ToLower(strings.TrimSpace(row["抓取备注"] + " " + row["评分"]))
		if isTrustedPriceStatus(status) {
			trusted++
			continue
		}
		if status != "" {
			untrusted++
		}
		if status == priceStatusRiskBlocked || strings.Contains(note, "whaleguard") || strings.Contains(note, "cloudflare") {
			return FailureRiskChallenge
		}
		if status == priceStatusUntrustedResourceMiss || strings.Contains(note, "resource_miss") || strings.Contains(note, "价格质量检查失败") {
			resourceMiss++
		}
		if strings.Contains(note, "页面未加载") {
			return FailurePageNotReady
		}
	}
	if trusted > 0 {
		return FailureNone
	}
	if resourceMiss >= minSamplesForResourceRate && untrusted > 0 {
		return FailureAPIBlockedSuspected
	}
	if resourceMiss > 0 {
		return FailureBusinessUnavailable
	}
	if untrusted > 0 {
		return FailureDataQualityLow
	}
	return FailureNone
}

func FailureClassRetryableByQualityRerun(class FailureClass) bool {
	switch class {
	case FailurePageNotReady, FailureDataQualityLow, FailureNavigationBlocked, FailureBrowserFailure:
		return true
	default:
		return false
	}
}

func FailureClassAllowsBrowserRestart(class FailureClass) bool {
	switch class {
	case FailureBrowserFailure, FailureWorkerStalled, FailureNavigationBlocked, FailurePageNotReady:
		return true
	default:
		return false
	}
}
```

- [ ] **Step 4: Add type fields for later integration**

Modify `internal/tripcdp/types.go`:

```go
type TaskRequest struct {
	InputPaths          []string
	URLsFile            string
	ExcelInput          string
	OutputPath          string
	OutputDir           string
	LogFile             string
	SessionFileExplicit bool
	Workers             int
	StartupStagger      time.Duration
	StopGraceSeconds    int
	RatingOnly          bool
	ExtractRealPrice    bool
	ExtractProductInfo  bool
	DelayOptions        DelayOptions
	PriceQualityRetry   PriceQualityRetryOptions
	QualityRerun        QualityRerunOptions
	Runtime             RuntimeOptions
	BrowserOptions      BrowserOptions
	CompareVendor       string // If set, run vendor comparison against this base vendor after scrape
}
```

`RuntimeOptions` is implemented in Task 2. If Go fails before Task 2, add this temporary stub at the bottom of `types.go` and remove it when Task 2 creates the real type:

```go
type RuntimeOptions struct{}
```

- [ ] **Step 5: Run tests**

Run:

```bash
go test ./internal/tripcdp -run 'TestClassify' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/tripcdp/failure_class.go internal/tripcdp/failure_class_test.go internal/tripcdp/types.go
git commit -m "feat: add trip runtime failure classification"
```

---

### Task 2: Add Runtime Policy and CLI Flags

**Files:**
- Create: `internal/tripcdp/runtime_policy.go`
- Create: `internal/tripcdp/runtime_policy_test.go`
- Modify: `internal/tripcdp/types.go`
- Modify: `internal/tripcdp/cli.go`
- Modify: `internal/tripcdp/cli_test.go`

**Interfaces:**
- Produces: `type RuntimeOptions struct`
- Produces: `func defaultRuntimeOptions() RuntimeOptions`
- Produces: `func parseLocalDeadline(value string, now time.Time) (time.Time, bool, error)`
- Consumes: `TaskRequest.Runtime` from Task 1.

- [ ] **Step 1: Write failing runtime policy tests**

Create `internal/tripcdp/runtime_policy_test.go`:

```go
package tripcdp

import (
	"testing"
	"time"
)

func TestDefaultRuntimeOptions(t *testing.T) {
	options := defaultRuntimeOptions()
	if options.DeadlineHHMM != "0530" {
		t.Fatalf("DeadlineHHMM = %q, want 0530", options.DeadlineHHMM)
	}
	if options.URLTotalTimeout != 300*time.Second {
		t.Fatalf("URLTotalTimeout = %s", options.URLTotalTimeout)
	}
	if options.WorkerStallTimeout != 10*time.Minute {
		t.Fatalf("WorkerStallTimeout = %s", options.WorkerStallTimeout)
	}
	if options.VendorAPIBlockRatio != 0.70 {
		t.Fatalf("VendorAPIBlockRatio = %.2f", options.VendorAPIBlockRatio)
	}
}

func TestParseLocalDeadlineSameDay(t *testing.T) {
	now := time.Date(2026, 7, 8, 0, 15, 0, 0, time.Local)
	deadline, ok, err := parseLocalDeadline("0530", now)
	if err != nil || !ok {
		t.Fatalf("deadline parse failed: ok=%v err=%v", ok, err)
	}
	want := time.Date(2026, 7, 8, 5, 30, 0, 0, time.Local)
	if !deadline.Equal(want) {
		t.Fatalf("deadline = %s, want %s", deadline, want)
	}
}

func TestParseLocalDeadlineDisabled(t *testing.T) {
	_, ok, err := parseLocalDeadline("", time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("empty deadline should be disabled")
	}
}

func TestParseLocalDeadlineRejectsInvalid(t *testing.T) {
	_, _, err := parseLocalDeadline("25:99", time.Now())
	if err == nil {
		t.Fatal("invalid deadline should fail")
	}
}
```

- [ ] **Step 2: Extend CLI test**

Add to `internal/tripcdp/cli_test.go` a test using existing style:

```go
func TestParseArgsRuntimeOptions(t *testing.T) {
	request, _, _, _, _, err := parseArgs([]string{
		"--input", "https://hk.trip.com/things-to-do/detail/1/",
		"--deadline", "0530",
		"--url-total-timeout", "240",
		"--navigation-timeout", "90",
		"--worker-stall-timeout", "600",
		"--max-quality-retries", "1",
		"--max-browser-restarts-per-vendor", "2",
		"--vendor-api-block-ratio", "0.75",
		"--global-api-block-ratio", "0.65",
	}, io.Discard)
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if request.Runtime.DeadlineHHMM != "0530" {
		t.Fatalf("deadline = %q", request.Runtime.DeadlineHHMM)
	}
	if request.Runtime.URLTotalTimeout != 240*time.Second {
		t.Fatalf("url timeout = %s", request.Runtime.URLTotalTimeout)
	}
	if request.Runtime.MaxQualityRetriesPerURL != 1 {
		t.Fatalf("max quality retries = %d", request.Runtime.MaxQualityRetriesPerURL)
	}
	if request.Runtime.VendorAPIBlockRatio != 0.75 {
		t.Fatalf("vendor ratio = %.2f", request.Runtime.VendorAPIBlockRatio)
	}
}
```

If `cli_test.go` does not already import `io` and `time`, add them.

- [ ] **Step 3: Run tests to verify failure**

```bash
go test ./internal/tripcdp -run 'TestDefaultRuntimeOptions|TestParseLocalDeadline|TestParseArgsRuntimeOptions' -count=1
```

Expected: FAIL because `RuntimeOptions`, parser, and flags do not exist.

- [ ] **Step 4: Implement runtime policy**

Create `internal/tripcdp/runtime_policy.go`:

```go
package tripcdp

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type RuntimeOptions struct {
	DeadlineHHMM                string
	URLTotalTimeout             time.Duration
	NavigationTimeout           time.Duration
	WorkerStallTimeout          time.Duration
	MaxQualityRetriesPerURL     int
	MaxBrowserRestartsPerVendor int
	VendorAPIBlockRatio         float64
	VendorAPIBlockMinAttempts   int
	GlobalAPIBlockRatio         float64
	GlobalAPIBlockMinProcessed  int
	FastURLThreshold            time.Duration
	RiskToSafeThreshold         int
	CheckpointEveryURLs         int
}

func defaultRuntimeOptions() RuntimeOptions {
	return RuntimeOptions{
		DeadlineHHMM:                "0530",
		URLTotalTimeout:             300 * time.Second,
		NavigationTimeout:           120 * time.Second,
		WorkerStallTimeout:          10 * time.Minute,
		MaxQualityRetriesPerURL:     2,
		MaxBrowserRestartsPerVendor: 2,
		VendorAPIBlockRatio:         0.70,
		VendorAPIBlockMinAttempts:   10,
		GlobalAPIBlockRatio:         0.60,
		GlobalAPIBlockMinProcessed:  50,
		FastURLThreshold:            30 * time.Second,
		RiskToSafeThreshold:         10,
		CheckpointEveryURLs:         10,
	}
}

func parseLocalDeadline(value string, now time.Time) (time.Time, bool, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "none" || value == "off" {
		return time.Time{}, false, nil
	}
	value = strings.ReplaceAll(value, ":", "")
	if len(value) != 4 {
		return time.Time{}, false, fmt.Errorf("deadline must be HHMM, HH:MM, empty, none, or off")
	}
	hour, err := strconv.Atoi(value[:2])
	if err != nil {
		return time.Time{}, false, fmt.Errorf("deadline hour must be numeric")
	}
	minute, err := strconv.Atoi(value[2:])
	if err != nil {
		return time.Time{}, false, fmt.Errorf("deadline minute must be numeric")
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return time.Time{}, false, fmt.Errorf("deadline must be a valid local time")
	}
	return time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location()), true, nil
}

func ratioReached(count int, total int, ratio float64, minTotal int) bool {
	if total < minTotal || total <= 0 {
		return false
	}
	return float64(count)/float64(total) >= ratio
}
```

Remove any temporary `type RuntimeOptions struct{}` from Task 1.

- [ ] **Step 5: Add CLI flags and request wiring**

Modify `parseArgs` in `internal/tripcdp/cli.go`:

Add variables near existing flag variables:

```go
	runtimeOptions := defaultRuntimeOptions()
	var urlTotalTimeoutSeconds int
	var navigationTimeoutSeconds int
	var workerStallTimeoutSeconds int
	var fastURLThresholdSeconds int
```

Initialize after `runtimeOptions`:

```go
	urlTotalTimeoutSeconds = int(runtimeOptions.URLTotalTimeout / time.Second)
	navigationTimeoutSeconds = int(runtimeOptions.NavigationTimeout / time.Second)
	workerStallTimeoutSeconds = int(runtimeOptions.WorkerStallTimeout / time.Second)
	fastURLThresholdSeconds = int(runtimeOptions.FastURLThreshold / time.Second)
```

Add flags after quality rerun flags:

```go
	fs.StringVar(&runtimeOptions.DeadlineHHMM, "deadline", runtimeOptions.DeadlineHHMM, "Local hard deadline as HHMM or HH:MM. Use none/off/empty to disable.")
	fs.IntVar(&urlTotalTimeoutSeconds, "url-total-timeout", urlTotalTimeoutSeconds, "Maximum seconds for one URL including SKU traversal.")
	fs.IntVar(&navigationTimeoutSeconds, "navigation-timeout", navigationTimeoutSeconds, "Maximum seconds for one navigation attempt.")
	fs.IntVar(&workerStallTimeoutSeconds, "worker-stall-timeout", workerStallTimeoutSeconds, "Seconds without worker progress before the worker is considered stalled.")
	fs.IntVar(&runtimeOptions.MaxQualityRetriesPerURL, "max-quality-retries", runtimeOptions.MaxQualityRetriesPerURL, "Maximum quality retry attempts per URL.")
	fs.IntVar(&runtimeOptions.MaxBrowserRestartsPerVendor, "max-browser-restarts-per-vendor", runtimeOptions.MaxBrowserRestartsPerVendor, "Maximum browser restarts per vendor before failing that vendor.")
	fs.Float64Var(&runtimeOptions.VendorAPIBlockRatio, "vendor-api-block-ratio", runtimeOptions.VendorAPIBlockRatio, "Vendor API-block ratio threshold after minimum attempts.")
	fs.IntVar(&runtimeOptions.VendorAPIBlockMinAttempts, "vendor-api-block-min-attempts", runtimeOptions.VendorAPIBlockMinAttempts, "Minimum attempted URLs before vendor API-block ratio applies.")
	fs.Float64Var(&runtimeOptions.GlobalAPIBlockRatio, "global-api-block-ratio", runtimeOptions.GlobalAPIBlockRatio, "Global API-block ratio threshold after minimum processed URLs.")
	fs.IntVar(&runtimeOptions.GlobalAPIBlockMinProcessed, "global-api-block-min-processed", runtimeOptions.GlobalAPIBlockMinProcessed, "Minimum processed URLs before global API-block ratio applies.")
	fs.IntVar(&fastURLThresholdSeconds, "fast-url-threshold", fastURLThresholdSeconds, "URL elapsed seconds below this value increases anti-bot pressure.")
	fs.IntVar(&runtimeOptions.RiskToSafeThreshold, "risk-to-safe-threshold", runtimeOptions.RiskToSafeThreshold, "Consecutive risk events before safe mode.")
	fs.IntVar(&runtimeOptions.CheckpointEveryURLs, "checkpoint-every-urls", runtimeOptions.CheckpointEveryURLs, "Write partial checkpoint after this many URLs per vendor; 0 disables URL checkpoints.")
```

After argument validation, add:

```go
	if _, _, err := parseLocalDeadline(runtimeOptions.DeadlineHHMM, time.Now()); err != nil {
		return TaskRequest{}, "", "", false, false, err
	}
	if urlTotalTimeoutSeconds < 1 {
		return TaskRequest{}, "", "", false, false, fmt.Errorf("--url-total-timeout must be >= 1")
	}
	if navigationTimeoutSeconds < 1 {
		return TaskRequest{}, "", "", false, false, fmt.Errorf("--navigation-timeout must be >= 1")
	}
	if workerStallTimeoutSeconds < 1 {
		return TaskRequest{}, "", "", false, false, fmt.Errorf("--worker-stall-timeout must be >= 1")
	}
	if runtimeOptions.MaxQualityRetriesPerURL < 0 {
		return TaskRequest{}, "", "", false, false, fmt.Errorf("--max-quality-retries must be >= 0")
	}
	if runtimeOptions.MaxBrowserRestartsPerVendor < 0 {
		return TaskRequest{}, "", "", false, false, fmt.Errorf("--max-browser-restarts-per-vendor must be >= 0")
	}
	if runtimeOptions.VendorAPIBlockRatio <= 0 || runtimeOptions.VendorAPIBlockRatio > 1 {
		return TaskRequest{}, "", "", false, false, fmt.Errorf("--vendor-api-block-ratio must be > 0 and <= 1")
	}
	if runtimeOptions.GlobalAPIBlockRatio <= 0 || runtimeOptions.GlobalAPIBlockRatio > 1 {
		return TaskRequest{}, "", "", false, false, fmt.Errorf("--global-api-block-ratio must be > 0 and <= 1")
	}
	runtimeOptions.URLTotalTimeout = time.Duration(urlTotalTimeoutSeconds) * time.Second
	runtimeOptions.NavigationTimeout = time.Duration(navigationTimeoutSeconds) * time.Second
	runtimeOptions.WorkerStallTimeout = time.Duration(workerStallTimeoutSeconds) * time.Second
	runtimeOptions.FastURLThreshold = time.Duration(fastURLThresholdSeconds) * time.Second
```

In returned `TaskRequest`, set:

```go
			Runtime: runtimeOptions,
```

- [ ] **Step 6: Run tests**

```bash
go test ./internal/tripcdp -run 'TestDefaultRuntimeOptions|TestParseLocalDeadline|TestParseArgsRuntimeOptions' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/tripcdp/runtime_policy.go internal/tripcdp/runtime_policy_test.go internal/tripcdp/types.go internal/tripcdp/cli.go internal/tripcdp/cli_test.go
git commit -m "feat: add runtime resilience policy options"
```

---

### Task 3: Split Quality Rerun by Failure Class

**Files:**
- Modify: `internal/tripcdp/quality_rerun.go`
- Modify: `internal/tripcdp/quality_rerun_test.go`

**Interfaces:**
- Consumes: `ClassifyRows`, `FailureClassRetryableByQualityRerun` from Task 1.
- Produces: `FailureClass` on `qualityRerunCandidate`.

- [ ] **Step 1: Write failing tests for API-block filtering**

Append to `internal/tripcdp/quality_rerun_test.go`:

```go
func TestQualityRerunCandidatesSkipAPISuspectedResourceMiss(t *testing.T) {
	rows := []map[string]string{}
	for i := 0; i < minSamplesForResourceRate; i++ {
		row := map[string]string{
			"厂商":   "BC",
			"商品ID": "1",
			"URL":  "https://hk.trip.com/things-to-do/detail/1/",
			"价格状态": priceStatusUntrustedResourceMiss,
			"资源ID":  "0",
			"接口ItemID": "0",
			"抓取备注": "价格质量检查失败: resource_miss_rate",
		}
		if i > 0 {
			row["厂商"] = ""
			row["商品ID"] = ""
			row["URL"] = ""
		}
		rows = append(rows, row)
	}

	candidates := collectQualityRerunCandidates(rows)
	if len(candidates) != 0 {
		t.Fatalf("api-block suspected rows should not be quality rerun candidates: %+v", candidates)
	}
}

func TestQualityRerunCandidatesKeepPageNotReady(t *testing.T) {
	rows := []map[string]string{{"厂商": "BC", "商品ID": "2", "URL": "https://hk.trip.com/things-to-do/detail/2/", "抓取备注": "页面未加载有效商品内容"}}
	candidates := collectQualityRerunCandidates(rows)
	if len(candidates) != 1 {
		t.Fatalf("page-not-ready rows should be rerun candidates: %+v", candidates)
	}
	if candidates[0].FailureClass != FailurePageNotReady {
		t.Fatalf("class = %q", candidates[0].FailureClass)
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

```bash
go test ./internal/tripcdp -run 'TestQualityRerunCandidates' -count=1
```

Expected: FAIL because `FailureClass` is not on candidate and filtering is not implemented.

- [ ] **Step 3: Modify candidate type and filtering**

In `internal/tripcdp/quality_rerun.go`, update `qualityRerunCandidate`:

```go
type qualityRerunCandidate struct {
	URL          string
	Vendor       string
	ProductID    string
	Reason       string
	FailureClass FailureClass
}
```

Update `collectQualityRerunCandidates` block:

```go
		if score.needsRerun {
			failureClass := ClassifyRows(block)
			if !FailureClassRetryableByQualityRerun(failureClass) {
				continue
			}
			seen[normalizeURL(url)] = true
			candidates = append(candidates, qualityRerunCandidate{
				URL:          url,
				Vendor:       firstNonEmptyRowValue(block, "厂商"),
				ProductID:    firstNonEmptyRowValue(block, "商品ID"),
				Reason:       score.reason,
				FailureClass: failureClass,
			})
		}
```

Update `quality_rerun_started` event in `runQualityReruns`:

```go
reporter(Event{"event": "quality_rerun_started", "stage": "quality_rerun", "vendor": vendor, "url": candidate.URL, "product_id": candidate.ProductID, "reason": candidate.Reason, "failure_class": candidate.FailureClass, "url_index": index + 1, "url_total": len(candidates), "headless": rerunRequest.BrowserOptions.Headless, "delay_profile": rerunRequest.DelayOptions.Profile})
```

- [ ] **Step 4: Ensure page-not-ready rows need rerun**

In `qualityScoreRows`, include page-not-ready as `hasError`:

```go
if strings.Contains(note, "处理错误") || strings.Contains(note, "页面未加载") || strings.Contains(note, "product dom not ready") || strings.Contains(note, "whaleguard") || strings.Contains(note, "cloudflare") || strings.Contains(note, "价格质量检查失败") {
	hasError = true
}
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/tripcdp -run 'TestQualityRerunCandidates|TestClassify' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/tripcdp/quality_rerun.go internal/tripcdp/quality_rerun_test.go
git commit -m "fix: split quality reruns by failure class"
```

---

### Task 4: Prevent API-Blocked Price Quality Restart Loops

**Files:**
- Modify: `internal/tripcdp/browser.go`
- Create or extend: `internal/tripcdp/page_health_test.go` or `internal/tripcdp/quality_rerun_test.go`

**Interfaces:**
- Consumes: `ClassifyQualityFailure`, `FailureClassAllowsBrowserRestart`.
- Produces: `priceQualityTracker.failureClass FailureClass`.

- [ ] **Step 1: Add unit test for retryability decision**

Create `internal/tripcdp/price_quality_retry_test.go`:

```go
package tripcdp

import "testing"

func TestShouldRetryPriceQualityRejectsAPISuspected(t *testing.T) {
	s := newTripScraper(TaskRequest{
		PriceQualityRetry: PriceQualityRetryOptions{MaxRetries: 2},
		Runtime:           defaultRuntimeOptions(),
	}, nil, "BC", 1, 1)
	quality := &priceQualityTracker{
		failed:           true,
		failReason:       "resource_miss_rate",
		trustedRows:      0,
		untrustedRows:    12,
		resourceMissRows: 12,
		totalRows:        12,
		diagnostics:      productPackageDiagnostics{NextDataPresent: true, PackageCount: 4, ResourceCount: 4, BatchResponseCount: 12, LastResponseStatus: 200},
	}
	if s.shouldRetryPriceQuality(quality, 0, 0) {
		t.Fatal("api-block suspected quality failure must not be retried")
	}
}

func TestShouldRetryPriceQualityAllowsDataQualityLowWithinBudget(t *testing.T) {
	s := newTripScraper(TaskRequest{
		PriceQualityRetry: PriceQualityRetryOptions{MaxRetries: 2},
		Runtime:           defaultRuntimeOptions(),
	}, nil, "BC", 1, 1)
	quality := &priceQualityTracker{failed: true, failReason: "same_fallback_price_streak", trustedRows: 0, untrustedRows: 8, totalRows: 8}
	if !s.shouldRetryPriceQuality(quality, 0, 0) {
		t.Fatal("data quality low should be retried within budget")
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

```bash
go test ./internal/tripcdp -run 'TestShouldRetryPriceQuality' -count=1
```

Expected: FAIL if `priceQualityTracker` field names differ or retry still allows API-suspected. Inspect `priceQualityTracker` definition in `browser.go`; adjust test fields to exact names if needed.

- [ ] **Step 3: Add failure class to tracker**

Find `type priceQualityTracker struct` in `browser.go` and add:

```go
	failureClass FailureClass
```

- [ ] **Step 4: Classify before retry**

Modify `shouldRetryPriceQuality`:

```go
func (s *tripScraper) shouldRetryPriceQuality(quality *priceQualityTracker, trustedRows int, retriesUsed int) bool {
	if s == nil || s.request.RatingOnly || quality == nil || !quality.failed || trustedRows > 0 {
		return false
	}
	class := quality.failureClass
	if class == "" || class == FailureNone {
		class = ClassifyQualityFailure(quality.failReason, quality.trustedRows, quality.untrustedRows, quality.resourceMissRows, quality.totalRows, quality.diagnostics)
		quality.failureClass = class
	}
	if !FailureClassRetryableByQualityRerun(class) || !priceQualityReasonRetryable(quality.failReason) {
		return false
	}
	limit := s.request.PriceQualityRetry.MaxRetries
	if s.request.Runtime.MaxQualityRetriesPerURL >= 0 && s.request.Runtime.MaxQualityRetriesPerURL < limit {
		limit = s.request.Runtime.MaxQualityRetriesPerURL
	}
	if retriesUsed >= max(0, limit) {
		return false
	}
	return true
}
```

If `RuntimeOptions` zero value appears in tests, make `defaultRuntimeOptions()` applied in `parseArgs`; for direct tests, pass `Runtime: defaultRuntimeOptions()`.

- [ ] **Step 5: Include failure class in quality events**

In `emitURLQualityFailed`, compute and include:

```go
	failureClass := quality.failureClass
	if failureClass == "" || failureClass == FailureNone {
		failureClass = ClassifyQualityFailure(quality.failReason, quality.trustedRows, quality.untrustedRows, quality.resourceMissRows, quality.totalRows, quality.diagnostics)
		quality.failureClass = failureClass
	}
```

Add to emitted event:

```go
			"failure_class":        failureClass,
```

In `price_quality_retry` event inside `scrapeOne`, add:

```go
"failure_class": qualityFailure.failureClass,
```

- [ ] **Step 6: Prevent restart-after-url for API-suspected extraction quality failure**

In `extractRows`, replace:

```go
	if quality != nil && quality.failed {
		s.currentQualityFailure = quality
		s.restartAfterURL = true
		s.restartReason = "price quality failed: " + quality.failReason
	}
```

with:

```go
	if quality != nil && quality.failed {
		quality.failureClass = ClassifyQualityFailure(quality.failReason, quality.trustedRows, quality.untrustedRows, quality.resourceMissRows, quality.totalRows, quality.diagnostics)
		s.currentQualityFailure = quality
		if FailureClassAllowsBrowserRestart(quality.failureClass) {
			s.restartAfterURL = true
			s.restartReason = "price quality failed: " + quality.failReason
		}
	}
```

- [ ] **Step 7: Run targeted tests**

```bash
go test ./internal/tripcdp -run 'TestShouldRetryPriceQuality|TestQualityRerunCandidates|TestClassifyQualityFailure' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/tripcdp/browser.go internal/tripcdp/price_quality_retry_test.go
git commit -m "fix: avoid browser restart loops for suspected API blocks"
```

---

### Task 5: Add Worker Watchdog and Per-URL Timeout

**Files:**
- Create: `internal/tripcdp/watchdog.go`
- Create: `internal/tripcdp/watchdog_test.go`
- Modify: `internal/tripcdp/runner.go`
- Modify: `internal/tripcdp/browser.go`

**Interfaces:**
- Produces: `type workerProgressWatchdog struct`
- Produces: `func newWorkerProgressWatchdog(timeout time.Duration, reporter Reporter, vendor string, workerID int, cancel context.CancelFunc) *workerProgressWatchdog`
- Produces: `func (w *workerProgressWatchdog) mark(event string)`
- Produces: `func (w *workerProgressWatchdog) stop()`
- Consumes: `TaskRequest.Runtime.WorkerStallTimeout` and `TaskRequest.Runtime.URLTotalTimeout`.

- [ ] **Step 1: Write watchdog tests**

Create `internal/tripcdp/watchdog_test.go`:

```go
package tripcdp

import (
	"context"
	"testing"
	"time"
)

func TestWorkerProgressWatchdogCancelsAfterStall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watchdog := newWorkerProgressWatchdog(20*time.Millisecond, nil, "BC", 1, cancel)
	defer watchdog.stop()
	select {
	case <-ctx.Done():
	case <-time.After(200 * time.Millisecond):
		t.Fatal("watchdog did not cancel stalled context")
	}
}

func TestWorkerProgressWatchdogMarkPreventsEarlyCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watchdog := newWorkerProgressWatchdog(80*time.Millisecond, nil, "BC", 1, cancel)
	defer watchdog.stop()
	watchdog.mark("url_started")
	time.Sleep(30 * time.Millisecond)
	watchdog.mark("url_done")
	select {
	case <-ctx.Done():
		t.Fatal("watchdog canceled too early")
	case <-time.After(30 * time.Millisecond):
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

```bash
go test ./internal/tripcdp -run 'TestWorkerProgressWatchdog' -count=1
```

Expected: FAIL because watchdog does not exist.

- [ ] **Step 3: Implement watchdog**

Create `internal/tripcdp/watchdog.go`:

```go
package tripcdp

import (
	"sync"
	"time"
)

type workerProgressWatchdog struct {
	mu       sync.Mutex
	timeout  time.Duration
	last     time.Time
	stopped  chan struct{}
	reporter Reporter
	vendor   string
	workerID int
	cancel   func()
}

func newWorkerProgressWatchdog(timeout time.Duration, reporter Reporter, vendor string, workerID int, cancel func()) *workerProgressWatchdog {
	w := &workerProgressWatchdog{timeout: timeout, last: time.Now(), stopped: make(chan struct{}), reporter: reporter, vendor: vendor, workerID: workerID, cancel: cancel}
	if timeout <= 0 || cancel == nil {
		return w
	}
	go w.loop()
	return w
}

func (w *workerProgressWatchdog) mark(event string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.last = time.Now()
	w.mu.Unlock()
}

func (w *workerProgressWatchdog) stop() {
	if w == nil {
		return
	}
	select {
	case <-w.stopped:
	default:
		close(w.stopped)
	}
}

func (w *workerProgressWatchdog) loop() {
	ticker := time.NewTicker(maxDuration(w.timeout/4, 10*time.Millisecond))
	defer ticker.Stop()
	for {
		select {
		case <-w.stopped:
			return
		case <-ticker.C:
			w.mu.Lock()
			stalled := time.Since(w.last) >= w.timeout
			w.mu.Unlock()
			if stalled {
				if w.reporter != nil {
					w.reporter(Event{"event": "worker_stalled", "stage": "watchdog", "vendor": w.vendor, "worker_id": w.workerID, "failure_class": FailureWorkerStalled, "stall_timeout_ms": w.timeout.Milliseconds()})
				}
				w.cancel()
				return
			}
		}
	}
}

func maxDuration(a time.Duration, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}
```

- [ ] **Step 4: Integrate watchdog in `runVendorWorker`**

In `runVendorWorker`, create child context and watchdog near the top:

```go
	workerCtx, workerCancel := context.WithCancel(ctx)
	defer workerCancel()
	watchdog := newWorkerProgressWatchdog(request.Runtime.WorkerStallTimeout, reporter, group.Vendor, workerID, workerCancel)
	defer watchdog.stop()
```

Use `workerCtx` instead of `ctx` for `scraper.start`, `scraper.scrapeOne`, `maybeRestart`, and `runQualityReruns`.

Inside `localReporter`, after setting event fields:

```go
		watchdog.mark(fmt.Sprint(event["event"]))
```

If `watchdog` is declared after `localReporter`, move localReporter definition after watchdog creation.

- [ ] **Step 5: Add per-URL timeout in `scrapeOne`**

At start of `scrapeOne` after `started := time.Now()`:

```go
	if s.request.Runtime.URLTotalTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.request.Runtime.URLTotalTimeout)
		defer cancel()
	}
```

In `scrapeOneAttempt`, wrap navigation with navigation timeout:

```go
	navCtx := ctx
	var navCancel context.CancelFunc
	if s.request.Runtime.NavigationTimeout > 0 {
		navCtx, navCancel = context.WithTimeout(ctx, s.request.Runtime.NavigationTimeout)
		defer navCancel()
	}
	if err := s.page.Context(navCtx).Navigate(target.NavigateURL); err != nil {
		return nil, pageSnapshot{}, false, err
	}
```

- [ ] **Step 6: Run tests**

```bash
go test ./internal/tripcdp -run 'TestWorkerProgressWatchdog' -count=1
go test ./internal/tripcdp -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/tripcdp/watchdog.go internal/tripcdp/watchdog_test.go internal/tripcdp/runner.go internal/tripcdp/browser.go
git commit -m "feat: add worker watchdog and URL timeouts"
```

---

### Task 6: Add Runtime Summary and Threshold Counters

**Files:**
- Create: `internal/tripcdp/runtime_summary.go`
- Create: `internal/tripcdp/runtime_summary_test.go`
- Modify: `internal/tripcdp/runner.go`
- Modify: `internal/tripcdp/types.go`

**Interfaces:**
- Produces: `type vendorRuntimeSummary struct`
- Produces: `type runtimeRunSummary struct`
- Produces: `func writeRuntimeSummary(outputDir string, summary runtimeRunSummary, reporter Reporter)`
- Produces: helper counters for API-block ratio checks.

- [ ] **Step 1: Write summary tests**

Create `internal/tripcdp/runtime_summary_test.go`:

```go
package tripcdp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteRuntimeSummary(t *testing.T) {
	dir := t.TempDir()
	events := []Event{}
	writeRuntimeSummary(dir, runtimeRunSummary{
		Event: "runtime_summary",
		Vendors: []vendorRuntimeSummary{{Vendor: "BC", State: "api_blocked", Reason: "vendor API block threshold reached", URLsDone: 12, URLCount: 20, APIBlockedURLs: 9}},
		GlobalState: "partial",
		Reason: "deadline",
	}, func(event Event) { events = append(events, event) })
	path := filepath.Join(dir, "_diagnostics", "runtime_summary.jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("summary not written: %v", err)
	}
	if !strings.Contains(string(raw), "api_blocked") || !strings.Contains(string(raw), "BC") {
		t.Fatalf("summary missing fields: %s", raw)
	}
	if len(events) != 1 || events[0]["event"] != "runtime_summary" {
		t.Fatalf("summary event not emitted: %+v", events)
	}
}
```

- [ ] **Step 2: Run test to verify failure**

```bash
go test ./internal/tripcdp -run 'TestWriteRuntimeSummary' -count=1
```

Expected: FAIL because summary types/functions do not exist.

- [ ] **Step 3: Implement runtime summary**

Create `internal/tripcdp/runtime_summary.go`:

```go
package tripcdp

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type vendorRuntimeSummary struct {
	Vendor         string `json:"vendor"`
	State          string `json:"state"`
	Reason         string `json:"reason,omitempty"`
	URLsDone       int    `json:"urls_done"`
	URLCount       int    `json:"url_count"`
	Rows           int    `json:"rows"`
	APIBlockedURLs int    `json:"api_blocked_urls"`
	RiskEvents     int    `json:"risk_events"`
	OutputPath     string `json:"output_path,omitempty"`
}

type runtimeRunSummary struct {
	Event              string                 `json:"event"`
	GlobalState        string                 `json:"global_state"`
	Reason             string                 `json:"reason,omitempty"`
	ProcessedURLs      int                    `json:"processed_urls"`
	URLCount           int                    `json:"url_count"`
	APIBlockedURLs     int                    `json:"api_blocked_urls"`
	RiskEvents         int                    `json:"risk_events"`
	DeadlineExpired    bool                   `json:"deadline_expired"`
	Vendors            []vendorRuntimeSummary `json:"vendors"`
}

func writeRuntimeSummary(outputDir string, summary runtimeRunSummary, reporter Reporter) {
	if summary.Event == "" {
		summary.Event = "runtime_summary"
	}
	dir := filepath.Join(expandPath(outputDir), "_diagnostics")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	path := filepath.Join(dir, "runtime_summary.jsonl")
	raw, err := json.Marshal(summary)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, append(raw, '\n'), 0o644)
	if reporter != nil {
		reporter(Event{"event": summary.Event, "stage": "runtime", "global_state": summary.GlobalState, "reason": summary.Reason, "processed_urls": summary.ProcessedURLs, "url_count": summary.URLCount, "api_blocked_urls": summary.APIBlockedURLs, "risk_events": summary.RiskEvents, "deadline_expired": summary.DeadlineExpired, "diagnostic_path": path})
	}
}
```

- [ ] **Step 4: Extend workerResult with counters**

In `runner.go` `workerResult`, add:

```go
	APIBlockedURLs int
	RiskEvents     int
	FinalState     string
	FinalReason    string
```

In `workerResult.Event()`, add:

```go
		"api_blocked_urls": r.APIBlockedURLs,
		"risk_events":     r.RiskEvents,
		"final_state":     r.FinalState,
		"final_reason":    r.FinalReason,
```

In `runVendorWorker.localReporter`, increment counters:

```go
		if event["failure_class"] == FailureAPIBlockedSuspected || event["failure_class"] == string(FailureAPIBlockedSuspected) {
			result.APIBlockedURLs++
		}
		if event["event"] == "risk_challenge_detected" {
			result.RiskEvents++
		}
```

Also when `event["event"] == "url_quality_failed"`, if event has no class, classify by reason:

```go
		if event["event"] == "url_quality_failed" {
			class := FailureClass(fmt.Sprint(event["failure_class"]))
			if class == "" {
				class = ClassifyError(nil, fmt.Sprint(event["reason"]))
			}
			if class == FailureAPIBlockedSuspected {
				result.APIBlockedURLs++
			}
		}
```

- [ ] **Step 5: Emit summary at end of `scrapeVendorBatches`**

Before final returns in `scrapeVendorBatches`, build summary:

```go
	runtimeSummary := runtimeRunSummary{Event: "runtime_summary", GlobalState: "done", ProcessedURLs: urlsDone, URLCount: len(items)}
	for _, result := range results {
		state := "done"
		reason := ""
		if result.Stopped {
			state = "partial"
			reason = result.Error
		} else if !result.Success {
			state = "failed"
			reason = result.Error
		}
		if result.FinalState != "" {
			state = result.FinalState
			reason = result.FinalReason
		}
		runtimeSummary.APIBlockedURLs += result.APIBlockedURLs
		runtimeSummary.RiskEvents += result.RiskEvents
		runtimeSummary.Vendors = append(runtimeSummary.Vendors, vendorRuntimeSummary{Vendor: result.Vendor, State: state, Reason: reason, URLsDone: result.URLsDone, URLCount: result.URLCount, Rows: result.Rows, APIBlockedURLs: result.APIBlockedURLs, RiskEvents: result.RiskEvents, OutputPath: result.OutputPath})
	}
	if ctx.Err() != nil {
		runtimeSummary.GlobalState = "partial"
		runtimeSummary.Reason = "stopped"
	}
	writeRuntimeSummary(outputDir, runtimeSummary, reporter)
```

Place this before the `if ctx.Err() != nil` return and before success/failure returns.

- [ ] **Step 6: Run tests**

```bash
go test ./internal/tripcdp -run 'TestWriteRuntimeSummary' -count=1
go test ./internal/tripcdp -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/tripcdp/runtime_summary.go internal/tripcdp/runtime_summary_test.go internal/tripcdp/runner.go internal/tripcdp/types.go
git commit -m "feat: emit runtime resilience summaries"
```

---

### Task 7: Add Deadline and Global/Vendor Stop Thresholds

**Files:**
- Modify: `internal/tripcdp/runner.go`
- Modify: `internal/tripcdp/runner_test.go`

**Interfaces:**
- Consumes: `parseLocalDeadline`, `ratioReached`, `RuntimeOptions`.
- Produces: graceful deadline cancellation and threshold-based termination.

- [ ] **Step 1: Add pure helper tests**

Create `internal/tripcdp/runtime_threshold_test.go`:

```go
package tripcdp

import "testing"

func TestRatioReached(t *testing.T) {
	if ratioReached(6, 10, 0.70, 10) {
		t.Fatal("6/10 should not reach 70%")
	}
	if !ratioReached(7, 10, 0.70, 10) {
		t.Fatal("7/10 should reach 70%")
	}
	if ratioReached(7, 9, 0.70, 10) {
		t.Fatal("threshold must respect minimum total")
	}
}

func TestVendorAPIBlockedByResult(t *testing.T) {
	options := defaultRuntimeOptions()
	result := workerResult{URLCount: 20, URLsDone: 12, APIBlockedURLs: 9}
	if !vendorAPIBlockedByResult(result, options) {
		t.Fatal("9/12 should trip vendor api block threshold")
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

```bash
go test ./internal/tripcdp -run 'TestRatioReached|TestVendorAPIBlockedByResult' -count=1
```

Expected: FAIL because `vendorAPIBlockedByResult` does not exist.

- [ ] **Step 3: Implement threshold helper**

Add to `runner.go` or new `runtime_threshold.go`:

```go
func vendorAPIBlockedByResult(result workerResult, options RuntimeOptions) bool {
	return ratioReached(result.APIBlockedURLs, result.URLsDone, options.VendorAPIBlockRatio, options.VendorAPIBlockMinAttempts)
}

func globalAPIBlocked(processed int, apiBlocked int, options RuntimeOptions) bool {
	return ratioReached(apiBlocked, processed, options.GlobalAPIBlockRatio, options.GlobalAPIBlockMinProcessed)
}
```

- [ ] **Step 4: Add deadline context in `scrapeVendorBatches`**

After creating `ctx, cancel := context.WithCancel(context.Background())`, add:

```go
	deadlineExpired := false
	if deadline, ok, err := parseLocalDeadline(request.Runtime.DeadlineHHMM, time.Now()); err == nil && ok {
		if time.Now().Before(deadline) {
			deadlineTimer := time.AfterFunc(time.Until(deadline), func() {
				deadlineExpired = true
				reporter(Event{"event": "runtime_deadline_expired", "stage": "runtime", "deadline": request.Runtime.DeadlineHHMM, "message": "time window expired; shutting down gracefully"})
				cancel()
			})
			defer deadlineTimer.Stop()
		} else {
			deadlineExpired = true
			reporter(Event{"event": "runtime_deadline_expired", "stage": "runtime", "deadline": request.Runtime.DeadlineHHMM, "message": "deadline already expired; shutting down gracefully"})
			cancel()
		}
	} else if err != nil {
		reporter(Event{"event": "warning", "stage": "runtime", "message": "deadline ignored", "error": err.Error()})
	}
```

Because `deadlineExpired` is written by a timer goroutine, if the race detector is used later, replace bool with `atomic.Bool`. For now, prefer `sync/atomic`:

```go
	var deadlineExpired atomic.Bool
```

and use `deadlineExpired.Store(true)` / `deadlineExpired.Load()`; add import `sync/atomic`.

- [ ] **Step 5: Apply vendor/global threshold after each worker result**

In result loop after `results = append(results, result)`:

```go
		if vendorAPIBlockedByResult(result, request.Runtime) {
			result.FinalState = "api_blocked"
			result.FinalReason = "vendor API block threshold reached"
			reporter(Event{"event": "vendor_api_blocked", "stage": "runtime", "vendor": result.Vendor, "worker_id": result.WorkerID, "api_blocked_urls": result.APIBlockedURLs, "urls_done": result.URLsDone, "url_count": result.URLCount, "threshold": request.Runtime.VendorAPIBlockRatio})
		}
		if globalAPIBlocked(urlsDone, runtimeSummaryAPIBlocked(results), request.Runtime) {
			reporter(Event{"event": "global_api_blocked", "stage": "runtime", "api_blocked_urls": runtimeSummaryAPIBlocked(results), "processed_urls": urlsDone, "threshold": request.Runtime.GlobalAPIBlockRatio})
			cancel()
		}
```

Add helper:

```go
func runtimeSummaryAPIBlocked(results []workerResult) int {
	total := 0
	for _, result := range results {
		total += result.APIBlockedURLs
	}
	return total
}
```

Important: update `results[len(results)-1] = result` after mutating `result.FinalState`, because `append` stored the earlier copy.

- [ ] **Step 6: Set summary deadline fields**

When building runtime summary in Task 6 code:

```go
	runtimeSummary.DeadlineExpired = deadlineExpired.Load()
	if deadlineExpired.Load() {
		runtimeSummary.GlobalState = "partial"
		runtimeSummary.Reason = "deadline"
	}
```

- [ ] **Step 7: Ensure partial export on deadline**

Existing `runVendorWorker` exports rows even when stopped after loop. Verify this path still runs when context is canceled. If `scrapeOne` returns nil due to timeout/cancel before rows append, the worker still reaches export if previous rows exist.

- [ ] **Step 8: Run tests**

```bash
go test ./internal/tripcdp -run 'TestRatioReached|TestVendorAPIBlockedByResult|TestParseLocalDeadline' -count=1
go test ./internal/tripcdp -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/tripcdp/runner.go internal/tripcdp/runtime_threshold_test.go
git commit -m "feat: add runtime deadlines and block thresholds"
```

---

### Task 8: Add AntiBot Pressure Controller

**Files:**
- Create: `internal/tripcdp/pressure_controller.go`
- Create: `internal/tripcdp/pressure_controller_test.go`
- Modify: `internal/tripcdp/runner.go`
- Modify: `internal/tripcdp/risk_test.go`

**Interfaces:**
- Produces: `type PressureMode string`
- Produces: `type antiBotPressureController struct`
- Produces: `func newAntiBotPressureController(options RuntimeOptions, reporter Reporter) *antiBotPressureController`
- Produces: `func (c *antiBotPressureController) Observe(event Event)`
- Produces: `func (c *antiBotPressureController) EffectiveWorkerLimit(configured int) int`
- Produces: `func (c *antiBotPressureController) DelayBeforeNextStart(ctx context.Context) bool`

- [ ] **Step 1: Write pressure tests**

Create `internal/tripcdp/pressure_controller_test.go`:

```go
package tripcdp

import (
	"context"
	"testing"
	"time"
)

func TestPressureControllerRiskMovesToCautiousThenSafe(t *testing.T) {
	options := defaultRuntimeOptions()
	options.RiskToSafeThreshold = 3
	controller := newAntiBotPressureController(options, nil)
	for i := 0; i < 2; i++ {
		controller.Observe(Event{"event": "risk_challenge_detected", "risk_kind": "whaleguard", "vendor": "BC"})
	}
	if controller.Mode() != PressureCautious {
		t.Fatalf("mode = %q, want cautious", controller.Mode())
	}
	controller.Observe(Event{"event": "risk_challenge_detected", "risk_kind": "whaleguard", "vendor": "BC"})
	if controller.Mode() != PressureSafe {
		t.Fatalf("mode = %q, want safe", controller.Mode())
	}
}

func TestPressureControllerFastURLIncreasesPressure(t *testing.T) {
	options := defaultRuntimeOptions()
	options.FastURLThreshold = 30 * time.Second
	controller := newAntiBotPressureController(options, nil)
	controller.Observe(Event{"event": "url_done", "elapsed_ms": int64(5000), "vendor": "BC"})
	if controller.Mode() != PressureCautious {
		t.Fatalf("mode = %q, want cautious", controller.Mode())
	}
}

func TestPressureControllerEffectiveWorkerLimit(t *testing.T) {
	options := defaultRuntimeOptions()
	options.RiskToSafeThreshold = 1
	controller := newAntiBotPressureController(options, nil)
	if got := controller.EffectiveWorkerLimit(3); got != 3 {
		t.Fatalf("normal workers = %d", got)
	}
	controller.Observe(Event{"event": "risk_challenge_detected", "risk_kind": "whaleguard"})
	if got := controller.EffectiveWorkerLimit(3); got != 1 {
		t.Fatalf("safe workers = %d, want 1", got)
	}
}

func TestPressureControllerDelayIsCancellable(t *testing.T) {
	options := defaultRuntimeOptions()
	options.RiskToSafeThreshold = 1
	controller := newAntiBotPressureController(options, nil)
	controller.Observe(Event{"event": "risk_challenge_detected", "risk_kind": "whaleguard"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if controller.DelayBeforeNextStart(ctx) {
		t.Fatal("delay should stop on canceled context")
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

```bash
go test ./internal/tripcdp -run 'TestPressureController' -count=1
```

Expected: FAIL because controller does not exist.

- [ ] **Step 3: Implement pressure controller**

Create `internal/tripcdp/pressure_controller.go`:

```go
package tripcdp

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type PressureMode string

const (
	PressureNormal   PressureMode = "normal"
	PressureCautious PressureMode = "cautious"
	PressureSafe     PressureMode = "safe"
	PressureRecovery PressureMode = "recovery"
	PressureTerminate PressureMode = "terminate"
)

type antiBotPressureController struct {
	mu             sync.Mutex
	options        RuntimeOptions
	reporter        Reporter
	mode           PressureMode
	globalScore    int
	consecutiveRisk int
	vendorScore    map[string]int
}

func newAntiBotPressureController(options RuntimeOptions, reporter Reporter) *antiBotPressureController {
	return &antiBotPressureController{options: options, reporter: reporter, mode: PressureNormal, vendorScore: map[string]int{}}
}

func (c *antiBotPressureController) Mode() PressureMode {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mode
}

func (c *antiBotPressureController) Observe(event Event) {
	if c == nil || event == nil {
		return
	}
	vendor := fmt.Sprint(event["vendor"])
	c.mu.Lock()
	oldMode := c.mode
	switch event["event"] {
	case "risk_challenge_detected":
		c.globalScore += 5
		c.consecutiveRisk++
		if vendor != "" {
			c.vendorScore[vendor] += 5
		}
	case "url_done":
		if elapsedMS, ok := numericEventValue(event["elapsed_ms"]); ok && c.options.FastURLThreshold > 0 && time.Duration(elapsedMS)*time.Millisecond < c.options.FastURLThreshold {
			c.globalScore += 2
			if vendor != "" {
				c.vendorScore[vendor] += 2
			}
		} else if c.globalScore > 0 {
			c.globalScore--
			c.consecutiveRisk = 0
		}
	case "price_quality_recovered":
		if c.globalScore > 0 {
			c.globalScore--
		}
	}
	c.recomputeModeLocked()
	newMode := c.mode
	c.mu.Unlock()
	if oldMode != newMode && c.reporter != nil {
		c.reporter(Event{"event": "pressure_mode_changed", "stage": "risk", "from": oldMode, "to": newMode})
	}
}

func (c *antiBotPressureController) recomputeModeLocked() {
	if c.consecutiveRisk >= max(1, c.options.RiskToSafeThreshold) || c.globalScore >= 15 {
		c.mode = PressureSafe
		return
	}
	if c.globalScore >= 2 {
		c.mode = PressureCautious
		return
	}
	if c.mode == PressureSafe || c.mode == PressureCautious {
		c.mode = PressureRecovery
		return
	}
	c.mode = PressureNormal
}

func (c *antiBotPressureController) EffectiveWorkerLimit(configured int) int {
	if configured < 1 {
		configured = 1
	}
	mode := c.Mode()
	switch mode {
	case PressureSafe:
		return 1
	case PressureCautious, PressureRecovery:
		return max(1, min(configured, 2))
	default:
		return configured
	}
}

func (c *antiBotPressureController) DelayBeforeNextStart(ctx context.Context) bool {
	if c == nil {
		return ctx.Err() == nil
	}
	mode := c.Mode()
	delay := time.Duration(0)
	switch mode {
	case PressureSafe:
		delay = 20 * time.Second
	case PressureCautious, PressureRecovery:
		delay = 8 * time.Second
	}
	return cancellableSleep(ctx, delay)
}

func numericEventValue(value any) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		return int64(v), true
	default:
		return 0, false
	}
}
```

- [ ] **Step 4: Integrate in `scrapeVendorBatches`**

Replace or wrap existing `riskPause` logic:

```go
	pressure := newAntiBotPressureController(request.Runtime, reporter)
	riskPause := newGlobalRiskPause(request.DelayOptions.Profile, reporter)
	workerReporter := func(event Event) {
		riskPause.observe(event)
		pressure.Observe(event)
		reporter(event)
	}
```

In `startNext`, after `riskPause.wait(ctx)` add:

```go
		if !pressure.DelayBeforeNextStart(ctx) {
			return false
		}
```

Change `canStartWorker` calls to use effective limit:

```go
	for canStartWorker(ctx, len(pending), active, pressure.EffectiveWorkerLimit(workers)) {
```

and later:

```go
		if canStartWorker(ctx, len(pending), active, pressure.EffectiveWorkerLimit(workers)) {
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/tripcdp -run 'TestPressureController|TestGlobalRiskPause' -count=1
go test ./internal/tripcdp -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/tripcdp/pressure_controller.go internal/tripcdp/pressure_controller_test.go internal/tripcdp/runner.go internal/tripcdp/risk_test.go
git commit -m "feat: add anti-bot pressure controller"
```

---

### Task 9: Add Vendor Scheduler State Machine

**Files:**
- Create: `internal/tripcdp/vendor_scheduler.go`
- Create: `internal/tripcdp/vendor_scheduler_test.go`
- Modify: `internal/tripcdp/runner.go`

**Interfaces:**
- Produces: `type VendorState string`
- Produces: `type vendorScheduler struct`
- Produces: `func newVendorScheduler(groups []vendorGroup) *vendorScheduler`
- Produces: `func (s *vendorScheduler) Next(now time.Time) (vendorGroup, bool)`
- Produces: `func (s *vendorScheduler) Complete(result workerResult, options RuntimeOptions, now time.Time)`
- Produces: `func (s *vendorScheduler) PendingCount() int`

- [ ] **Step 1: Write scheduler tests**

Create `internal/tripcdp/vendor_scheduler_test.go`:

```go
package tripcdp

import (
	"testing"
	"time"
)

func TestVendorSchedulerSkipsAPIBlockedVendor(t *testing.T) {
	groups := []vendorGroup{{Vendor: "BC", Items: []ScrapeItem{{URL: "u1"}}}, {Vendor: "TSIM", Items: []ScrapeItem{{URL: "u2"}}}}
	scheduler := newVendorScheduler(groups)
	now := time.Now()
	first, ok := scheduler.Next(now)
	if !ok || first.Vendor != "BC" {
		t.Fatalf("first = %+v ok=%v", first, ok)
	}
	scheduler.Complete(workerResult{Vendor: "BC", URLsDone: 10, URLCount: 10, APIBlockedURLs: 8}, defaultRuntimeOptions(), now)
	second, ok := scheduler.Next(now)
	if !ok || second.Vendor != "TSIM" {
		t.Fatalf("second = %+v ok=%v", second, ok)
	}
	if scheduler.State("BC") != VendorStateAPIBlocked {
		t.Fatalf("BC state = %q", scheduler.State("BC"))
	}
}

func TestVendorSchedulerCoolingDelaysVendor(t *testing.T) {
	groups := []vendorGroup{{Vendor: "BC", Items: []ScrapeItem{{URL: "u1"}}}}
	scheduler := newVendorScheduler(groups)
	now := time.Now()
	scheduler.SetCooling("BC", now.Add(time.Minute), "risk")
	if _, ok := scheduler.Next(now); ok {
		t.Fatal("cooling vendor should not be schedulable")
	}
	if _, ok := scheduler.Next(now.Add(2 * time.Minute)); !ok {
		t.Fatal("vendor should be schedulable after cooling expires")
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

```bash
go test ./internal/tripcdp -run 'TestVendorScheduler' -count=1
```

Expected: FAIL because scheduler does not exist.

- [ ] **Step 3: Implement scheduler**

Create `internal/tripcdp/vendor_scheduler.go`:

```go
package tripcdp

import "time"

type VendorState string

const (
	VendorStateActive     VendorState = "active"
	VendorStateCooling    VendorState = "cooling"
	VendorStateDegraded   VendorState = "degraded"
	VendorStateAPIBlocked VendorState = "api_blocked"
	VendorStatePaused     VendorState = "paused"
	VendorStateDone       VendorState = "done"
)

type vendorScheduleEntry struct {
	group     vendorGroup
	state     VendorState
	reason    string
	coolUntil time.Time
	started   bool
}

type vendorScheduler struct {
	entries []vendorScheduleEntry
}

func newVendorScheduler(groups []vendorGroup) *vendorScheduler {
	entries := make([]vendorScheduleEntry, 0, len(groups))
	for _, group := range groups {
		entries = append(entries, vendorScheduleEntry{group: group, state: VendorStateActive})
	}
	return &vendorScheduler{entries: entries}
}

func (s *vendorScheduler) Next(now time.Time) (vendorGroup, bool) {
	if s == nil {
		return vendorGroup{}, false
	}
	for i := range s.entries {
		entry := &s.entries[i]
		if entry.started || entry.state == VendorStateDone || entry.state == VendorStateAPIBlocked || entry.state == VendorStatePaused {
			continue
		}
		if entry.state == VendorStateCooling && now.Before(entry.coolUntil) {
			continue
		}
		if entry.state == VendorStateCooling && !now.Before(entry.coolUntil) {
			entry.state = VendorStateDegraded
		}
		entry.started = true
		return entry.group, true
	}
	return vendorGroup{}, false
}

func (s *vendorScheduler) Complete(result workerResult, options RuntimeOptions, now time.Time) {
	if s == nil {
		return
	}
	for i := range s.entries {
		entry := &s.entries[i]
		if entry.group.Vendor != result.Vendor {
			continue
		}
		entry.started = false
		if vendorAPIBlockedByResult(result, options) {
			entry.state = VendorStateAPIBlocked
			entry.reason = "vendor API block threshold reached"
			return
		}
		entry.state = VendorStateDone
		entry.reason = result.Error
		return
	}
}

func (s *vendorScheduler) SetCooling(vendor string, until time.Time, reason string) {
	if s == nil {
		return
	}
	for i := range s.entries {
		if s.entries[i].group.Vendor == vendor {
			s.entries[i].state = VendorStateCooling
			s.entries[i].coolUntil = until
			s.entries[i].reason = reason
		}
	}
}

func (s *vendorScheduler) State(vendor string) VendorState {
	if s == nil {
		return ""
	}
	for _, entry := range s.entries {
		if entry.group.Vendor == vendor {
			return entry.state
		}
	}
	return ""
}

func (s *vendorScheduler) PendingCount() int {
	if s == nil {
		return 0
	}
	count := 0
	for _, entry := range s.entries {
		if !entry.started && entry.state != VendorStateDone && entry.state != VendorStateAPIBlocked && entry.state != VendorStatePaused {
			count++
		}
	}
	return count
}
```

- [ ] **Step 4: Integrate scheduler in `scrapeVendorBatches`**

Replace:

```go
	pending := append([]vendorGroup(nil), groups...)
```

with:

```go
	scheduler := newVendorScheduler(groups)
```

Replace initial start loop:

```go
	for canStartWorker(ctx, scheduler.PendingCount(), active, pressure.EffectiveWorkerLimit(workers)) {
		group, ok := scheduler.Next(time.Now())
		if !ok {
			break
		}
		if !startNext(group) {
			break
		}
	}
```

Replace result loop completion start logic:

```go
		scheduler.Complete(result, request.Runtime, time.Now())
		if result.RiskEvents > 0 && scheduler.State(result.Vendor) != VendorStateDone && scheduler.State(result.Vendor) != VendorStateAPIBlocked {
			scheduler.SetCooling(result.Vendor, time.Now().Add(riskCooldownDelay(request.DelayOptions.Profile)), "risk events")
		}
		for canStartWorker(ctx, scheduler.PendingCount(), active, pressure.EffectiveWorkerLimit(workers)) {
			group, ok := scheduler.Next(time.Now())
			if !ok {
				break
			}
			if !startNext(group) {
				break
			}
		}
```

Remove all uses of `pending` in this function.

- [ ] **Step 5: Run tests**

```bash
go test ./internal/tripcdp -run 'TestVendorScheduler|TestPressureController' -count=1
go test ./internal/tripcdp -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/tripcdp/vendor_scheduler.go internal/tripcdp/vendor_scheduler_test.go internal/tripcdp/runner.go
git commit -m "feat: add pressure-aware vendor scheduler"
```

---

### Task 10: Add Partial Checkpoint Exports

**Files:**
- Modify: `internal/tripcdp/runner.go`
- Modify: `internal/tripcdp/export.go` if export helper needs safe repeated writes
- Add tests if existing export tests can cover path naming.

**Interfaces:**
- Consumes: `TaskRequest.Runtime.CheckpointEveryURLs`.
- Produces: per-vendor partial checkpoint files during long worker runs.

- [ ] **Step 1: Add checkpoint filename helper test**

Create `internal/tripcdp/checkpoint_test.go`:

```go
package tripcdp

import (
	"strings"
	"testing"
)

func TestCheckpointOutputPath(t *testing.T) {
	path := checkpointOutputPath("/tmp/out/BC_260708-0000.xlsx", "BC", 10)
	if !strings.Contains(path, "BC_partial_010.xlsx") {
		t.Fatalf("path = %q", path)
	}
}
```

- [ ] **Step 2: Run test to verify failure**

```bash
go test ./internal/tripcdp -run 'TestCheckpointOutputPath' -count=1
```

Expected: FAIL because helper does not exist.

- [ ] **Step 3: Implement helper**

Add to `runner.go` or new `checkpoint.go`:

```go
func checkpointOutputPath(outputPath string, vendor string, done int) string {
	dir := filepath.Dir(outputPath)
	return filepath.Join(dir, fmt.Sprintf("%s_partial_%03d.xlsx", safeFilename(vendor), done))
}
```

Ensure `fmt` and `path/filepath` imports are already present in `runner.go`; they are.

- [ ] **Step 4: Export checkpoint in `runVendorWorker`**

Inside URL loop after `rows = append(rows, itemRows...)` and `result.URLsDone++`:

```go
			if request.Runtime.CheckpointEveryURLs > 0 && result.URLsDone%request.Runtime.CheckpointEveryURLs == 0 && len(rows) > 0 {
				checkpointPath := checkpointOutputPath(outputPath, group.FileVendor, result.URLsDone)
				if savedPath, err := exportRows(rows, checkpointPath, request.RatingOnly, request.ExtractRealPrice, request.ExtractProductInfo, true); err == nil {
					localReporter(Event{"event": "partial_checkpoint_done", "stage": "export", "rows": len(rows), "urls_done": result.URLsDone, "output_path": savedPath})
				} else {
					localReporter(Event{"event": "warning", "stage": "export", "message": "partial checkpoint failed", "error": err.Error(), "urls_done": result.URLsDone})
				}
			}
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/tripcdp -run 'TestCheckpointOutputPath' -count=1
go test ./internal/tripcdp -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/tripcdp/runner.go internal/tripcdp/checkpoint_test.go
git commit -m "feat: write vendor partial checkpoints"
```

---

### Task 11: Update Hermes Cron Launcher Defaults

**Files:**
- Modify: `scripts/hermes-cron/trip_sku_daily_launcher.py`
- Test: run Python syntax check and inspect generated command path if existing tests are unavailable.

**Interfaces:**
- Consumes: CLI flags from Task 2.
- Produces: safer scheduled defaults.

- [ ] **Step 1: Inspect current command construction**

Open `scripts/hermes-cron/trip_sku_daily_launcher.py` and locate the `trip-score-cdp` argument list. Preserve existing paths, Telegram chat settings, and business-specific arguments.

- [ ] **Step 2: Modify command flags**

Add or update the command args so scheduled mode includes:

```python
"--no-headless",
"--workers", "2",
"--delay-profile", "safe",
"--deadline", "0530",
"--url-total-timeout", "300",
"--navigation-timeout", "120",
"--worker-stall-timeout", "600",
"--max-quality-retries", "2",
"--max-browser-restarts-per-vendor", "2",
"--vendor-api-block-ratio", "0.70",
"--vendor-api-block-min-attempts", "10",
"--global-api-block-ratio", "0.60",
"--global-api-block-min-processed", "50",
"--fast-url-threshold", "30",
"--risk-to-safe-threshold", "10",
"--checkpoint-every-urls", "10",
```

If the script already passes `--headless`, remove it when adding `--no-headless`.

- [ ] **Step 3: Run syntax check**

```bash
python3 -m py_compile scripts/hermes-cron/trip_sku_daily_launcher.py
```

Expected: exits 0.

- [ ] **Step 4: Run Go CLI help smoke test**

```bash
go run ./cmd/trip-score-cdp --help >/tmp/trip-score-cdp-help.txt
```

Expected: exits 0 and `/tmp/trip-score-cdp-help.txt` contains the new runtime flags.

- [ ] **Step 5: Commit**

```bash
git add scripts/hermes-cron/trip_sku_daily_launcher.py
git commit -m "chore: enable safer Trip SKU cron runtime defaults"
```

---

### Task 12: End-to-End Validation and Spec Alignment

**Files:**
- Modify docs only if implementation diverged from `/Users/example/Downloads/trip-sku-improvement-spec.md`.
- No production code unless validation reveals a bug.

**Interfaces:**
- Consumes: all previous tasks.
- Produces: final verified state.

- [ ] **Step 1: Run full test suite**

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 2: Run CLI parse smoke test with new flags**

```bash
go run ./cmd/trip-score-cdp \
  --input https://hk.trip.com/things-to-do/detail/1/ \
  --deadline none \
  --url-total-timeout 5 \
  --navigation-timeout 5 \
  --worker-stall-timeout 5 \
  --max-quality-retries 1 \
  --max-browser-restarts-per-vendor 1 \
  --vendor-api-block-ratio 0.70 \
  --global-api-block-ratio 0.60 \
  --checkpoint-every-urls 1 \
  --help >/tmp/trip-score-cdp-runtime-smoke.txt
```

Expected: exits 0.

- [ ] **Step 3: Run targeted tests for all new runtime areas**

```bash
go test ./internal/tripcdp -run 'TestClassify|TestQualityRerun|TestShouldRetryPriceQuality|TestWorkerProgressWatchdog|TestPressureController|TestVendorScheduler|TestWriteRuntimeSummary|TestCheckpointOutputPath|TestParseArgsRuntimeOptions' -count=1
```

Expected: PASS.

- [ ] **Step 4: Verify no unbounded retry remains for API-suspected resource misses**

Search code:

```bash
grep -R "resource_miss_rate\|consecutive_resource_miss\|restart_browser\|restartAfterURL" -n internal/tripcdp
```

Expected: every path that restarts a browser checks either `FailureClassAllowsBrowserRestart` or is explicitly for browser/risk/page failure.

- [ ] **Step 5: Verify runtime summary events exist**

Search code:

```bash
grep -R "runtime_summary\|vendor_api_blocked\|global_api_blocked\|runtime_deadline_expired\|pressure_mode_changed\|worker_stalled" -n internal/tripcdp
```

Expected: all event names appear in implementation and relevant tests.

- [ ] **Step 6: Run formatting**

```bash
gofmt -w internal/tripcdp/*.go
```

Expected: no output.

- [ ] **Step 7: Re-run full tests after formatting**

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 8: Check git diff**

```bash
git status --short
git diff --stat
```

Expected: only planned files changed.

- [ ] **Step 9: Final commit if any validation fixes were made after prior commits**

```bash
git add internal/tripcdp scripts/hermes-cron/trip_sku_daily_launcher.py docs/superpowers/plans/2026-07-08-trip-sku-runtime-resilience-development.md
git commit -m "test: validate Trip SKU runtime resilience"
```

If there are no changes since the previous commit, skip this commit.

---

## Self-Review Notes

### Spec coverage

- P0 failure classification: Tasks 1, 3, 4.
- P0 quality rerun分流: Tasks 3, 4.
- P0 retry/rerun budgets: Tasks 2, 4, 7.
- P0 deadline: Tasks 2, 7.
- P0 vendor/global thresholds: Tasks 6, 7, 9.
- P0 worker watchdog: Task 5.
- P0 partial output/final summary: Tasks 6, 10.
- P1 pressure controller: Task 8.
- P1 dynamic worker/delay behavior: Task 8 integrated in runner.
- P2 vendor scheduler: Task 9.
- Cron configuration: Task 11.
- End-to-end verification: Task 12.

### Known implementation cautions

- `deadlineExpired` must use `sync/atomic` if accessed from a timer goroutine.
- `workerResult` is copied when appended to `results`; if mutating after append, write back to `results[len(results)-1]`.
- Avoid adding browser restarts for `FailureAPIBlockedSuspected` or `FailureBusinessUnavailable`.
- Scheduler integration should preserve existing output naming and session/profile setup.
- If a test references `priceQualityTracker` fields that differ from current code, adjust the test to exact field names before implementing logic.

### Execution model

This plan is intentionally a single consolidated development plan for one implementation agent. Execute tasks sequentially in one branch/session. Do not split P0/P1/P2 into separate agents unless the user changes the execution instruction.
