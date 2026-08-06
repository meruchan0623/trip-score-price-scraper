package tripcdp

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type marketJobRunner func(context.Context, MarketJob, TaskRequest, Reporter, func(func())) MarketJobResult
type marketExporter func([]MarketJobResult, string, bool, bool) (string, error)

// These seams keep orchestration tests browser-free. Production always uses
// runLiveMarketJob and exportMarketRows.
var multiMarketJobRunner marketJobRunner = runLiveMarketJob
var multiMarketExporter marketExporter = exportMarketRows
var multiMarketStateResolver = resolveRuntimeState
var multiMarketPowerGuardStarter = startSystemAwakeGuard

func scrapeMultiMarkets(request TaskRequest, reporter Reporter, runMode string) TaskResult {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	forceWorkers := &forceStopper{}
	stop := installStopHandlers(request.StopGraceSeconds, reporter, cancel, forceWorkers.run)
	defer stop.restore()
	powerGuard := multiMarketPowerGuardStarter(reporter)
	defer powerGuard.close()
	return scrapeMultiMarketsContextWithForce(ctx, request, reporter, runMode, forceWorkers.add)
}

func scrapeMultiMarketsContext(ctx context.Context, request TaskRequest, reporter Reporter, runMode string) TaskResult {
	return scrapeMultiMarketsContextWithForce(ctx, request, reporter, runMode, nil)
}

func scrapeMultiMarketsContextWithForce(ctx context.Context, request TaskRequest, reporter Reporter, runMode string, registerForceClose func(func())) TaskResult {
	marketCount := len(request.Markets)
	items, _, err := loadItems(request)
	if err != nil {
		return TaskResult{Success: false, Error: err.Error(), ExitCode: 2, MarketCount: marketCount}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if request.MarketWorkers < 1 {
		request.MarketWorkers = 2
	}
	workers := min(request.MarketWorkers, marketCount)
	outputPath := expandPath(request.OutputPath)
	if outputPath == "" {
		outputPath = defaultSingleOutputPath(request, time.Now().Format("060102-1504"))
	}
	state := runtimeState{}
	if !request.FreshProfile {
		state = multiMarketStateResolver(request)
	}
	safeReport := synchronizedReporter(reporter)
	for _, warning := range state.Warnings {
		safeReport(Event{"event": "warning", "stage": "state", "message": warning})
	}
	if state.Migrated {
		safeReport(Event{"event": "state_migrated", "state_dir": state.StateDir})
	}
	safeReport(Event{
		"event":          "run_started",
		"mode":           "multi_market",
		"run_mode":       runMode,
		"market_count":   marketCount,
		"market_workers": workers,
		"url_count":      len(items),
		"output":         absPath(outputPath),
	})
	safeReport(Event{
		"event":          "multi_market_started",
		"market_count":   marketCount,
		"market_workers": workers,
		"url_count":      len(items),
		"output_path":    absPath(outputPath),
	})
	preflight(request, safeReport)

	jobs := make([]MarketJob, marketCount)
	sheetNames := marketWorksheetNames(request.Markets)
	for index, market := range request.Markets {
		jobs[index] = MarketJob{Index: index, Market: market, Inputs: append([]ScrapeItem(nil), items...), SheetName: sheetNames[index]}
	}
	results := make([]MarketJobResult, marketCount)
	semaphore := make(chan struct{}, workers)
	var group sync.WaitGroup
	for index := range jobs {
		job := jobs[index]
		group.Add(1)
		go func() {
			defer group.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				safeReport(marketJobEvent("market_job_started", MarketJobResult{Job: job}))
				results[job.Index] = cancelledMarketResult(job, ctx.Err())
				reportMarketJobFailure(safeReport, results[job.Index])
				return
			}
			if err := ctx.Err(); err != nil {
				safeReport(marketJobEvent("market_job_started", MarketJobResult{Job: job}))
				results[job.Index] = cancelledMarketResult(job, err)
				reportMarketJobFailure(safeReport, results[job.Index])
				return
			}
			results[job.Index] = runPreparedMarketJobWithForce(ctx, job, request, state, outputPath, safeReport, registerForceClose)
		}()
	}
	group.Wait()

	return finishMultiMarketRun(ctx, request, outputPath, results, safeReport)
}

func runPreparedMarketJob(ctx context.Context, job MarketJob, request TaskRequest, state runtimeState, outputPath string, reporter Reporter) MarketJobResult {
	return runPreparedMarketJobWithForce(ctx, job, request, state, outputPath, reporter, nil)
}

func runPreparedMarketJobWithForce(ctx context.Context, job MarketJob, request TaskRequest, state runtimeState, outputPath string, reporter Reporter, registerForceClose func(func())) MarketJobResult {
	runtimeDir := filepath.Join(filepath.Dir(outputPath), "_markets", fmt.Sprintf("%02d_%s", job.Index+1, safeFilename(strings.ToUpper(job.Market.Code)+"-"+strings.ToUpper(job.Market.Currency))))
	jobRequest := request
	jobRequest.Market = job.Market
	jobRequest.Markets = nil
	jobRequest.InputPaths = nil
	jobRequest.URLsFile = ""
	jobRequest.ExcelInput = ""
	jobRequest.OutputPath = filepath.Join(runtimeDir, "runtime.xlsx")
	jobRequest.BrowserOptions.SessionFile = filepath.Join(runtimeDir, "session.json")
	jobRequest.BrowserOptions.UserDataDir = filepath.Join(runtimeDir, "profile")
	applyMarketBrowserDefaults(&jobRequest)
	reporter(marketJobEvent("market_job_started", MarketJobResult{Job: job}))

	if err := prepareRuntimeSession(state.SessionFile, jobRequest.BrowserOptions.SessionFile, request.FreshProfile); err != nil {
		result := failedMarketResult(job, "session_prepare", err)
		reportMarketJobFailure(reporter, result)
		return result
	}
	if err := prepareRuntimeProfile(state.ProfileSeedDir, jobRequest.BrowserOptions.UserDataDir, request.FreshProfile); err != nil {
		result := failedMarketResult(job, "profile_copy", err)
		reportMarketJobFailure(reporter, result)
		return result
	}
	result := multiMarketJobRunner(ctx, job, jobRequest, reporter, registerForceClose)
	if result.Job.Market.Code == "" {
		result.Job = job
	}
	if result.Success && len(result.Rows) > 0 {
		reporter(marketJobEvent("market_job_done", result))
		return result
	}
	if result.Error == "" {
		result.Error = "no rows captured"
	}
	if result.FailureClass == "" {
		result.FailureClass = "no_rows"
	}
	reportMarketJobFailure(reporter, result)
	return result
}

func reportMarketJobFailure(reporter Reporter, result MarketJobResult) {
	reporter(marketJobEvent("market_job_failed", result))
}

func finishMultiMarketRun(ctx context.Context, request TaskRequest, outputPath string, results []MarketJobResult, reporter Reporter) TaskResult {
	result := TaskResult{MarketCount: len(results), MarketResults: results, LogFile: request.LogFile}
	failed := []string{}
	for _, jobResult := range results {
		if jobResult.Success && len(jobResult.Rows) > 0 {
			result.SuccessfulMarketCount++
			result.ProcessedCount += len(jobResult.Rows)
			continue
		}
		result.FailedMarketCount++
		failed = append(failed, fmt.Sprintf("%s: %s", jobResult.Job.Market.Code, jobResult.Error))
	}
	if ctx.Err() != nil {
		result.Error = "stopped by signal"
		result.ExitCode = 130
		return result
	}
	if result.SuccessfulMarketCount == 0 {
		result.Error = strings.Join(failed, "; ")
		result.ExitCode = 4
		return result
	}
	savedPath, err := multiMarketExporter(results, outputPath, request.ExtractRealPrice, request.ExtractProductInfo)
	if err != nil {
		result.Error = "export failed: " + err.Error()
		result.ExitCode = 4
		return result
	}
	result.OutputPath = savedPath
	result.OutputPaths = []string{savedPath}
	reporter(Event{"event": "multi_market_export_done", "output_path": savedPath, "market_count": result.SuccessfulMarketCount, "rows": result.ProcessedCount})
	if result.FailedMarketCount > 0 {
		result.PartialSuccess = true
		result.Error = strings.Join(failed, "; ")
		result.ExitCode = 4
		return result
	}
	result.Success = true
	result.ExitCode = 0
	return result
}

func failedMarketResult(job MarketJob, stage string, err error) MarketJobResult {
	return MarketJobResult{Job: job, FailureClass: stage, Error: err.Error()}
}

func cancelledMarketResult(job MarketJob, err error) MarketJobResult {
	message := "cancelled"
	if err != nil {
		message = err.Error()
	}
	return MarketJobResult{Job: job, FailureClass: "cancelled", Error: message}
}

func runLiveMarketJob(ctx context.Context, job MarketJob, request TaskRequest, reporter Reporter, registerForceClose func(func())) MarketJobResult {
	result := MarketJobResult{Job: job}
	marketReporter := func(event Event) {
		reporter(mergeEvent(event, marketEventFields(job.Market)))
	}
	vendor := fmt.Sprintf("market-%02d-%s", job.Index+1, strings.ToUpper(job.Market.Code))
	scraper := newTripScraper(request, marketReporter, vendor, job.Index+1, len(job.Inputs))
	if registerForceClose != nil {
		registerForceClose(scraper.close)
	}
	defer scraper.close()
	if err := scraper.start(ctx); err != nil {
		result.FailureClass = "browser_start"
		result.Error = err.Error()
		return result
	}
	rows := make([]map[string]string, 0)
	for index, item := range job.Inputs {
		if err := ctx.Err(); err != nil {
			result.FailureClass = "cancelled"
			result.Error = err.Error()
			return result
		}
		rows = append(rows, scraper.scrapeOne(ctx, item, index+1, len(job.Inputs))...)
		if index < len(job.Inputs)-1 {
			scraper.cleanupAfterURL()
			_ = scraper.maybeRestart(ctx)
		}
	}
	if !scraper.saveSession() {
		marketReporter(Event{"event": "warning", "stage": "session_save", "message": "session save failed"})
	}
	rows, _ = runQualityReruns(ctx, rows, request, vendor, request.OutputPath, len(job.Inputs), marketReporter, nil)
	if err := ctx.Err(); err != nil {
		result.FailureClass = "cancelled"
		result.Error = err.Error()
		return result
	}
	if _, err := validateSinglePriceScrapeRows(rows); err != nil {
		result.FailureClass = "quality"
		result.Error = err.Error()
		return result
	}
	if len(rows) == 0 {
		result.FailureClass = "no_rows"
		result.Error = "no rows captured"
		return result
	}
	result.Success = true
	result.Rows = rows
	return result
}

func multiMarketResultFields(results []MarketJobResult) []Event {
	fields := make([]Event, len(results))
	for index, result := range results {
		fields[index] = marketJobPublicFields(result)
	}
	return fields
}

func marketJobEvent(name string, result MarketJobResult) Event {
	event := marketJobPublicFields(result)
	event["event"] = name
	return event
}

func marketJobPublicFields(result MarketJobResult) Event {
	trusted, untrusted, _ := countPriceQualityRows(result.Rows)
	fields := mergeEvent(Event{
		"sheet_name":     result.Job.SheetName,
		"rows":           len(result.Rows),
		"trusted_rows":   trusted,
		"untrusted_rows": untrusted,
		"success":        result.Success,
		"failure_class":  result.FailureClass,
	}, marketEventFields(result.Job.Market))
	return fields
}

func synchronizedReporter(reporter Reporter) Reporter {
	if reporter == nil {
		return func(Event) {}
	}
	var mu sync.Mutex
	return func(event Event) {
		copy := Event{}
		for key, value := range event {
			copy[key] = value
		}
		mu.Lock()
		defer mu.Unlock()
		reporter(copy)
	}
}
