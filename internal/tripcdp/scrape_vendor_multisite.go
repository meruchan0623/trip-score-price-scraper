package tripcdp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// scrapeVendorMultiSite scrapes an Excel vendor target table across multiple
// sites (--sites) using multiple vendor workers per site, markets strictly
// sequential ("finish all products of one market before the next market"),
// and outputs one combined workbook with a sheet per site code. Every row
// keeps its vendor column (from exportRows includeVendorColumn=true).
func scrapeVendorMultiSite(request TaskRequest, reporter Reporter, runMode string) TaskResult {
	items, warnings, err := loadItems(request)
	if err != nil {
		return TaskResult{Success: false, Error: err.Error(), ExitCode: 2}
	}
	emitInputAudit(request.ExcelInput, reporter)
	for _, warning := range warnings {
		reporter(Event{"event": "warning", "message": warning})
	}
	groups := vendorGroups(items)
	workers, err := resolveWorkerCount(request.Workers, len(groups))
	if err != nil {
		return TaskResult{Success: false, Error: err.Error(), ExitCode: 2}
	}
	stamp := time.Now().Format("060102-1504")
	baseOutputDir := expandPath(request.OutputDir)
	if baseOutputDir == "" {
		baseOutputDir = defaultVendorOutputDir(stamp)
	}
	_ = os.MkdirAll(baseOutputDir, 0o755)

	// Resolve markets from --sites (sequential).
	markets := []MarketContext{}
	for _, code := range request.Sites {
		m, ok := lookupSite(code)
		if !ok {
			return TaskResult{Success: false, Error: fmt.Sprintf("unknown site %q", code), ExitCode: 2}
		}
		markets = append(markets, m)
	}
	if len(markets) == 0 {
		return TaskResult{Success: false, Error: "no markets to scrape", ExitCode: 2}
	}
	// The coordinator is the sole writer: all worker rows are accumulated in
	// memory and emitted once into this workbook after every market completes.
	combinedOutputPath := filepath.Join(baseOutputDir, fmt.Sprintf("trip_mode2_%s.xlsx", stamp))
	if len(markets) == 1 {
		combinedOutputPath = filepath.Join(baseOutputDir, fmt.Sprintf("trip_score_%s.xlsx", stamp))
	}

	state := resolveRuntimeState(request)
	for _, warning := range state.Warnings {
		reporter(Event{"event": "warning", "stage": "state", "message": warning})
	}
	if state.Migrated {
		reporter(Event{"event": "state_migrated", "state_dir": state.StateDir})
	}

	// Global runtime context shared across markets.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var deadlineExpired atomic.Bool
	if deadline, ok, err := parseLocalDeadline(request.Runtime.DeadlineHHMM, time.Now()); err == nil && ok {
		if time.Now().Before(deadline) {
			deadlineTimer := time.AfterFunc(time.Until(deadline), func() {
				deadlineExpired.Store(true)
				reporter(Event{"event": "runtime_deadline_expired", "stage": "runtime", "deadline": request.Runtime.DeadlineHHMM, "message": "time window expired; shutting down gracefully"})
				cancel()
			})
			defer deadlineTimer.Stop()
		} else {
			deadlineExpired.Store(true)
			cancel()
		}
	} else if err != nil {
		reporter(Event{"event": "warning", "stage": "runtime", "message": "deadline ignored", "error": err.Error()})
	}
	force := make(chan struct{}, 1)
	stopCtl := installStopHandlers(request.StopGraceSeconds, reporter, cancel, func() { signalOnce(force) })
	defer stopCtl.restore()
	pressure := newAntiBotPressureController(request.Runtime, reporter)
	riskPause := newGlobalRiskPause(request.DelayOptions.Profile, reporter)
	workerReporter := func(event Event) {
		riskPause.observe(event)
		pressure.Observe(event)
		reporter(event)
	}
	powerGuard := startSystemAwakeGuard(reporter)
	defer powerGuard.close()

	allRows := []map[string]string{}
	var urlsDone, rows, failures int
	failedVendors := []string{}
	outputPaths := []string{}
	results := []workerResult{}
	processedMarkets := 0

	for mi, market := range markets {
		if ctx.Err() != nil {
			break
		}
		// Workers have isolated browser state only; they do not write shard XLSX
		// files. The coordinator writes combinedOutputPath once at the end.
		marketDir := baseOutputDir
		var marketResults []workerResult

		savedMarket := request.Market
		request.Market = market
		request.BrowserOptions.Language = market.AcceptLanguage
		request.BrowserOptions.SessionFile = state.SessionFile
		reporter(mergeEvent(Event{
			"event":                "run_started",
			"mode":                 "excel",
			"run_mode":             runMode,
			"vendor_count":         len(groups),
			"url_count":            len(items),
			"workers":              workers,
			"headless":             request.BrowserOptions.Headless,
			"rating_only":          request.RatingOnly,
			"extract_real_price":   request.ExtractRealPrice,
			"extract_product_info": request.ExtractProductInfo,
			"delay_profile":        request.DelayOptions.Profile,
			"output_dir":           absPath(marketDir),
			"log_file":             absPath(request.LogFile),
			"state_dir":            absPath(state.StateDir),
			"profile_seed_dir":     absPath(state.ProfileSeedDir),
			"market_index":         mi + 1,
			"market_count":         len(markets),
		}, marketEventFields(market)))

		// Run the multi-vendor scheduler for this market (same-site, different
		// vendors run concurrently via multiple workers).
		scheduler := newVendorScheduler(groups)
		nextID := 0
		active := 0
		completed := 0
		resultCh := make(chan workerResult)
		forceWorkers := &forceStopper{}
		qualityRerunLimiter := make(chan struct{}, 1)

		startNext := func(group vendorGroup) bool {
			if ctx.Err() != nil {
				return false
			}
			if !riskPause.wait(ctx) {
				return false
			}
			if !pressure.DelayBeforeNextStart(ctx) {
				return false
			}
			nextID++
			active++
			workerID := nextID
			outputPath := combinedOutputPath
			workerDir := filepath.Join(marketDir, "_workers", strings.ToUpper(market.Code), fmt.Sprintf("%02d_%s", workerID, safeFilename(group.FileVendor)))
			workerRequest := request
			workerRequest.OutputPath = outputPath
			workerRequest.BrowserOptions.SessionFile = filepath.Join(workerDir, "session.json")
			workerRequest.BrowserOptions.UserDataDir = filepath.Join(workerDir, "profile")
			applyMarketBrowserDefaults(&workerRequest)
			reporter(Event{"event": "worker_spawn_started", "vendor": group.Vendor, "worker_id": workerID, "message": "spawning worker", "url_count": len(group.Items)})
			workerCtx, workerCancel := context.WithCancel(ctx)
			forceWorkers.add(workerCancel)
			go runVendorWorker(workerCtx, workerID, group, workerRequest, outputPath, true, len(items), workerReporter, resultCh, forceWorkers.add, qualityRerunLimiter)
			if request.StartupStagger > 0 {
				_ = cancellableSleep(ctx, request.StartupStagger)
			}
			return ctx.Err() == nil
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

		for active > 0 {
			result := <-resultCh
			active--
			completed++
			results = append(results, result)
			marketResults = append(marketResults, result)
			urlsDone += result.URLsDone
			if !result.Success {
				failures++
				failedVendors = append(failedVendors, result.Vendor)
			}
			reporter(result.Event())
			reporter(Event{
				"event":        "progress",
				"vendors_done": completed,
				"vendor_count": len(groups),
				"urls_done":    urlsDone,
				"url_count":    len(items) * len(markets),
				"active":       []string{},
				"failed":       failures,
			})
			scheduler.Complete(result, request.Runtime, time.Now())
			for canStartWorker(ctx, scheduler.PendingCount(), active, pressure.EffectiveWorkerLimit(workers)) {
				group, ok := scheduler.Next(time.Now())
				if !ok {
					break
				}
				if !startNext(group) {
					break
				}
			}
		}

		// Save any explicit session across markets.
		if state.SessionExplicit {
			for _, result := range marketResults {
				if result.Success && result.SessionFile != "" {
					if raw, err := os.ReadFile(result.SessionFile); err == nil {
						_ = ensureParent(state.SessionFile)
						_ = os.WriteFile(expandPath(state.SessionFile), raw, 0o644)
					}
					break
				}
			}
		}

		// Workers return rows directly. Tag their vendor and market before the
		// coordinator performs the only final workbook export.
		for _, result := range marketResults {
			for i := range result.CollectedRows {
				result.CollectedRows[i]["厂商"] = result.Vendor
				result.CollectedRows[i]["站点"] = market.Code
			}
			allRows = append(allRows, result.CollectedRows...)
			rows += len(result.CollectedRows)
		}
		processedMarkets++

		_ = os.RemoveAll(filepath.Join(marketDir, "_workers"))
		request.Market = savedMarket // restore for next market
	}

	// One and only one final workbook, with a sheet per site and a vendor column.
	savedPath, exportErr := exportMultiSiteRows(allRows, combinedOutputPath, request.RatingOnly, request.ExtractRealPrice, request.ExtractProductInfo, true)
	if exportErr != nil {
		return TaskResult{Success: false, OutputPath: baseOutputDir, OutputPaths: outputPaths, LogFile: request.LogFile, Error: "combined export failed: " + exportErr.Error(), ExitCode: 4}
	}
	outputPaths = append([]string{savedPath}, outputPaths...)

	if processedMarkets == 0 || len(allRows) == 0 {
		return TaskResult{Success: false, OutputPath: baseOutputDir, OutputPaths: outputPaths, LogFile: request.LogFile, Error: "no score/price data captured", ExitCode: 4}
	}
	if ctx.Err() != nil {
		return TaskResult{Success: false, OutputPath: baseOutputDir, OutputPaths: outputPaths, ProcessedCount: rows, Warnings: warnings, FailedVendors: failedVendors, LogFile: request.LogFile, Error: "stopped by signal", ExitCode: 130}
	}
	if len(failedVendors) > 0 {
		return TaskResult{Success: false, OutputPath: baseOutputDir, OutputPaths: outputPaths, ProcessedCount: rows, Warnings: warnings, FailedVendors: failedVendors, LogFile: request.LogFile, Error: summarizeFailures(results), ExitCode: 4}
	}
	return TaskResult{Success: true, OutputPath: baseOutputDir, OutputPaths: outputPaths, ProcessedCount: rows, Warnings: warnings, LogFile: request.LogFile, ExitCode: 0}
}
