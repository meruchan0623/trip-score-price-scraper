package tripcdp

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

func scrape(request TaskRequest, reporter Reporter, runMode string) TaskResult {
	if request.ExcelInput != "" {
		return scrapeVendorBatches(request, reporter, runMode)
	}
	return scrapeSingle(request, reporter, runMode)
}

func validateSinglePriceScrapeRows(rows []map[string]string) (int, error) {
	failures := []string{}
	for _, group := range groupRowsByURL(rows) {
		score := qualityScoreRows(group)
		trusted, _, priceFailed := countPriceQualityRows(group)
		if !score.needsRerun && trusted > 0 && !priceFailed {
			continue
		}

		identifier := firstNonEmptyRowValue(group, "商品ID")
		if identifier == "" {
			identifier = productID(firstNonEmptyRowValue(group, "URL"))
		}
		if identifier == "" {
			identifier = "未知产品"
		}
		reason := "结果质量校验未通过，未获得完整 SKU 价格。"
		if ClassifyRows(group) == FailureRiskChallenge {
			reason = "Trip.com/Cloudflare 风险拦截，未获得完整 SKU 价格。"
		}
		failures = append(failures, identifier+" 价格抓取失败，原因："+reason)
	}
	if len(failures) == 0 {
		return 0, nil
	}
	return len(failures), fmt.Errorf("%s", strings.Join(failures, "；"))
}

func scrapeSingle(request TaskRequest, reporter Reporter, runMode string) TaskResult {
	items, _, err := loadItems(request)
	if err != nil {
		return TaskResult{Success: false, Error: err.Error(), ExitCode: 2}
	}
	stamp := time.Now().Format("060102-1504")
	outputPath := expandPath(request.OutputPath)
	if outputPath == "" {
		outputPath = defaultSingleOutputPath(request, stamp)
	}
	request.OutputPath = outputPath
	state := resolveRuntimeState(request)
	for _, warning := range state.Warnings {
		reporter(Event{"event": "warning", "stage": "state", "message": warning})
	}
	if state.Migrated {
		reporter(Event{"event": "state_migrated", "state_dir": state.StateDir})
	}
	if request.BrowserOptions.Language == "" {
		request.BrowserOptions.Language = siteConfigFor(request.Site).AcceptLanguage
	}
	runtimeDir := filepath.Join(filepath.Dir(outputPath), "_workers", "01_"+safeFilename(defaultVendor))
	request.BrowserOptions.SessionFile = filepath.Join(runtimeDir, "session.json")
	request.BrowserOptions.UserDataDir = filepath.Join(runtimeDir, "profile")
	if err := ensureParent(request.BrowserOptions.SessionFile); err != nil {
		return TaskResult{Success: false, OutputPath: outputPath, LogFile: request.LogFile, Error: "session prepare failed: " + err.Error(), ExitCode: 4}
	}
	if request.FreshProfile {
		if err := prepareRuntimeSession(state.SessionFile, request.BrowserOptions.SessionFile, true); err != nil {
			return TaskResult{Success: false, OutputPath: outputPath, LogFile: request.LogFile, Error: "session prepare failed: " + err.Error(), ExitCode: 4}
		}
	} else if raw, err := os.ReadFile(state.SessionFile); err == nil {
		if err := os.WriteFile(request.BrowserOptions.SessionFile, raw, 0o644); err != nil {
			return TaskResult{Success: false, OutputPath: outputPath, LogFile: request.LogFile, Error: "session prepare failed: " + err.Error(), ExitCode: 4}
		}
	} else if !state.SessionExplicit {
		if err := writeEmptySession(request.BrowserOptions.SessionFile); err != nil {
			return TaskResult{Success: false, OutputPath: outputPath, LogFile: request.LogFile, Error: "session prepare failed: " + err.Error(), ExitCode: 4}
		}
	}
	if err := prepareRuntimeProfile(state.ProfileSeedDir, request.BrowserOptions.UserDataDir, request.FreshProfile); err != nil {
		return TaskResult{Success: false, OutputPath: outputPath, LogFile: request.LogFile, Error: "profile copy failed: " + err.Error(), ExitCode: 4}
	}
	reporter(Event{
		"event":                 "run_started",
		"mode":                  "single",
		"run_mode":              runMode,
		"vendor_count":          1,
		"url_count":             len(items),
		"workers":               1,
		"headless":              request.BrowserOptions.Headless,
		"rating_only":           request.RatingOnly,
		"extract_real_price":    request.ExtractRealPrice,
		"extract_product_info":  request.ExtractProductInfo,
		"delay_profile":         request.DelayOptions.Profile,
		"price_quality_retries": request.PriceQualityRetry.MaxRetries,
		"output":                absPath(outputPath),
		"log_file":              absPath(request.LogFile),
		"state_dir":             absPath(state.StateDir),
		"profile_seed_dir":      absPath(state.ProfileSeedDir),
	})
	preflight(request, reporter)
	powerGuard := startSystemAwakeGuard(reporter)
	defer powerGuard.close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	forceClose := make(chan struct{}, 1)
	stopCount := installStopHandlers(request.StopGraceSeconds, reporter, cancel, func() { signalOnce(forceClose) })
	defer stopCount.restore()

	qualityFailedURLs := 0
	qualityRecoveredURLs := 0
	qualityFailures := []qualityFailureSummary{}
	scraperReporter := func(event Event) {
		if event["event"] == "url_quality_failed" {
			qualityFailedURLs++
			qualityFailures = append(qualityFailures, qualityFailureSummaryFromEvent(event))
		}
		if event["event"] == "price_quality_recovered" {
			qualityRecoveredURLs++
		}
		reporter(event)
	}
	scraper := newTripScraper(request, scraperReporter, defaultVendor, 1, len(items))
	rows := []map[string]string{}
	done := 0
	go func() {
		<-forceClose
		scraper.close()
	}()
	defer scraper.close()

	if err := scraper.start(ctx); err != nil {
		if ctx.Err() != nil {
			return TaskResult{Success: false, OutputPath: outputPath, LogFile: request.LogFile, Error: "stopped by signal", ExitCode: 130}
		}
		return TaskResult{Success: false, OutputPath: outputPath, LogFile: request.LogFile, Error: err.Error(), ExitCode: 4}
	}
	for index, item := range items {
		if ctx.Err() != nil {
			break
		}
		itemRows := scraper.scrapeOne(ctx, item, index+1, len(items))
		rows = append(rows, itemRows...)
		done++
		reporter(Event{
			"event":        "progress",
			"vendors_done": 0,
			"vendor_count": 1,
			"urls_done":    done,
			"url_count":    len(items),
			"active":       []string{fmt.Sprintf("%s:%d/%d:done", defaultVendor, done, len(items))},
			"failed":       0,
		})
		if index < len(items)-1 {
			scraper.cleanupAfterURL()
			_ = scraper.maybeRestart(ctx)
		}
	}
	validationFailures := 0
	var validationErr error
	if !request.RatingOnly && len(rows) > 0 {
		validationFailures, validationErr = validateSinglePriceScrapeRows(rows)
	}
	if validationErr == nil && !scraper.saveSession() {
		reporter(Event{"event": "warning", "stage": "session_save", "message": "session save failed"})
	}
	if validationErr == nil && state.SessionExplicit {
		if raw, err := os.ReadFile(request.BrowserOptions.SessionFile); err == nil {
			_ = ensureParent(state.SessionFile)
			_ = os.WriteFile(state.SessionFile, raw, 0o644)
		}
	}
	stateRows := len(rows)
	if validationErr != nil {
		stateRows = 0
	}
	resultForState := workerResult{Vendor: defaultVendor, WorkerID: 1, Success: len(rows) > 0 && validationErr == nil, Rows: stateRows, SessionFile: request.BrowserOptions.SessionFile, ProfileDir: request.BrowserOptions.UserDataDir}
	if err := consolidateRuntimeState([]workerResult{resultForState}, state, reporter); err != nil {
		reporter(Event{"event": "warning", "stage": "state_consolidate", "message": "state consolidation failed", "error": err.Error()})
	} else {
		_ = os.RemoveAll(filepath.Join(filepath.Dir(outputPath), "_workers"))
	}
	summary := newQualityRunSummary(qualityFailures, qualityRecoveredURLs)
	emitQualityRunSummary(filepath.Dir(outputPath), summary, reporter)
	if len(rows) > 0 {
		savedPath, err := exportRows(rows, outputPath, request.RatingOnly, request.ExtractRealPrice, request.ExtractProductInfo, false)
		if err != nil {
			return TaskResult{Success: false, OutputPath: outputPath, ProcessedCount: len(rows), LogFile: request.LogFile, Error: err.Error(), ExitCode: 4, QualityFailedURLs: qualityFailedURLs, QualityRecoveredURLs: qualityRecoveredURLs}
		}
		if ctx.Err() != nil {
			reporter(Event{"event": "partial_export_done", "vendor": defaultVendor, "rows": len(rows), "output_path": savedPath})
			return TaskResult{Success: false, OutputPath: savedPath, ProcessedCount: len(rows), LogFile: request.LogFile, Error: "stopped by signal", ExitCode: 130, QualityFailedURLs: qualityFailedURLs, QualityRecoveredURLs: qualityRecoveredURLs}
		}
		reporter(Event{"event": "export_done", "vendor": defaultVendor, "rows": len(rows), "output_path": savedPath})
		if validationErr != nil {
			finalQualityFailedURLs := max(qualityFailedURLs, validationFailures)
			reporter(Event{"event": "warning", "stage": "quality", "message": validationErr.Error(), "final_quality_failed_urls": finalQualityFailedURLs})
			return TaskResult{Success: false, OutputPath: savedPath, ProcessedCount: len(rows), LogFile: request.LogFile, Error: validationErr.Error(), ExitCode: 4, QualityFailedURLs: finalQualityFailedURLs, QualityRecoveredURLs: qualityRecoveredURLs, FinalQualityFailedURLs: finalQualityFailedURLs}
		}
		return TaskResult{Success: true, OutputPath: savedPath, ProcessedCount: len(rows), LogFile: request.LogFile, ExitCode: 0, QualityFailedURLs: qualityFailedURLs, QualityRecoveredURLs: qualityRecoveredURLs}
	}
	if ctx.Err() != nil {
		return TaskResult{Success: false, OutputPath: outputPath, LogFile: request.LogFile, Error: "stopped by signal", ExitCode: 130, QualityFailedURLs: qualityFailedURLs, QualityRecoveredURLs: qualityRecoveredURLs}
	}
	return TaskResult{Success: false, OutputPath: outputPath, LogFile: request.LogFile, Error: "no score/price data captured", ExitCode: 4, QualityFailedURLs: qualityFailedURLs, QualityRecoveredURLs: qualityRecoveredURLs}
}

func scrapeVendorBatches(request TaskRequest, reporter Reporter, runMode string) TaskResult {
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
	outputDir := expandPath(request.OutputDir)
	if outputDir == "" {
		outputDir = defaultVendorOutputDir(stamp)
	}
	_ = os.MkdirAll(outputDir, 0o755)
	state := resolveRuntimeState(request)
	for _, warning := range state.Warnings {
		reporter(Event{"event": "warning", "stage": "state", "message": warning})
	}
	if state.Migrated {
		reporter(Event{"event": "state_migrated", "state_dir": state.StateDir})
	}
	request.BrowserOptions.SessionFile = state.SessionFile
	reporter(Event{
		"event":                 "run_started",
		"mode":                  "excel",
		"run_mode":              runMode,
		"vendor_count":          len(groups),
		"url_count":             len(items),
		"workers":               workers,
		"headless":              request.BrowserOptions.Headless,
		"rating_only":           request.RatingOnly,
		"extract_real_price":    request.ExtractRealPrice,
		"extract_product_info":  request.ExtractProductInfo,
		"delay_profile":         request.DelayOptions.Profile,
		"price_quality_retries": request.PriceQualityRetry.MaxRetries,
		"output_dir":            absPath(outputDir),
		"log_file":              absPath(request.LogFile),
		"state_dir":             absPath(state.StateDir),
		"profile_seed_dir":      absPath(state.ProfileSeedDir),
	})
	preflight(request, reporter)
	powerGuard := startSystemAwakeGuard(reporter)
	defer powerGuard.close()

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
			reporter(Event{"event": "runtime_deadline_expired", "stage": "runtime", "deadline": request.Runtime.DeadlineHHMM, "message": "deadline already expired; shutting down gracefully"})
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

	scheduler := newVendorScheduler(groups)
	results := []workerResult{}
	outputPaths := []string{}
	active := 0
	nextID := 0
	completed := 0
	urlsDone := 0
	failures := 0
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
		outputPath := filepath.Join(outputDir, fmt.Sprintf("%s_%s.xlsx", safeFilename(group.FileVendor), stamp))
		if len(groups) == 1 && request.OutputPath != "" {
			outputPath = expandPath(request.OutputPath)
		}
		workerDir := filepath.Join(outputDir, "_workers", fmt.Sprintf("%02d_%s", workerID, safeFilename(group.FileVendor)))
		workerRequest := request
		workerRequest.OutputPath = outputPath
		workerRequest.BrowserOptions.SessionFile = filepath.Join(workerDir, "session.json")
		workerRequest.BrowserOptions.UserDataDir = filepath.Join(workerDir, "profile")
		if workerRequest.BrowserOptions.Language == "" {
			workerRequest.BrowserOptions.Language = siteConfigFor(workerRequest.Site).AcceptLanguage
		}
		var sessionPrepareErr error
		if err := ensureParent(workerRequest.BrowserOptions.SessionFile); err != nil {
			sessionPrepareErr = err
		}
		if workerRequest.FreshProfile {
			if sessionPrepareErr == nil {
				sessionPrepareErr = prepareRuntimeSession("", workerRequest.BrowserOptions.SessionFile, true)
			}
		} else if base := expandPath(request.BrowserOptions.SessionFile); base != "" {
			if raw, err := os.ReadFile(base); err == nil {
				if sessionPrepareErr == nil {
					sessionPrepareErr = os.WriteFile(workerRequest.BrowserOptions.SessionFile, raw, 0o644)
				}
			}
		}
		if sessionPrepareErr != nil {
			go func() {
				resultCh <- workerResult{Vendor: group.Vendor, WorkerID: workerID, Success: false, OutputPath: outputPath, SessionFile: workerRequest.BrowserOptions.SessionFile, ProfileDir: workerRequest.BrowserOptions.UserDataDir, Error: "session prepare failed: " + sessionPrepareErr.Error(), ErrorType: fmt.Sprintf("%T", sessionPrepareErr), Stage: "session_prepare", URLCount: len(group.Items), LogFile: request.LogFile}
			}()
			return ctx.Err() == nil
		}
		baseProfile := state.ProfileSeedDir
		reporter(Event{"event": "browser_prepare", "stage": "profile_copy", "vendor": group.Vendor, "worker_id": workerID, "message": "copying Chrome profile for worker", "source_profile_dir": baseProfile, "profile_dir": workerRequest.BrowserOptions.UserDataDir})
		if err := prepareRuntimeProfile(baseProfile, workerRequest.BrowserOptions.UserDataDir, workerRequest.FreshProfile); err != nil {
			go func() {
				resultCh <- workerResult{Vendor: group.Vendor, WorkerID: workerID, Success: false, OutputPath: outputPath, SessionFile: workerRequest.BrowserOptions.SessionFile, ProfileDir: workerRequest.BrowserOptions.UserDataDir, Error: "profile copy failed: " + err.Error(), ErrorType: fmt.Sprintf("%T", err), Stage: "profile_copy", URLCount: len(group.Items), LogFile: request.LogFile}
			}()
			return ctx.Err() == nil
		}
		reporter(Event{"event": "worker_spawn_started", "vendor": group.Vendor, "worker_id": workerID, "message": "spawning worker"})
		workerCtx, workerCancel := context.WithCancel(ctx)
		forceWorkers.add(workerCancel)
		go runVendorWorker(workerCtx, workerID, group, workerRequest, outputPath, len(items), workerReporter, resultCh, forceWorkers.add, qualityRerunLimiter)
		reporter(Event{"event": "vendor_started", "vendor": group.Vendor, "worker_id": workerID, "url_count": len(group.Items)})
		if request.StartupStagger > 0 {
			return cancellableSleep(ctx, request.StartupStagger)
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

	go func() {
		<-force
		forceWorkers.run()
	}()

	for active > 0 {
		result := <-resultCh
		active--
		completed++
		if vendorAPIBlockedByResult(result, request.Runtime) {
			result.FinalState = "api_blocked"
			result.FinalReason = "vendor API block threshold reached"
			reporter(Event{"event": "vendor_api_blocked", "stage": "runtime", "vendor": result.Vendor, "worker_id": result.WorkerID, "api_blocked_urls": result.APIBlockedURLs, "urls_done": result.URLsDone, "url_count": result.URLCount, "threshold": request.Runtime.VendorAPIBlockRatio})
		}
		results = append(results, result)
		urlsDone += result.URLsDone
		if !result.Success {
			failures++
		}
		if result.OutputPath != "" && (result.Success || result.Rows > 0) {
			outputPaths = append(outputPaths, result.OutputPath)
		}
		reporter(result.Event())
		reporter(Event{
			"event":        "progress",
			"vendors_done": completed,
			"vendor_count": len(groups),
			"urls_done":    urlsDone,
			"url_count":    len(items),
			"active":       []string{},
			"failed":       failures,
		})
		if globalAPIBlocked(urlsDone, runtimeSummaryAPIBlocked(results), request.Runtime) {
			reporter(Event{"event": "global_api_blocked", "stage": "runtime", "api_blocked_urls": runtimeSummaryAPIBlocked(results), "processed_urls": urlsDone, "threshold": request.Runtime.GlobalAPIBlockRatio})
			cancel()
		}
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

	if state.SessionExplicit {
		for _, result := range results {
			if result.Success && result.SessionFile != "" {
				if raw, err := os.ReadFile(result.SessionFile); err == nil {
					_ = ensureParent(state.SessionFile)
					_ = os.WriteFile(expandPath(state.SessionFile), raw, 0o644)
				}
				break
			}
		}
	}
	if err := consolidateRuntimeState(results, state, reporter); err != nil {
		reporter(Event{"event": "warning", "stage": "state_consolidate", "message": "state consolidation failed", "error": err.Error()})
	} else {
		_ = os.RemoveAll(filepath.Join(outputDir, "_workers"))
	}
	rows := 0
	failedVendors := []string{}
	qualityFailedURLs := 0
	qualityRecoveredURLs := 0
	qualityRerunAttemptedURLs := 0
	qualityRerunRecoveredURLs := 0
	finalQualityFailedURLs := 0
	qualityFailures := []qualityFailureSummary{}
	for _, result := range results {
		rows += result.Rows
		qualityFailedURLs += result.QualityFailedURLs
		qualityRecoveredURLs += result.QualityRecoveredURLs
		qualityRerunAttemptedURLs += result.QualityRerunAttemptedURLs
		qualityRerunRecoveredURLs += result.QualityRerunRecoveredURLs
		finalQualityFailedURLs += result.FinalQualityFailedURLs
		qualityFailures = append(qualityFailures, result.QualityFailures...)
		if !result.Success {
			failedVendors = append(failedVendors, result.Vendor)
		}
	}
	summary := newQualityRunSummary(qualityFailures, qualityRecoveredURLs)
	emitQualityRunSummary(outputDir, summary, reporter)
	runtimeSummary := runtimeRunSummary{Event: "runtime_summary", GlobalState: "done", ProcessedURLs: urlsDone, URLCount: len(items), DeadlineExpired: deadlineExpired.Load()}
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
	if deadlineExpired.Load() {
		runtimeSummary.GlobalState = "partial"
		runtimeSummary.Reason = "deadline"
	}
	writeRuntimeSummary(outputDir, runtimeSummary, reporter)
	if ctx.Err() != nil {
		return TaskResult{Success: false, OutputPath: outputDir, OutputPaths: outputPaths, ProcessedCount: rows, Warnings: warnings, FailedVendors: failedVendors, LogFile: request.LogFile, Error: "stopped by signal", ExitCode: 130, QualityFailedURLs: finalQualityFailedURLs, QualityRecoveredURLs: qualityRecoveredURLs, QualityRerunAttemptedURLs: qualityRerunAttemptedURLs, QualityRerunRecoveredURLs: qualityRerunRecoveredURLs, FinalQualityFailedURLs: finalQualityFailedURLs}
	}
	if len(failedVendors) > 0 {
		return TaskResult{Success: false, OutputPath: outputDir, OutputPaths: outputPaths, ProcessedCount: rows, Warnings: warnings, FailedVendors: failedVendors, LogFile: request.LogFile, Error: summarizeFailures(results), ExitCode: 4, QualityFailedURLs: finalQualityFailedURLs, QualityRecoveredURLs: qualityRecoveredURLs, QualityRerunAttemptedURLs: qualityRerunAttemptedURLs, QualityRerunRecoveredURLs: qualityRerunRecoveredURLs, FinalQualityFailedURLs: finalQualityFailedURLs}
	}
	if rows == 0 && len(groups) > 0 {
		for _, group := range groups {
			failedVendors = append(failedVendors, group.Vendor)
		}
		return TaskResult{Success: false, OutputPath: outputDir, OutputPaths: outputPaths, ProcessedCount: 0, Warnings: warnings, FailedVendors: failedVendors, LogFile: request.LogFile, Error: "no rows captured and no successful worker output", ExitCode: 4, QualityFailedURLs: finalQualityFailedURLs, QualityRecoveredURLs: qualityRecoveredURLs, QualityRerunAttemptedURLs: qualityRerunAttemptedURLs, QualityRerunRecoveredURLs: qualityRerunRecoveredURLs, FinalQualityFailedURLs: finalQualityFailedURLs}
	}

	// Run vendor comparison if requested
	if request.CompareVendor != "" && request.ExcelInput != "" {
		compareResult, compareErr := CompareVendors(request.ExcelInput, request.CompareVendor, outputDir, stamp, reporter)
		if compareErr != nil {
			reporter(Event{"event": "warning", "stage": "vendor_compare", "message": fmt.Sprintf("vendor comparison failed: %v", compareErr)})
		} else if compareResult != nil {
			outputPaths = append(outputPaths, compareResult.OutputPath)
		}
	}

	return TaskResult{Success: true, OutputPath: outputDir, OutputPaths: outputPaths, ProcessedCount: rows, Warnings: warnings, LogFile: request.LogFile, ExitCode: 0, QualityFailedURLs: finalQualityFailedURLs, QualityRecoveredURLs: qualityRecoveredURLs, QualityRerunAttemptedURLs: qualityRerunAttemptedURLs, QualityRerunRecoveredURLs: qualityRerunRecoveredURLs, FinalQualityFailedURLs: finalQualityFailedURLs}
}

func prepareRuntimeProfile(seedDir, runtimeDir string, fresh bool) error {
	if fresh {
		return os.MkdirAll(runtimeDir, 0o755)
	}
	return replaceProfileDir(seedDir, runtimeDir)
}

func prepareRuntimeSession(seedPath, runtimePath string, fresh bool) error {
	if err := ensureParent(runtimePath); err != nil {
		return err
	}
	if fresh {
		return writeEmptySession(runtimePath)
	}
	raw, err := os.ReadFile(seedPath)
	if err != nil {
		return err
	}
	return os.WriteFile(runtimePath, raw, 0o644)
}

type stopControl struct {
	restore func()
}

func installStopHandlers(grace int, reporter Reporter, cancel context.CancelFunc, force func()) stopControl {
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		attempt := 0
		for {
			select {
			case <-done:
				return
			case sig := <-signals:
				attempt++
				if attempt == 1 {
					reporter(Event{"event": "stop_requested", "message": fmt.Sprintf("received signal %s; graceful stop requested", sig), "grace_seconds": grace, "attempt": 1, "mode": "graceful"})
					cancel()
					if grace >= 0 {
						go func() {
							time.Sleep(time.Duration(grace) * time.Second)
							reporter(Event{"event": "warning", "message": "stop grace period exceeded; terminating remaining workers"})
							force()
						}()
					}
					continue
				}
				reporter(Event{"event": "force_stop_requested", "message": fmt.Sprintf("received signal %s; forcing browser shutdown", sig), "attempt": attempt, "mode": "force"})
				force()
			}
		}
	}()
	return stopControl{restore: func() {
		signal.Stop(signals)
		close(done)
	}}
}

func preflight(request TaskRequest, reporter Reporter) {
	reporter(Event{"event": "browser_prepare", "message": "go=cdp"})
	reporter(Event{"event": "browser_prepare", "message": fmt.Sprintf("platform=%s", runtimePlatform())})
}

func emitInputAudit(path string, reporter Reporter) {
	if strings.TrimSpace(path) == "" || reporter == nil {
		return
	}
	audit, err := auditExcelInput(path)
	if err != nil {
		reporter(Event{"event": "warning", "stage": "input_audit", "message": "input audit failed", "error": err.Error()})
		return
	}
	reporter(Event{
		"event":                "input_audit",
		"stage":                "input",
		"sheet":                audit.SheetName,
		"dimension":            audit.Dimension,
		"trip_url_count":       audit.TripURLCount,
		"vendor_count":         audit.VendorCount,
		"vendor_url_counts":    audit.VendorURLCounts,
		"empty_rows":           audit.EmptyRows,
		"interior_empty_rows":  audit.InteriorEmptyRows,
		"non_trip_link_count":  audit.NonTripLinkCount,
		"blank_vendor_columns": audit.BlankVendorColumns,
		"max_rows":             audit.MaxRows,
		"max_cols":             audit.MaxCols,
	})
	if audit.InteriorEmptyRows > 0 {
		reporter(Event{"event": "warning", "stage": "input_audit", "message": fmt.Sprintf("input workbook has %d empty row(s) inside the used range; sparse scan will continue after blanks", audit.InteriorEmptyRows), "interior_empty_rows": audit.InteriorEmptyRows})
	}
}

func runtimePlatform() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}

func signalOnce(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

type forceStopper struct {
	mu    sync.Mutex
	funcs []func()
}

func (f *forceStopper) add(fn func()) {
	if fn == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.funcs = append(f.funcs, fn)
}

func (f *forceStopper) run() {
	f.mu.Lock()
	funcs := append([]func(){}, f.funcs...)
	f.mu.Unlock()
	for _, fn := range funcs {
		fn()
	}
}

type globalRiskPause struct {
	mu         sync.Mutex
	profile    string
	reporter   Reporter
	recent     []time.Time
	pauseUntil time.Time
}

func newGlobalRiskPause(profile string, reporter Reporter) *globalRiskPause {
	return &globalRiskPause{profile: profile, reporter: reporter}
}

func (p *globalRiskPause) observe(event Event) {
	if p == nil || event["event"] != "risk_challenge_detected" || event["risk_kind"] != "whaleguard" {
		return
	}
	now := time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	cutoff := now.Add(-5 * time.Minute)
	filtered := p.recent[:0]
	for _, ts := range p.recent {
		if ts.After(cutoff) {
			filtered = append(filtered, ts)
		}
	}
	p.recent = append(filtered, now)
	if len(p.recent) < 2 {
		return
	}
	cooldown := riskCooldownDelay(p.profile)
	until := now.Add(cooldown)
	if until.After(p.pauseUntil) {
		p.pauseUntil = until
		if p.reporter != nil {
			p.reporter(Event{"event": "risk_global_cooldown", "stage": "risk", "message": "multiple whaleguard challenges detected; pausing new worker starts", "risk_kind": "whaleguard", "cooldown_ms": cooldown.Milliseconds()})
		}
	}
}

func (p *globalRiskPause) wait(ctx context.Context) bool {
	if p == nil {
		return ctx.Err() == nil
	}
	p.mu.Lock()
	until := p.pauseUntil
	p.mu.Unlock()
	delay := time.Until(until)
	if delay <= 0 {
		return ctx.Err() == nil
	}
	return cancellableSleep(ctx, delay)
}

func canStartWorker(ctx context.Context, pending int, active int, workers int) bool {
	return ctx.Err() == nil && pending > 0 && active < workers
}

func vendorAPIBlockedByResult(result workerResult, options RuntimeOptions) bool {
	return ratioReached(result.APIBlockedURLs, result.URLsDone, options.VendorAPIBlockRatio, options.VendorAPIBlockMinAttempts)
}

func globalAPIBlocked(processed int, apiBlocked int, options RuntimeOptions) bool {
	return ratioReached(apiBlocked, processed, options.GlobalAPIBlockRatio, options.GlobalAPIBlockMinProcessed)
}

func runtimeSummaryAPIBlocked(results []workerResult) int {
	total := 0
	for _, result := range results {
		total += result.APIBlockedURLs
	}
	return total
}

func checkpointOutputPath(outputPath string, vendor string, done int) string {
	dir := filepath.Dir(outputPath)
	return filepath.Join(dir, fmt.Sprintf("%s_partial_%03d.xlsx", safeFilename(vendor), done))
}

func cancellableSleep(ctx context.Context, duration time.Duration) bool {
	if duration <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

type workerResult struct {
	Vendor                    string
	WorkerID                  int
	Success                   bool
	Stopped                   bool
	Rows                      int
	OutputPath                string
	SessionFile               string
	ProfileDir                string
	Error                     string
	ErrorType                 string
	Stage                     string
	URLCount                  int
	URLsDone                  int
	LogFile                   string
	QualityFailedURLs         int
	QualityRecoveredURLs      int
	QualityRerunAttemptedURLs int
	QualityRerunRecoveredURLs int
	FinalQualityFailedURLs    int
	QualityFailures           []qualityFailureSummary
	APIBlockedURLs            int
	RiskEvents                int
	FinalState                string
	FinalReason               string
}

func (r workerResult) Event() Event {
	return Event{
		"event":                        "vendor_done",
		"vendor":                       r.Vendor,
		"worker_id":                    r.WorkerID,
		"success":                      r.Success,
		"stopped":                      r.Stopped,
		"rows":                         r.Rows,
		"output_path":                  r.OutputPath,
		"session_file":                 r.SessionFile,
		"profile_dir":                  r.ProfileDir,
		"error":                        r.Error,
		"error_type":                   r.ErrorType,
		"stage":                        r.Stage,
		"log_file":                     r.LogFile,
		"url_count":                    r.URLCount,
		"urls_done":                    r.URLsDone,
		"quality_failed_urls":          r.QualityFailedURLs,
		"quality_recovered_urls":       r.QualityRecoveredURLs,
		"quality_rerun_attempted_urls": r.QualityRerunAttemptedURLs,
		"quality_rerun_recovered_urls": r.QualityRerunRecoveredURLs,
		"final_quality_failed_urls":    r.FinalQualityFailedURLs,
		"api_blocked_urls":             r.APIBlockedURLs,
		"risk_events":                  r.RiskEvents,
		"final_state":                  r.FinalState,
		"final_reason":                 r.FinalReason,
	}
}

func runVendorWorker(ctx context.Context, workerID int, group vendorGroup, request TaskRequest, outputPath string, globalTotal int, reporter Reporter, resultCh chan<- workerResult, registerForceClose func(func()), qualityRerunLimiter chan struct{}) {
	rows := []map[string]string{}
	result := workerResult{Vendor: group.Vendor, WorkerID: workerID, OutputPath: outputPath, SessionFile: request.BrowserOptions.SessionFile, ProfileDir: request.BrowserOptions.UserDataDir, URLCount: len(group.Items), LogFile: request.LogFile}
	workerCtx, workerCancel := context.WithCancel(ctx)
	defer workerCancel()
	watchdog := newWorkerProgressWatchdog(request.Runtime.WorkerStallTimeout, reporter, group.Vendor, workerID, workerCancel)
	defer watchdog.stop()
	localReporter := func(event Event) {
		if event["event"] != "url_quality_failed" && (event["failure_class"] == FailureAPIBlockedSuspected || event["failure_class"] == string(FailureAPIBlockedSuspected)) {
			result.APIBlockedURLs++
		}
		if event["event"] == "risk_challenge_detected" {
			result.RiskEvents++
		}
		if event["event"] == "url_quality_failed" {
			class := FailureClass(fmt.Sprint(event["failure_class"]))
			if class == "" {
				class = ClassifyError(nil, fmt.Sprint(event["reason"]))
			}
			if class == FailureAPIBlockedSuspected {
				result.APIBlockedURLs++
			}
			result.QualityFailedURLs++
			result.QualityFailures = append(result.QualityFailures, qualityFailureSummaryFromEvent(event))
		}
		if event["event"] == "price_quality_recovered" {
			result.QualityRecoveredURLs++
		}
		event["vendor"] = group.Vendor
		event["worker_id"] = workerID
		event["output_path"] = outputPath
		event["log_file"] = request.LogFile
		watchdog.mark(fmt.Sprint(event["event"]))
		reporter(event)
	}
	scraper := newTripScraper(request, localReporter, group.Vendor, workerID, globalTotal)
	if registerForceClose != nil {
		registerForceClose(scraper.close)
	}
	defer func() { resultCh <- result }()
	defer scraper.close()
	localReporter(Event{"event": "process_started", "stage": "process_started", "message": "worker process started", "url_count": len(group.Items)})
	localReporter(Event{"event": "runtime_init_started", "stage": "runtime_init", "message": "initializing worker runtime"})
	if err := scraper.start(workerCtx); err != nil {
		result.Error = err.Error()
		result.ErrorType = fmt.Sprintf("%T", err)
		result.Stage = "browser_start"
		localReporter(Event{"event": "worker_failed", "stage": result.Stage, "error": result.Error, "error_type": result.ErrorType, "rows": 0, "url_count": len(group.Items)})
		return
	}
	for index, item := range group.Items {
		if workerCtx.Err() != nil {
			result.Stopped = true
			break
		}
		itemRows := scraper.scrapeOne(workerCtx, item, index+1, len(group.Items))
		rows = append(rows, itemRows...)
		result.URLsDone++
		if request.Runtime.CheckpointEveryURLs > 0 && result.URLsDone%request.Runtime.CheckpointEveryURLs == 0 && len(rows) > 0 {
			checkpointPath := checkpointOutputPath(outputPath, group.FileVendor, result.URLsDone)
			if savedPath, err := exportRows(rows, checkpointPath, request.RatingOnly, request.ExtractRealPrice, request.ExtractProductInfo, true); err == nil {
				localReporter(Event{"event": "partial_checkpoint_done", "stage": "export", "rows": len(rows), "urls_done": result.URLsDone, "output_path": savedPath})
			} else {
				localReporter(Event{"event": "warning", "stage": "export", "message": "partial checkpoint failed", "error": err.Error(), "urls_done": result.URLsDone})
			}
		}
		if index < len(group.Items)-1 {
			scraper.cleanupAfterURL()
			_ = scraper.maybeRestart(workerCtx)
		}
	}
	if !scraper.saveSession() {
		localReporter(Event{"event": "warning", "stage": "session_save", "message": "session save failed"})
	}
	scraper.close()
	rows, rerunStats := runQualityReruns(workerCtx, rows, request, group.Vendor, outputPath, globalTotal, localReporter, qualityRerunLimiter)
	result.QualityRerunAttemptedURLs = rerunStats.Attempted
	result.QualityRerunRecoveredURLs = rerunStats.Recovered
	result.FinalQualityFailedURLs = rerunStats.FinalFailed
	result.QualityFailedURLs = rerunStats.FinalFailed
	result.QualityFailures = filterQualityFailuresForRows(result.QualityFailures, rows)
	if len(rows) > 0 {
		savedPath, err := exportRows(rows, outputPath, request.RatingOnly, request.ExtractRealPrice, request.ExtractProductInfo, true)
		if err != nil {
			result.Error = "export failed: " + err.Error()
			result.ErrorType = "ExportFailed"
			result.Stage = "export"
			return
		}
		result.OutputPath = savedPath
		result.Rows = len(rows)
		eventName := "export_done"
		if result.Stopped || workerCtx.Err() != nil {
			eventName = "partial_export_done"
		}
		localReporter(Event{"event": eventName, "rows": len(rows), "output_path": savedPath})
	}
	if result.Stopped || workerCtx.Err() != nil {
		result.Stopped = true
		result.Error = "stopped by signal or watchdog"
		result.Stage = "stopped"
		return
	}
	if len(rows) == 0 {
		result.Error = "no rows captured"
		result.Stage = "done"
		return
	}
	result.Success = true
	result.Stage = "done"
}

func summarizeFailures(results []workerResult) string {
	parts := []string{}
	for _, result := range results {
		if !result.Success {
			parts = append(parts, fmt.Sprintf("%s: %s", result.Vendor, result.Error))
		}
	}
	return strings.Join(parts, "; ")
}
