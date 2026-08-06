package tripcdp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestScrapeRoutesMultiMarketRequestsBeforeAnyBrowserWork(t *testing.T) {
	result := scrape(TaskRequest{
		Markets: []MarketContext{{Code: "hk"}, {Code: "jp"}},
	}, func(Event) {}, "")

	if result.ExitCode != 2 {
		t.Fatalf("exit code = %d, want input validation exit 2", result.ExitCode)
	}
	if result.MarketCount != 2 {
		t.Fatalf("market count = %d, want 2; multi-market request was not routed", result.MarketCount)
	}
}

func TestMultiMarketRunsEveryJobAndExportsOnlySuccessfulMarketsInRequestOrder(t *testing.T) {
	oldRunner := multiMarketJobRunner
	oldExporter := multiMarketExporter
	t.Cleanup(func() {
		multiMarketJobRunner = oldRunner
		multiMarketExporter = oldExporter
	})

	var mu sync.Mutex
	started := []string{}
	multiMarketJobRunner = func(_ context.Context, job MarketJob, request TaskRequest, _ Reporter, _ func(func())) MarketJobResult {
		mu.Lock()
		started = append(started, job.Market.Code)
		mu.Unlock()
		if request.Market.Code != job.Market.Code || len(request.Markets) != 0 {
			return MarketJobResult{Job: job, Error: "market request leaked into another job"}
		}
		if job.Market.Code == "jp" {
			return MarketJobResult{Job: job, FailureClass: "browser_start", Error: "JP unavailable"}
		}
		return MarketJobResult{Job: job, Success: true, Rows: []map[string]string{{"商品ID": job.Market.Code, "货币": job.Market.Currency, "外显价格": "10"}}}
	}
	var exported []MarketJobResult
	multiMarketExporter = func(results []MarketJobResult, path string, realPrice, productInfo bool) (string, error) {
		exported = append([]MarketJobResult(nil), results...)
		return path, nil
	}

	output := filepath.Join(t.TempDir(), "markets.xlsx")
	events := collectEvents()
	result := scrapeMultiMarketsContext(context.Background(), TaskRequest{
		Markets:       []MarketContext{{Code: "hk", Currency: "HKD"}, {Code: "jp", Currency: "JPY"}, {Code: "sg", Currency: "SGD"}},
		MarketWorkers: 2,
		FreshProfile:  true,
		InputPaths:    []string{"https://trip.com/things-to-do/detail/1/"},
		OutputPath:    output,
	}, events.report, "")

	if result.Success || !result.PartialSuccess || result.ExitCode != 4 {
		t.Fatalf("result = %+v, want partial exit 4", result)
	}
	if result.SuccessfulMarketCount != 2 || result.FailedMarketCount != 1 || result.ProcessedCount != 2 {
		t.Fatalf("counts = success:%d failed:%d rows:%d", result.SuccessfulMarketCount, result.FailedMarketCount, result.ProcessedCount)
	}
	if len(result.MarketResults) != 3 || result.MarketResults[0].Job.Market.Code != "hk" || result.MarketResults[1].Job.Market.Code != "jp" || result.MarketResults[2].Job.Market.Code != "sg" {
		t.Fatalf("request order was not preserved: %+v", result.MarketResults)
	}
	if len(started) != 3 || !containsAll(started, "hk", "jp", "sg") {
		t.Fatalf("a market failure cancelled a peer: started=%v", started)
	}
	if len(exported) != 3 || !exported[0].Success || exported[1].Success || !exported[2].Success {
		t.Fatalf("exported results = %+v", exported)
	}
	for _, name := range []string{"multi_market_started", "market_job_started", "market_job_done", "market_job_failed", "multi_market_export_done"} {
		if !events.has(name) {
			t.Fatalf("missing %s event: %#v", name, events.all())
		}
	}
}

func TestMultiMarketAllFailuresDoNotWriteWorkbook(t *testing.T) {
	oldRunner := multiMarketJobRunner
	oldExporter := multiMarketExporter
	t.Cleanup(func() {
		multiMarketJobRunner = oldRunner
		multiMarketExporter = oldExporter
	})
	multiMarketJobRunner = func(_ context.Context, job MarketJob, _ TaskRequest, _ Reporter, _ func(func())) MarketJobResult {
		return MarketJobResult{Job: job, Error: "blocked"}
	}
	exporterCalled := false
	multiMarketExporter = func(_ []MarketJobResult, _ string, _ bool, _ bool) (string, error) {
		exporterCalled = true
		return "", errors.New("must not export")
	}

	result := scrapeMultiMarketsContext(context.Background(), TaskRequest{
		Markets:       []MarketContext{{Code: "hk", Currency: "HKD"}, {Code: "jp", Currency: "JPY"}},
		MarketWorkers: 2,
		FreshProfile:  true,
		InputPaths:    []string{"https://trip.com/things-to-do/detail/1/"},
		OutputPath:    filepath.Join(t.TempDir(), "markets.xlsx"),
	}, func(Event) {}, "")

	if result.Success || result.PartialSuccess || result.ExitCode != 4 || result.OutputPath != "" {
		t.Fatalf("result = %+v, want all-failed no-output exit 4", result)
	}
	if exporterCalled {
		t.Fatal("all-failed run called exporter")
	}
}

func TestMultiMarketHonorsWorkerLimitWithoutReorderingResults(t *testing.T) {
	oldRunner := multiMarketJobRunner
	oldExporter := multiMarketExporter
	t.Cleanup(func() {
		multiMarketJobRunner = oldRunner
		multiMarketExporter = oldExporter
	})
	started := make(chan string, 3)
	release := make(chan struct{})
	var active, peak atomic.Int32
	multiMarketJobRunner = func(_ context.Context, job MarketJob, _ TaskRequest, _ Reporter, _ func(func())) MarketJobResult {
		current := active.Add(1)
		for {
			observed := peak.Load()
			if current <= observed || peak.CompareAndSwap(observed, current) {
				break
			}
		}
		started <- job.Market.Code
		<-release
		active.Add(-1)
		return MarketJobResult{Job: job, Success: true, Rows: []map[string]string{{"商品ID": job.Market.Code}}}
	}
	multiMarketExporter = func(_ []MarketJobResult, path string, _ bool, _ bool) (string, error) { return path, nil }

	resultCh := make(chan TaskResult, 1)
	go func() {
		resultCh <- scrapeMultiMarketsContext(context.Background(), multiMarketTestRequest(t, 2, 3), func(Event) {}, "")
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("two permitted market jobs did not start")
		}
	}
	select {
	case third := <-started:
		t.Fatalf("market %q started before a worker slot was released", third)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	result := <-resultCh
	if peak.Load() != 2 || !result.Success || result.ExitCode != 0 {
		t.Fatalf("peak=%d result=%+v, want worker cap 2 and complete success", peak.Load(), result)
	}
	for index, code := range []string{"hk", "jp", "sg"} {
		if result.MarketResults[index].Job.Market.Code != code {
			t.Fatalf("result order = %+v", result.MarketResults)
		}
	}
}

func TestMultiMarketParentCancellationIsBoundedAndSkipsFinalExport(t *testing.T) {
	oldRunner := multiMarketJobRunner
	oldExporter := multiMarketExporter
	t.Cleanup(func() {
		multiMarketJobRunner = oldRunner
		multiMarketExporter = oldExporter
	})
	started := make(chan struct{}, 1)
	multiMarketJobRunner = func(ctx context.Context, job MarketJob, _ TaskRequest, _ Reporter, _ func(func())) MarketJobResult {
		started <- struct{}{}
		<-ctx.Done()
		return cancelledMarketResult(job, ctx.Err())
	}
	exporterCalled := false
	multiMarketExporter = func(_ []MarketJobResult, _ string, _ bool, _ bool) (string, error) {
		exporterCalled = true
		return "", nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan TaskResult, 1)
	go func() {
		resultCh <- scrapeMultiMarketsContext(ctx, multiMarketTestRequest(t, 1, 1), func(Event) {}, "")
	}()
	select {
	case <-started:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("market job did not start")
	}
	select {
	case result := <-resultCh:
		if result.ExitCode != 130 || result.Success || exporterCalled {
			t.Fatalf("result=%+v exporterCalled=%v, want cancelled no-export exit 130", result, exporterCalled)
		}
	case <-time.After(time.Second):
		t.Fatal("parent cancellation did not finish within bound")
	}
}

func TestPreparedMarketJobsUseIndependentRuntimeCopiesWithoutMutatingSeed(t *testing.T) {
	oldRunner := multiMarketJobRunner
	t.Cleanup(func() { multiMarketJobRunner = oldRunner })
	root := t.TempDir()
	seedSession := filepath.Join(root, "seed", "session.json")
	seedProfile := filepath.Join(root, "seed", "profile")
	if err := writeEmptySession(seedSession); err != nil {
		t.Fatal(err)
	}
	if err := createManagedChromeProfile(seedProfile); err != nil {
		t.Fatal(err)
	}
	seedBefore, err := os.ReadFile(seedSession)
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{}
	multiMarketJobRunner = func(_ context.Context, job MarketJob, request TaskRequest, _ Reporter, _ func(func())) MarketJobResult {
		paths = append(paths, request.BrowserOptions.SessionFile, request.BrowserOptions.UserDataDir)
		if err := os.WriteFile(request.BrowserOptions.SessionFile, []byte("job-owned-session"), 0o644); err != nil {
			return MarketJobResult{Job: job, Error: err.Error()}
		}
		return MarketJobResult{Job: job, Success: true, Rows: []map[string]string{{"商品ID": job.Market.Code}}}
	}
	request := TaskRequest{FreshProfile: false, OutputPath: filepath.Join(root, "markets.xlsx")}
	state := runtimeState{SessionFile: seedSession, ProfileSeedDir: seedProfile}
	first := runPreparedMarketJob(context.Background(), MarketJob{Index: 0, Market: MarketContext{Code: "hk", Currency: "HKD"}}, request, state, request.OutputPath, func(Event) {})
	second := runPreparedMarketJob(context.Background(), MarketJob{Index: 1, Market: MarketContext{Code: "jp", Currency: "JPY"}}, request, state, request.OutputPath, func(Event) {})
	if !first.Success || !second.Success || len(paths) != 4 || paths[0] == paths[2] || paths[1] == paths[3] {
		t.Fatalf("market runtime isolation = first:%+v second:%+v paths=%v", first, second, paths)
	}
	seedAfter, err := os.ReadFile(seedSession)
	if err != nil {
		t.Fatal(err)
	}
	if string(seedAfter) != string(seedBefore) {
		t.Fatal("job session mutation was consolidated into the shared seed")
	}
}

func TestMarketPreparationFailureEmitsStartedAndFailedEvents(t *testing.T) {
	job := MarketJob{Index: 0, Market: MarketContext{Code: "hk", Currency: "HKD"}}
	events := collectEvents()
	result := runPreparedMarketJob(context.Background(), job, TaskRequest{
		FreshProfile: false,
		OutputPath:   filepath.Join(t.TempDir(), "markets.xlsx"),
	}, runtimeState{
		SessionFile:    filepath.Join(t.TempDir(), "missing-session.json"),
		ProfileSeedDir: filepath.Join(t.TempDir(), "missing-profile"),
	}, filepath.Join(t.TempDir(), "markets.xlsx"), events.report)
	if result.Success || result.FailureClass != "session_prepare" {
		t.Fatalf("result = %+v, want session preparation failure", result)
	}
	if !events.has("market_job_started") || !events.has("market_job_failed") {
		t.Fatalf("preparation events = %#v, want market_job_started then market_job_failed", events.all())
	}
}

func TestRunDoneEventIncludesOnlyAllowlistedMarketResultFields(t *testing.T) {
	event := runDoneEvent(TaskRequest{}, TaskResult{
		MarketCount:           2,
		SuccessfulMarketCount: 1,
		FailedMarketCount:     1,
		PartialSuccess:        true,
		MarketResults: []MarketJobResult{{
			Job:          MarketJob{Index: 0, Market: MarketContext{Code: "hk", Locale: "zh-HK", Currency: "HKD"}},
			Success:      true,
			Rows:         []map[string]string{{"商品ID": "1"}},
			FailureClass: "",
		}},
	})
	if event["market_count"] != 2 || event["successful_market_count"] != 1 || event["failed_market_count"] != 1 || event["partial_success"] != true {
		t.Fatalf("run_done counts=%#v", event)
	}
	results, ok := event["market_results"].([]Event)
	if !ok || len(results) != 1 || results[0]["market_code"] != "hk" || results[0]["rows"] != 1 {
		t.Fatalf("run_done market results=%#v", event["market_results"])
	}
	wantFields := map[string]bool{
		"market_code": true, "market_site_id": true, "market_host": true, "market_locale": true, "market_currency": true,
		"sheet_name": true, "rows": true, "trusted_rows": true, "untrusted_rows": true, "success": true, "failure_class": true,
	}
	if len(results[0]) != len(wantFields) {
		t.Fatalf("run_done result fields=%#v, want exactly %#v", results[0], wantFields)
	}
	for field := range results[0] {
		if !wantFields[field] {
			t.Fatalf("run_done leaked non-allowlisted %s: %#v", field, results[0])
		}
	}
}

func TestMultiMarketPrecomputesSemanticSheetNamesForEveryJobAndResult(t *testing.T) {
	oldRunner := multiMarketJobRunner
	oldExporter := multiMarketExporter
	t.Cleanup(func() {
		multiMarketJobRunner = oldRunner
		multiMarketExporter = oldExporter
	})
	want := []string{"SG-MYR-zh-SG", "SG-MYR-en-SG"}
	multiMarketJobRunner = func(_ context.Context, job MarketJob, _ TaskRequest, _ Reporter, _ func(func())) MarketJobResult {
		if job.SheetName != want[job.Index] {
			return MarketJobResult{Job: job, Error: "missing precomputed sheet name"}
		}
		return MarketJobResult{Job: job, Success: true, Rows: []map[string]string{{"商品ID": job.Market.Locale}}}
	}
	var exported []MarketJobResult
	multiMarketExporter = func(results []MarketJobResult, path string, _ bool, _ bool) (string, error) {
		exported = append([]MarketJobResult(nil), results...)
		return path, nil
	}
	result := scrapeMultiMarketsContext(context.Background(), TaskRequest{
		Markets:       []MarketContext{{Code: "sg", SiteID: "SG", Host: "sg.trip.com", Locale: "zh-SG", Currency: "MYR"}, {Code: "sg", SiteID: "SG", Host: "sg.trip.com", Locale: "en-SG", Currency: "MYR"}},
		MarketWorkers: 2,
		FreshProfile:  true,
		InputPaths:    []string{"https://trip.com/things-to-do/detail/1/"},
		OutputPath:    filepath.Join(t.TempDir(), "markets.xlsx"),
	}, func(Event) {}, "")
	if !result.Success || len(exported) != 2 {
		t.Fatalf("result=%+v exported=%+v", result, exported)
	}
	for index, name := range want {
		if result.MarketResults[index].Job.SheetName != name || exported[index].Job.SheetName != name {
			t.Fatalf("sheet names results=%+v exported=%+v", result.MarketResults, exported)
		}
	}
}

func TestFreshMultiMarketSkipsGlobalStateResolution(t *testing.T) {
	oldRunner := multiMarketJobRunner
	oldExporter := multiMarketExporter
	oldResolver := multiMarketStateResolver
	t.Cleanup(func() {
		multiMarketJobRunner = oldRunner
		multiMarketExporter = oldExporter
		multiMarketStateResolver = oldResolver
	})
	resolverCalls := 0
	multiMarketStateResolver = func(TaskRequest) runtimeState {
		resolverCalls++
		return runtimeState{}
	}
	multiMarketJobRunner = func(_ context.Context, job MarketJob, _ TaskRequest, _ Reporter, _ func(func())) MarketJobResult {
		return MarketJobResult{Job: job, Success: true, Rows: []map[string]string{{"商品ID": "1"}}}
	}
	multiMarketExporter = func(_ []MarketJobResult, path string, _ bool, _ bool) (string, error) { return path, nil }
	result := scrapeMultiMarketsContext(context.Background(), multiMarketTestRequest(t, 1, 1), func(Event) {}, "")
	if !result.Success || resolverCalls != 0 {
		t.Fatalf("result=%+v resolverCalls=%d, fresh run must not resolve global state", result, resolverCalls)
	}
}

func TestNonFreshMultiResolvesOneReadOnlySeedForAllJobs(t *testing.T) {
	oldRunner := multiMarketJobRunner
	oldExporter := multiMarketExporter
	oldResolver := multiMarketStateResolver
	t.Cleanup(func() {
		multiMarketJobRunner = oldRunner
		multiMarketExporter = oldExporter
		multiMarketStateResolver = oldResolver
	})
	root := t.TempDir()
	seedSession := filepath.Join(root, "seed", "session.json")
	seedProfile := filepath.Join(root, "seed", "profile")
	if err := writeEmptySession(seedSession); err != nil {
		t.Fatal(err)
	}
	if err := createManagedChromeProfile(seedProfile); err != nil {
		t.Fatal(err)
	}
	resolverCalls := 0
	multiMarketStateResolver = func(TaskRequest) runtimeState {
		resolverCalls++
		return runtimeState{SessionFile: seedSession, ProfileSeedDir: seedProfile}
	}
	var sessionsMu sync.Mutex
	runtimeSessions := []string{}
	multiMarketJobRunner = func(_ context.Context, job MarketJob, request TaskRequest, _ Reporter, _ func(func())) MarketJobResult {
		sessionsMu.Lock()
		runtimeSessions = append(runtimeSessions, request.BrowserOptions.SessionFile)
		sessionsMu.Unlock()
		return MarketJobResult{Job: job, Success: true, Rows: []map[string]string{{"商品ID": job.Market.Code}}}
	}
	multiMarketExporter = func(_ []MarketJobResult, path string, _ bool, _ bool) (string, error) { return path, nil }
	result := scrapeMultiMarketsContext(context.Background(), TaskRequest{
		Markets:       []MarketContext{{Code: "hk", Currency: "HKD"}, {Code: "jp", Currency: "JPY"}},
		MarketWorkers: 2,
		InputPaths:    []string{"https://trip.com/things-to-do/detail/1/"},
		OutputPath:    filepath.Join(root, "markets.xlsx"),
	}, func(Event) {}, "")
	sessionsMu.Lock()
	gotSessions := append([]string(nil), runtimeSessions...)
	sessionsMu.Unlock()
	if !result.Success || resolverCalls != 1 || len(gotSessions) != 2 || gotSessions[0] == seedSession || gotSessions[1] == seedSession || gotSessions[0] == gotSessions[1] {
		t.Fatalf("result=%+v resolverCalls=%d runtimeSessions=%v", result, resolverCalls, gotSessions)
	}
}

func TestMultiMarketStartsAndClosesOneAwakeGuard(t *testing.T) {
	oldRunner := multiMarketJobRunner
	oldExporter := multiMarketExporter
	oldGuard := multiMarketPowerGuardStarter
	t.Cleanup(func() {
		multiMarketJobRunner = oldRunner
		multiMarketExporter = oldExporter
		multiMarketPowerGuardStarter = oldGuard
	})
	guard := &countingPowerGuard{}
	starts := 0
	multiMarketPowerGuardStarter = func(Reporter) powerGuard {
		starts++
		return guard
	}
	multiMarketJobRunner = func(_ context.Context, job MarketJob, _ TaskRequest, _ Reporter, _ func(func())) MarketJobResult {
		return MarketJobResult{Job: job, Success: true, Rows: []map[string]string{{"商品ID": "1"}}}
	}
	multiMarketExporter = func(_ []MarketJobResult, path string, _ bool, _ bool) (string, error) { return path, nil }
	result := scrapeMultiMarkets(multiMarketTestRequest(t, 1, 1), func(Event) {}, "")
	if !result.Success || starts != 1 || guard.closes != 1 {
		t.Fatalf("result=%+v starts=%d closes=%d", result, starts, guard.closes)
	}
}

func TestMultiMarketPassesForceCloseRegistrationToActiveJob(t *testing.T) {
	oldRunner := multiMarketJobRunner
	t.Cleanup(func() { multiMarketJobRunner = oldRunner })
	closerCalls := 0
	multiMarketJobRunner = func(_ context.Context, job MarketJob, _ TaskRequest, _ Reporter, register func(func())) MarketJobResult {
		register(func() { closerCalls++ })
		return MarketJobResult{Job: job, Success: true, Rows: []map[string]string{{"商品ID": "1"}}}
	}
	stopper := &forceStopper{}
	result := runPreparedMarketJobWithForce(context.Background(), MarketJob{Index: 0, Market: MarketContext{Code: "hk", Currency: "HKD"}}, TaskRequest{
		FreshProfile: true,
		OutputPath:   filepath.Join(t.TempDir(), "markets.xlsx"),
	}, runtimeState{}, filepath.Join(t.TempDir(), "markets.xlsx"), func(Event) {}, stopper.add)
	if !result.Success {
		t.Fatalf("result=%+v", result)
	}
	stopper.run()
	if closerCalls != 1 {
		t.Fatalf("registered closer calls=%d, want 1", closerCalls)
	}
}

func TestCancelledJobsBeforeSemaphoreHaveCompleteEventLifecycle(t *testing.T) {
	oldRunner := multiMarketJobRunner
	t.Cleanup(func() { multiMarketJobRunner = oldRunner })
	multiMarketJobRunner = func(_ context.Context, job MarketJob, _ TaskRequest, _ Reporter, _ func(func())) MarketJobResult {
		t.Fatalf("cancelled job %s reached runner", job.Market.Code)
		return MarketJobResult{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	events := collectEvents()
	result := scrapeMultiMarketsContext(ctx, multiMarketTestRequest(t, 1, 2), events.report, "")
	if result.ExitCode != 130 {
		t.Fatalf("result=%+v", result)
	}
	for _, code := range []string{"hk", "jp"} {
		if !events.hasForMarket("market_job_started", code) || !events.hasForMarket("market_job_failed", code) {
			t.Fatalf("cancelled %s event lifecycle incomplete: %#v", code, events.all())
		}
	}
}

func TestCancelledJobsSerializeReporterCallbacks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var active atomic.Int32
	var overlapped atomic.Bool
	reporter := func(Event) {
		if active.Add(1) > 1 {
			overlapped.Store(true)
		}
		time.Sleep(5 * time.Millisecond)
		active.Add(-1)
	}
	request := multiMarketTestRequest(t, 1, 3)
	result := scrapeMultiMarketsContext(ctx, request, reporter, "")
	if result.ExitCode != 130 || overlapped.Load() {
		t.Fatalf("result=%+v overlapped=%v, cancelled callbacks must be serialized", result, overlapped.Load())
	}
}

func TestMultiMarketRunDoneDoesNotMergeZeroLegacyMarket(t *testing.T) {
	event := runDoneEvent(TaskRequest{}, TaskResult{MarketCount: 1, MarketResults: []MarketJobResult{{Job: MarketJob{Market: MarketContext{Code: "hk"}}}}})
	for _, field := range []string{"market_code", "market_site_id", "market_host", "market_locale", "market_currency"} {
		if _, found := event[field]; found {
			t.Fatalf("multi-market run_done merged zero legacy field %s: %#v", field, event)
		}
	}
}

func TestMarketJobEventUsesExactPublicAllowlistAndQualityCounts(t *testing.T) {
	event := marketJobEvent("market_job_done", MarketJobResult{
		Job:     MarketJob{Market: MarketContext{Code: "hk", SiteID: "HK", Host: "hk.trip.com", Locale: "zh-HK", Currency: "HKD"}, SheetName: "HK-HKD"},
		Success: true,
		Rows: []map[string]string{
			{"价格状态": priceStatusTrustedBase},
			{"价格状态": priceStatusUntrustedPriceMissing},
		},
	})
	wantFields := map[string]bool{
		"event": true, "market_code": true, "market_site_id": true, "market_host": true, "market_locale": true, "market_currency": true,
		"sheet_name": true, "rows": true, "trusted_rows": true, "untrusted_rows": true, "success": true, "failure_class": true,
	}
	if len(event) != len(wantFields) || event["trusted_rows"] != 1 || event["untrusted_rows"] != 1 {
		t.Fatalf("event=%#v, want exact allowlist and 1/1 quality counts", event)
	}
	for field := range event {
		if !wantFields[field] {
			t.Fatalf("unexpected public job field %s: %#v", field, event)
		}
	}
}

func multiMarketTestRequest(t *testing.T, workers int, count int) TaskRequest {
	t.Helper()
	markets := []MarketContext{{Code: "hk", Currency: "HKD"}, {Code: "jp", Currency: "JPY"}, {Code: "sg", Currency: "SGD"}}
	return TaskRequest{
		Markets:       markets[:count],
		MarketWorkers: workers,
		FreshProfile:  true,
		InputPaths:    []string{"https://trip.com/things-to-do/detail/1/"},
		OutputPath:    filepath.Join(t.TempDir(), "markets.xlsx"),
	}
}

type eventCollector struct {
	mu     sync.Mutex
	events []Event
}

type countingPowerGuard struct{ closes int }

func (g *countingPowerGuard) close() { g.closes++ }

func collectEvents() *eventCollector { return &eventCollector{} }

func (c *eventCollector) report(event Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	copy := Event{}
	for key, value := range event {
		copy[key] = value
	}
	c.events = append(c.events, copy)
}

func (c *eventCollector) has(name string) bool {
	for _, event := range c.all() {
		if event["event"] == name {
			return true
		}
	}
	return false
}

func (c *eventCollector) hasForMarket(name, marketCode string) bool {
	for _, event := range c.all() {
		if event["event"] == name && event["market_code"] == marketCode {
			return true
		}
	}
	return false
}

func (c *eventCollector) all() []Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Event(nil), c.events...)
}

func containsAll(values []string, want ...string) bool {
	seen := map[string]bool{}
	for _, value := range values {
		seen[value] = true
	}
	for _, value := range want {
		if !seen[value] {
			return false
		}
	}
	return true
}
