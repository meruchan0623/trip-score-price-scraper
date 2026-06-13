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
	"syscall"
	"time"
)

func scrape(request TaskRequest, reporter Reporter, runMode string) TaskResult {
	if request.ExcelInput != "" {
		return scrapeVendorBatches(request, reporter, runMode)
	}
	return scrapeSingle(request, reporter, runMode)
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
	state := resolveRuntimeState(request)
	for _, warning := range state.Warnings {
		reporter(Event{"event": "warning", "stage": "state", "message": warning})
	}
	if state.Migrated {
		reporter(Event{"event": "state_migrated", "state_dir": state.StateDir})
	}
	if request.BrowserOptions.Language == "" {
		request.BrowserOptions.Language = preferredLanguageForItems(items)
	}
	runtimeDir := filepath.Join(filepath.Dir(outputPath), "_workers", "01_"+safeFilename(defaultVendor))
	request.BrowserOptions.SessionFile = filepath.Join(runtimeDir, "session.json")
	request.BrowserOptions.UserDataDir = filepath.Join(runtimeDir, "profile")
	_ = ensureParent(request.BrowserOptions.SessionFile)
	if raw, err := os.ReadFile(state.SessionFile); err == nil {
		_ = os.WriteFile(request.BrowserOptions.SessionFile, raw, 0o644)
	} else if !state.SessionExplicit {
		_ = writeEmptySession(request.BrowserOptions.SessionFile)
	}
	if err := replaceProfileDir(state.ProfileSeedDir, request.BrowserOptions.UserDataDir); err != nil {
		return TaskResult{Success: false, OutputPath: outputPath, LogFile: request.LogFile, Error: "profile copy failed: " + err.Error(), ExitCode: 4}
	}
	reporter(Event{
		"event":                "run_started",
		"mode":                 "single",
		"run_mode":             runMode,
		"vendor_count":         1,
		"url_count":            len(items),
		"workers":              1,
		"headless":             request.BrowserOptions.Headless,
		"rating_only":          request.RatingOnly,
		"extract_real_price":   request.ExtractRealPrice,
		"extract_product_info": request.ExtractProductInfo,
		"delay_profile":        request.DelayOptions.Profile,
		"output":               absPath(outputPath),
		"log_file":             absPath(request.LogFile),
		"state_dir":            absPath(state.StateDir),
		"profile_seed_dir":     absPath(state.ProfileSeedDir),
	})
	preflight(request, reporter)
	powerGuard := startSystemAwakeGuard(reporter)
	defer powerGuard.close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	forceClose := make(chan struct{}, 1)
	stopCount := installStopHandlers(request.StopGraceSeconds, reporter, cancel, func() { signalOnce(forceClose) })
	defer stopCount.restore()

	scraper := newTripScraper(request, reporter, defaultVendor, 1, len(items))
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
	if !scraper.saveSession() {
		reporter(Event{"event": "warning", "stage": "session_save", "message": "session save failed"})
	}
	if state.SessionExplicit {
		if raw, err := os.ReadFile(request.BrowserOptions.SessionFile); err == nil {
			_ = ensureParent(state.SessionFile)
			_ = os.WriteFile(state.SessionFile, raw, 0o644)
		}
	}
	resultForState := workerResult{Vendor: defaultVendor, WorkerID: 1, Success: len(rows) > 0, Rows: len(rows), SessionFile: request.BrowserOptions.SessionFile, ProfileDir: request.BrowserOptions.UserDataDir}
	if err := consolidateRuntimeState([]workerResult{resultForState}, state, reporter); err != nil {
		reporter(Event{"event": "warning", "stage": "state_consolidate", "message": "state consolidation failed", "error": err.Error()})
	} else {
		_ = os.RemoveAll(filepath.Join(filepath.Dir(outputPath), "_workers"))
	}
	if len(rows) > 0 {
		savedPath, err := exportRows(rows, outputPath, request.RatingOnly, request.ExtractRealPrice, request.ExtractProductInfo, false)
		if err != nil {
			return TaskResult{Success: false, OutputPath: outputPath, ProcessedCount: len(rows), LogFile: request.LogFile, Error: err.Error(), ExitCode: 4}
		}
		if ctx.Err() != nil {
			reporter(Event{"event": "partial_export_done", "vendor": defaultVendor, "rows": len(rows), "output_path": savedPath})
			return TaskResult{Success: false, OutputPath: savedPath, ProcessedCount: len(rows), LogFile: request.LogFile, Error: "stopped by signal", ExitCode: 130}
		}
		reporter(Event{"event": "export_done", "vendor": defaultVendor, "rows": len(rows), "output_path": savedPath})
		return TaskResult{Success: true, OutputPath: savedPath, ProcessedCount: len(rows), LogFile: request.LogFile, ExitCode: 0}
	}
	if ctx.Err() != nil {
		return TaskResult{Success: false, OutputPath: outputPath, LogFile: request.LogFile, Error: "stopped by signal", ExitCode: 130}
	}
	return TaskResult{Success: false, OutputPath: outputPath, LogFile: request.LogFile, Error: "no score/price data captured", ExitCode: 4}
}

func scrapeVendorBatches(request TaskRequest, reporter Reporter, runMode string) TaskResult {
	items, warnings, err := loadItems(request)
	if err != nil {
		return TaskResult{Success: false, Error: err.Error(), ExitCode: 2}
	}
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
		"output_dir":           absPath(outputDir),
		"log_file":             absPath(request.LogFile),
		"state_dir":            absPath(state.StateDir),
		"profile_seed_dir":     absPath(state.ProfileSeedDir),
	})
	preflight(request, reporter)
	powerGuard := startSystemAwakeGuard(reporter)
	defer powerGuard.close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	force := make(chan struct{}, 1)
	stopCtl := installStopHandlers(request.StopGraceSeconds, reporter, cancel, func() { signalOnce(force) })
	defer stopCtl.restore()
	riskPause := newGlobalRiskPause(request.DelayOptions.Profile, reporter)
	workerReporter := func(event Event) {
		riskPause.observe(event)
		reporter(event)
	}

	pending := append([]vendorGroup(nil), groups...)
	results := []workerResult{}
	outputPaths := []string{}
	active := 0
	nextID := 0
	completed := 0
	urlsDone := 0
	failures := 0
	resultCh := make(chan workerResult)
	forceWorkers := &forceStopper{}

	startNext := func(group vendorGroup) bool {
		if ctx.Err() != nil {
			return false
		}
		if !riskPause.wait(ctx) {
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
			workerRequest.BrowserOptions.Language = preferredLanguageForItems(group.Items)
		}
		_ = ensureParent(workerRequest.BrowserOptions.SessionFile)
		if base := expandPath(request.BrowserOptions.SessionFile); base != "" {
			if raw, err := os.ReadFile(base); err == nil {
				_ = os.WriteFile(workerRequest.BrowserOptions.SessionFile, raw, 0o644)
			}
		}
		baseProfile := state.ProfileSeedDir
		reporter(Event{"event": "browser_prepare", "stage": "profile_copy", "vendor": group.Vendor, "worker_id": workerID, "message": "copying Chrome profile for worker", "source_profile_dir": baseProfile, "profile_dir": workerRequest.BrowserOptions.UserDataDir})
		if err := replaceProfileDir(baseProfile, workerRequest.BrowserOptions.UserDataDir); err != nil {
			go func() {
				resultCh <- workerResult{Vendor: group.Vendor, WorkerID: workerID, Success: false, OutputPath: outputPath, SessionFile: workerRequest.BrowserOptions.SessionFile, ProfileDir: workerRequest.BrowserOptions.UserDataDir, Error: "profile copy failed: " + err.Error(), ErrorType: fmt.Sprintf("%T", err), Stage: "profile_copy", URLCount: len(group.Items), LogFile: request.LogFile}
			}()
			return ctx.Err() == nil
		}
		reporter(Event{"event": "worker_spawn_started", "vendor": group.Vendor, "worker_id": workerID, "message": "spawning worker"})
		workerCtx, workerCancel := context.WithCancel(ctx)
		forceWorkers.add(workerCancel)
		go runVendorWorker(workerCtx, workerID, group, workerRequest, outputPath, len(items), workerReporter, resultCh, forceWorkers.add)
		reporter(Event{"event": "vendor_started", "vendor": group.Vendor, "worker_id": workerID, "url_count": len(group.Items)})
		if request.StartupStagger > 0 {
			return cancellableSleep(ctx, request.StartupStagger)
		}
		return ctx.Err() == nil
	}

	for canStartWorker(ctx, len(pending), active, workers) {
		group := pending[0]
		pending = pending[1:]
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
		if canStartWorker(ctx, len(pending), active, workers) {
			group := pending[0]
			pending = pending[1:]
			_ = startNext(group)
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
	for _, result := range results {
		rows += result.Rows
		if !result.Success {
			failedVendors = append(failedVendors, result.Vendor)
		}
	}
	if ctx.Err() != nil {
		return TaskResult{Success: false, OutputPath: outputDir, OutputPaths: outputPaths, ProcessedCount: rows, Warnings: warnings, FailedVendors: failedVendors, LogFile: request.LogFile, Error: "stopped by signal", ExitCode: 130}
	}
	if len(failedVendors) > 0 {
		return TaskResult{Success: false, OutputPath: outputDir, OutputPaths: outputPaths, ProcessedCount: rows, Warnings: warnings, FailedVendors: failedVendors, LogFile: request.LogFile, Error: summarizeFailures(results), ExitCode: 4}
	}
	if rows == 0 && len(groups) > 0 {
		for _, group := range groups {
			failedVendors = append(failedVendors, group.Vendor)
		}
		return TaskResult{Success: false, OutputPath: outputDir, OutputPaths: outputPaths, ProcessedCount: 0, Warnings: warnings, FailedVendors: failedVendors, LogFile: request.LogFile, Error: "no rows captured and no successful worker output", ExitCode: 4}
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

	return TaskResult{Success: true, OutputPath: outputDir, OutputPaths: outputPaths, ProcessedCount: rows, Warnings: warnings, LogFile: request.LogFile, ExitCode: 0}
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
	Vendor      string
	WorkerID    int
	Success     bool
	Stopped     bool
	Rows        int
	OutputPath  string
	SessionFile string
	ProfileDir  string
	Error       string
	ErrorType   string
	Stage       string
	URLCount    int
	URLsDone    int
	LogFile     string
}

func (r workerResult) Event() Event {
	return Event{
		"event":        "vendor_done",
		"vendor":       r.Vendor,
		"worker_id":    r.WorkerID,
		"success":      r.Success,
		"stopped":      r.Stopped,
		"rows":         r.Rows,
		"output_path":  r.OutputPath,
		"session_file": r.SessionFile,
		"profile_dir":  r.ProfileDir,
		"error":        r.Error,
		"error_type":   r.ErrorType,
		"stage":        r.Stage,
		"log_file":     r.LogFile,
		"url_count":    r.URLCount,
		"urls_done":    r.URLsDone,
	}
}

func runVendorWorker(ctx context.Context, workerID int, group vendorGroup, request TaskRequest, outputPath string, globalTotal int, reporter Reporter, resultCh chan<- workerResult, registerForceClose func(func())) {
	localReporter := func(event Event) {
		event["vendor"] = group.Vendor
		event["worker_id"] = workerID
		event["output_path"] = outputPath
		event["log_file"] = request.LogFile
		reporter(event)
	}
	rows := []map[string]string{}
	result := workerResult{Vendor: group.Vendor, WorkerID: workerID, OutputPath: outputPath, SessionFile: request.BrowserOptions.SessionFile, ProfileDir: request.BrowserOptions.UserDataDir, URLCount: len(group.Items), LogFile: request.LogFile}
	scraper := newTripScraper(request, localReporter, group.Vendor, workerID, globalTotal)
	if registerForceClose != nil {
		registerForceClose(scraper.close)
	}
	defer func() { resultCh <- result }()
	defer scraper.close()
	localReporter(Event{"event": "process_started", "stage": "process_started", "message": "worker process started", "url_count": len(group.Items)})
	localReporter(Event{"event": "runtime_init_started", "stage": "runtime_init", "message": "initializing worker runtime"})
	if err := scraper.start(ctx); err != nil {
		result.Error = err.Error()
		result.ErrorType = fmt.Sprintf("%T", err)
		result.Stage = "browser_start"
		localReporter(Event{"event": "worker_failed", "stage": result.Stage, "error": result.Error, "error_type": result.ErrorType, "rows": 0, "url_count": len(group.Items)})
		return
	}
	for index, item := range group.Items {
		if ctx.Err() != nil {
			result.Stopped = true
			break
		}
		itemRows := scraper.scrapeOne(ctx, item, index+1, len(group.Items))
		rows = append(rows, itemRows...)
		result.URLsDone++
		if index < len(group.Items)-1 {
			scraper.cleanupAfterURL()
			_ = scraper.maybeRestart(ctx)
		}
	}
	if !scraper.saveSession() {
		localReporter(Event{"event": "warning", "stage": "session_save", "message": "session save failed"})
	}
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
		if result.Stopped || ctx.Err() != nil {
			eventName = "partial_export_done"
		}
		localReporter(Event{"event": eventName, "rows": len(rows), "output_path": savedPath})
	}
	if result.Stopped || ctx.Err() != nil {
		result.Stopped = true
		result.Error = "stopped by signal"
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
