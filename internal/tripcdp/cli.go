package tripcdp

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

type stringList []string

func (s *stringList) String() string {
	return fmt.Sprint([]string(*s))
}

func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

type triState struct {
	set   bool
	value bool
}

func Run(argv []string) int {
	request, runMode, logLevel, jsonLines, noColor, err := parseArgs(argv, os.Stderr)
	if err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		fmt.Fprintf(os.Stderr, "argument error: %v\n", err)
		return 2
	}
	_ = noColor
	renderer := newRenderer(request.LogFile, logLevel, jsonLines)
	result := scrape(request, renderer.emit, runMode)
	renderer.emit(runDoneEvent(request, result))
	return result.ExitCode
}

func runDoneEvent(request TaskRequest, result TaskResult) Event {
	event := Event{
		"event":                        "run_done",
		"success":                      result.Success,
		"exit_code":                    result.ExitCode,
		"processed_count":              result.ProcessedCount,
		"output_path":                  absPath(result.OutputPath),
		"output_paths":                 absPaths(result.OutputPaths),
		"failed_vendors":               result.FailedVendors,
		"log_file":                     absPath(result.LogFile),
		"error":                        result.Error,
		"quality_failed_urls":          result.QualityFailedURLs,
		"quality_recovered_urls":       result.QualityRecoveredURLs,
		"quality_rerun_attempted_urls": result.QualityRerunAttemptedURLs,
		"quality_rerun_recovered_urls": result.QualityRerunRecoveredURLs,
		"final_quality_failed_urls":    result.FinalQualityFailedURLs,
	}
	if request.Market.Code != "" {
		event = mergeEvent(event, marketEventFields(request.Market))
	}
	if result.MarketCount > 0 {
		event["market_count"] = result.MarketCount
		event["successful_market_count"] = result.SuccessfulMarketCount
		event["failed_market_count"] = result.FailedMarketCount
		event["partial_success"] = result.PartialSuccess
		event["market_results"] = multiMarketResultFields(result.MarketResults)
	}
	return event
}

func parseArgs(argv []string, stderr io.Writer) (TaskRequest, string, string, bool, bool, error) {
	var inputs stringList
	var marketSpecs stringList
	var headless triState
	var ratingOnly triState
	var realPrice triState
	var productInfo triState
	var startupStagger float64
	var stopGrace int
	var timeoutSeconds int
	var memoryLimit int
	var restartEvery int
	var disableMemoryGuard bool
	var runMode string
	var browser string
	var site string
	var locale string
	var currency string
	var freshProfile bool
	var urlsFile string
	var excelInput string
	var output string
	var outputDir string
	var workers int
	var marketWorkers int
	var sessionFile string
	var logFile string
	var logLevel string
	var jsonLines bool
	var noColor bool
	var delayProfile string
	var pageDelayMin float64 = -1
	var pageDelayMax float64 = -1
	var scrollDelayMin float64 = -1
	var scrollDelayMax float64 = -1
	var skuClickDelayMin float64 = -1
	var skuClickDelayMax float64 = -1
	var skuPriceDelayMin float64 = -1
	var skuPriceDelayMax float64 = -1
	var skuBetweenDelayMin float64 = -1
	var skuBetweenDelayMax float64 = -1
	var priceQualityRetries int = -1
	var noQualityRerun bool
	var qualityRerunHeadless triState
	var qualityRerunDelayProfile string
	runtimeOptions := defaultRuntimeOptions()
	urlTotalTimeoutSeconds := int(runtimeOptions.URLTotalTimeout / time.Second)
	navigationTimeoutSeconds := int(runtimeOptions.NavigationTimeout / time.Second)
	workerStallTimeoutSeconds := int(runtimeOptions.WorkerStallTimeout / time.Second)
	fastURLThresholdSeconds := int(runtimeOptions.FastURLThreshold / time.Second)

	var compareVendor string

	fs := flag.NewFlagSet("trip-score-cdp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "usage: trip-score-cdp [options]")
		fmt.Fprintln(fs.Output(), "")
		fmt.Fprintln(fs.Output(), "Scrape Trip score and price data from one or more product URLs using Go + CDP.")
		fmt.Fprintln(fs.Output(), "")
		fs.PrintDefaults()
	}
	fs.Var(&inputs, "input", "Product URL. Repeat for multiple URLs.")
	fs.Var(&marketSpecs, "market", "Market context site[:locale[:currency]]. Repeat for multi-market scraping.")
	fs.IntVar(&marketWorkers, "market-workers", 2, "Concurrent market jobs, 1-3. Defaults to 2.")
	fs.StringVar(&urlsFile, "urls-file", "", "Text file containing one product URL per line.")
	fs.StringVar(&excelInput, "excel-input", "", "Excel workbook. First row contains vendors; links start from row 2.")
	fs.StringVar(&output, "output", "", "Excel output path.")
	fs.StringVar(&outputDir, "output-dir", "", "Directory for automatically named Excel outputs.")
	fs.IntVar(&workers, "workers", 0, "Concurrent browser workers, 1-5. Defaults to min(vendor count, 5).")
	fs.Float64Var(&startupStagger, "startup-stagger", 0.5, "Seconds to wait between starting vendor browser workers. Use 0 to disable staggering.")
	fs.IntVar(&stopGrace, "stop-grace-seconds", 30, "Seconds to wait for partial exports after SIGINT/SIGTERM before terminating workers.")
	fs.StringVar(&runMode, "run-mode", "", "Preset run mode: mode1=headless full SKU/price/rating scrape; mode2=headless rating-only + product-info scrape (no SKU, no price columns).")
	fs.StringVar(&browser, "browser", "chrome", "Browser to use. Only chrome is supported.")
	fs.StringVar(&site, "site", "hk", "Trip subsite to scrape.")
	fs.StringVar(&locale, "locale", "", "Trip Things-to-Do locale override (advertised locale).")
	fs.StringVar(&currency, "currency", "", "Trip ISO currency override.")
	fs.BoolVar(&freshProfile, "fresh-profile", false, "Start with an empty temporary Chrome profile instead of copying saved browser state.")
	fs.BoolFunc("headless", "Run Chrome in headless mode.", func(string) error { headless = triState{set: true, value: true}; return nil })
	fs.BoolFunc("no-headless", "Run Chrome with a visible browser window.", func(string) error { headless = triState{set: true, value: false}; return nil })
	fs.StringVar(&sessionFile, "session-file", "", "Optional session file path.")
	fs.StringVar(&logFile, "log-file", "", "Optional log file path.")
	fs.StringVar(&logLevel, "log-level", "normal", "Terminal log verbosity: quiet, normal, or verbose.")
	fs.BoolVar(&jsonLines, "json-lines", false, "Emit machine-readable JSON events, one event per line.")
	fs.BoolVar(&noColor, "no-color", false, "Disable ANSI color in terminal output.")
	fs.IntVar(&timeoutSeconds, "timeout", 300, "Browser startup timeout seconds.")
	fs.IntVar(&memoryLimit, "browser-memory-limit-mb", 8192, "Restart Chrome after managed browser RSS exceeds this many MB.")
	fs.IntVar(&restartEvery, "browser-restart-every", 20, "Restart Chrome after this many processed URLs.")
	fs.BoolVar(&disableMemoryGuard, "disable-browser-memory-guard", false, "Disable Chrome memory guard and automatic browser restarts.")
	fs.StringVar(&delayProfile, "delay-profile", "normal", "Delay preset: fast, normal, safe, or slow.")
	fs.Float64Var(&pageDelayMin, "page-delay-min", -1, "Minimum seconds to wait after page load before scrolling. Default -1 uses delay profile default.")
	fs.Float64Var(&pageDelayMax, "page-delay-max", -1, "Maximum seconds to wait after page load before scrolling. Default -1 uses delay profile default.")
	fs.Float64Var(&scrollDelayMin, "scroll-delay-min", -1, "Minimum seconds to wait between lazy-load scrolls. Default -1 uses delay profile default.")
	fs.Float64Var(&scrollDelayMax, "scroll-delay-max", -1, "Maximum seconds to wait between lazy-load scrolls. Default -1 uses delay profile default.")
	fs.Float64Var(&skuClickDelayMin, "sku-click-delay-min", -1, "Minimum seconds to wait after a SKU option click. Default -1 uses delay profile default.")
	fs.Float64Var(&skuClickDelayMax, "sku-click-delay-max", -1, "Maximum seconds to wait after a SKU option click. Default -1 uses delay profile default.")
	fs.Float64Var(&skuPriceDelayMin, "sku-price-delay-min", -1, "Minimum seconds to wait before reading a SKU price bundle. Default -1 uses delay profile default.")
	fs.Float64Var(&skuPriceDelayMax, "sku-price-delay-max", -1, "Maximum seconds to wait before reading a SKU price bundle. Default -1 uses delay profile default.")
	fs.Float64Var(&skuBetweenDelayMin, "sku-between-delay-min", -1, "Minimum seconds to wait between SKU combinations. Default -1 uses delay profile default.")
	fs.Float64Var(&skuBetweenDelayMax, "sku-between-delay-max", -1, "Maximum seconds to wait between SKU combinations. Default -1 uses delay profile default.")
	fs.IntVar(&priceQualityRetries, "price-quality-retries", -1, "Retry a URL this many times when SKU price quality fails. Default -1 uses mode defaults.")
	fs.BoolVar(&noQualityRerun, "no-quality-rerun", false, "Disable post-scrape low-quality URL reruns.")
	fs.BoolFunc("quality-rerun-headless", "Run low-quality URL reruns in headless mode.", func(string) error { qualityRerunHeadless = triState{set: true, value: true}; return nil })
	fs.BoolFunc("quality-rerun-no-headless", "Run low-quality URL reruns with a visible browser window.", func(string) error { qualityRerunHeadless = triState{set: true, value: false}; return nil })
	fs.StringVar(&qualityRerunDelayProfile, "quality-rerun-delay-profile", "safe", "Delay preset for post-scrape low-quality URL reruns: fast, normal, safe, or slow.")
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
	fs.BoolFunc("rating-only", "Only scrape rating-related fields.", func(string) error { ratingOnly = triState{set: true, value: true}; return nil })
	fs.BoolFunc("no-rating-only", "Scrape SKU and price fields as well as rating fields.", func(string) error { ratingOnly = triState{set: true, value: false}; return nil })
	fs.BoolFunc("real-price", "Scrape dynamic real-price fields.", func(string) error { realPrice = triState{set: true, value: true}; return nil })
	fs.BoolFunc("no-real-price", "Skip dynamic real-price extraction.", func(string) error { realPrice = triState{set: true, value: false}; return nil })
	fs.BoolFunc("product-info", "Scrape product information fields.", func(string) error { productInfo = triState{set: true, value: true}; return nil })
	fs.BoolFunc("no-product-info", "Skip product info extraction.", func(string) error { productInfo = triState{set: true, value: false}; return nil })
	fs.StringVar(&compareVendor, "compare-vendor", "", "After scraping, compare all vendors against this base vendor using row-based region mapping from --excel-input.")

	if err := fs.Parse(argv); err != nil {
		return TaskRequest{}, "", "", false, false, err
	}
	if browser != "chrome" {
		return TaskRequest{}, "", "", false, false, fmt.Errorf("--browser must be chrome")
	}
	if marketWorkers < 1 || marketWorkers > 3 {
		return TaskRequest{}, "", "", false, false, fmt.Errorf("--market-workers must be between 1 and 3")
	}
	if len(marketSpecs) > 0 {
		for _, name := range []string{"site", "locale", "currency"} {
			if stringFlagPassed(argv, name) {
				return TaskRequest{}, "", "", false, false, fmt.Errorf("--market cannot be combined with --%s", name)
			}
		}
		if excelInput != "" {
			return TaskRequest{}, "", "", false, false, fmt.Errorf("--market cannot be combined with --excel-input")
		}
		if compareVendor != "" {
			return TaskRequest{}, "", "", false, false, fmt.Errorf("--market cannot be combined with --compare-vendor")
		}
		if runMode == "mode2" || (ratingOnly.set && ratingOnly.value) {
			return TaskRequest{}, "", "", false, false, fmt.Errorf("--market cannot be combined with rating-only or mode2")
		}
	}
	market, err := resolveMarketContext(site, locale, currency)
	if err != nil {
		return TaskRequest{}, "", "", false, false, flagValueError(err)
	}
	var markets []MarketContext
	if len(marketSpecs) > 0 {
		markets, err = resolveMarketSpecs([]string(marketSpecs))
		if err != nil {
			return TaskRequest{}, "", "", false, false, err
		}
		if len(markets) == 0 {
			return TaskRequest{}, "", "", false, false, fmt.Errorf("at least one --market is required")
		}
		market = MarketContext{}
	}
	if runMode != "" && runMode != "mode1" && runMode != "mode2" {
		return TaskRequest{}, "", "", false, false, fmt.Errorf("--run-mode must be mode1 or mode2")
	}
	if logLevel != "quiet" && logLevel != "normal" && logLevel != "verbose" {
		return TaskRequest{}, "", "", false, false, fmt.Errorf("--log-level must be quiet, normal, or verbose")
	}
	if priceQualityRetries < -1 {
		return TaskRequest{}, "", "", false, false, fmt.Errorf("--price-quality-retries must be >= 0, or -1 for mode default")
	}
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
	if fastURLThresholdSeconds < 1 {
		return TaskRequest{}, "", "", false, false, fmt.Errorf("--fast-url-threshold must be >= 1")
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
	if runtimeOptions.VendorAPIBlockMinAttempts < 1 {
		return TaskRequest{}, "", "", false, false, fmt.Errorf("--vendor-api-block-min-attempts must be >= 1")
	}
	if runtimeOptions.GlobalAPIBlockMinProcessed < 1 {
		return TaskRequest{}, "", "", false, false, fmt.Errorf("--global-api-block-min-processed must be >= 1")
	}
	if runtimeOptions.RiskToSafeThreshold < 1 {
		return TaskRequest{}, "", "", false, false, fmt.Errorf("--risk-to-safe-threshold must be >= 1")
	}
	if runtimeOptions.CheckpointEveryURLs < 0 {
		return TaskRequest{}, "", "", false, false, fmt.Errorf("--checkpoint-every-urls must be >= 0")
	}
	runtimeOptions.URLTotalTimeout = time.Duration(urlTotalTimeoutSeconds) * time.Second
	runtimeOptions.NavigationTimeout = time.Duration(navigationTimeoutSeconds) * time.Second
	runtimeOptions.WorkerStallTimeout = time.Duration(workerStallTimeoutSeconds) * time.Second
	runtimeOptions.FastURLThreshold = time.Duration(fastURLThresholdSeconds) * time.Second

	options := resolveRunOptions(runMode, headless, ratingOnly, realPrice, productInfo)
	overrides, err := buildDelayOverrides(pageDelayMin, pageDelayMax, scrollDelayMin, scrollDelayMax, skuClickDelayMin, skuClickDelayMax, skuPriceDelayMin, skuPriceDelayMax, skuBetweenDelayMin, skuBetweenDelayMax)
	if err != nil {
		return TaskRequest{}, "", "", false, false, err
	}
	if delayProfile == "normal" && !stringFlagPassed(argv, "delay-profile") && runMode == "mode2" {
		delayProfile = "fast"
	}
	delayOptions, err := resolveDelayOptions(delayProfile, overrides)
	if err != nil {
		return TaskRequest{}, "", "", false, false, err
	}
	if _, err := resolveDelayOptions(qualityRerunDelayProfile, delayOverrides{}); err != nil {
		return TaskRequest{}, "", "", false, false, fmt.Errorf("--quality-rerun-delay-profile %w", err)
	}
	if priceQualityRetries == -1 {
		priceQualityRetries = defaultPriceQualityRetries(runMode, options)
	}
	qualityRerun := QualityRerunOptions{Enabled: !noQualityRerun && runMode != "mode2" && !options.ratingOnly, Headless: false, DelayProfile: qualityRerunDelayProfile}
	if qualityRerunHeadless.set {
		qualityRerun.Headless = qualityRerunHeadless.value
	}
	return TaskRequest{
		Market:              market,
		Markets:             markets,
		MarketWorkers:       marketWorkers,
		FreshProfile:        freshProfile,
		InputPaths:          []string(inputs),
		URLsFile:            urlsFile,
		ExcelInput:          excelInput,
		OutputPath:          output,
		OutputDir:           outputDir,
		LogFile:             logFile,
		SessionFileExplicit: sessionFile != "",
		Workers:             workers,
		StartupStagger:      time.Duration(max(startupStagger, 0) * float64(time.Second)),
		StopGraceSeconds:    max(stopGrace, 0),
		RatingOnly:          options.ratingOnly,
		ExtractRealPrice:    options.realPrice,
		ExtractProductInfo:  options.productInfo,
		DelayOptions:        delayOptions,
		PriceQualityRetry:   PriceQualityRetryOptions{MaxRetries: priceQualityRetries},
		QualityRerun:        qualityRerun,
		Runtime:             runtimeOptions,
		BrowserOptions: BrowserOptions{
			Browser:            browser,
			Headless:           options.headless,
			SessionFile:        sessionFile,
			Timeout:            time.Duration(timeoutSeconds) * time.Second,
			MemoryGuardEnabled: !disableMemoryGuard,
			MemoryLimitMB:      memoryLimit,
			RestartEvery:       restartEvery,
		},
		CompareVendor: compareVendor,
	}, runMode, logLevel, jsonLines, noColor, nil
}

func flagValueError(err error) error {
	message := err.Error()
	switch {
	case strings.Contains(message, "unsupported site"):
		values := supportedSiteCodes()
		sort.Strings(values)
		return fmt.Errorf("--site: %s (supported: %s)", message, strings.Join(values, ", "))
	case strings.Contains(message, "unsupported locale"):
		values := make([]string, 0, len(advertisedTTDLocales))
		for value := range advertisedTTDLocales {
			values = append(values, value)
		}
		sort.Strings(values)
		return fmt.Errorf("--locale: %s (supported: %s)", message, strings.Join(values, ", "))
	case strings.Contains(message, "unsupported currency"):
		values := make([]string, 0, len(currencyPricePrefixes))
		for value := range currencyPricePrefixes {
			values = append(values, value)
		}
		sort.Strings(values)
		return fmt.Errorf("--currency: %s (supported: %s)", message, strings.Join(values, ", "))
	default:
		return err
	}
}

type runOptions struct {
	headless    bool
	ratingOnly  bool
	realPrice   bool
	productInfo bool
}

func resolveRunOptions(runMode string, headless triState, ratingOnly triState, realPrice triState, productInfo triState) runOptions {
	options := runOptions{headless: true, ratingOnly: false, realPrice: true, productInfo: true}
	switch runMode {
	case "mode1":
		options = runOptions{headless: true, ratingOnly: false, realPrice: true, productInfo: true}
	case "mode2":
		options = runOptions{headless: true, ratingOnly: true, realPrice: false, productInfo: true}
	}
	if headless.set {
		options.headless = headless.value
	}
	// mode2 is intentionally a fixed compatibility mode matching the legacy GUI
	// checkboxes: "仅抓取评分" + "抓取商品资讯".  It must never expand SKU
	// combinations or emit price columns, even if callers accidentally pass
	// --no-rating-only or --real-price from older scripts.
	if runMode == "mode2" {
		return options
	}
	if ratingOnly.set {
		options.ratingOnly = ratingOnly.value
	}
	if realPrice.set {
		options.realPrice = realPrice.value
	}
	if productInfo.set {
		options.productInfo = productInfo.value
	}
	return options
}

func defaultPriceQualityRetries(runMode string, options runOptions) int {
	if runMode == "mode2" || options.ratingOnly {
		return 0
	}
	return 2
}

func stringFlagPassed(argv []string, name string) bool {
	long := "--" + name
	short := "-" + name
	for i, arg := range argv {
		if arg == long || arg == short || strings.HasPrefix(arg, long+"=") || strings.HasPrefix(arg, short+"=") {
			return true
		}
		if (arg == long || arg == short) && i+1 < len(argv) {
			return true
		}
	}
	return false
}

func buildDelayOverrides(pageMin float64, pageMax float64, scrollMin float64, scrollMax float64, skuClickMin float64, skuClickMax float64, skuPriceMin float64, skuPriceMax float64, skuBetweenMin float64, skuBetweenMax float64) (delayOverrides, error) {
	page, err := optionalDelayOverride(pageMin, pageMax, "page")
	if err != nil {
		return delayOverrides{}, err
	}
	scroll, err := optionalDelayOverride(scrollMin, scrollMax, "scroll")
	if err != nil {
		return delayOverrides{}, err
	}
	skuClick, err := optionalDelayOverride(skuClickMin, skuClickMax, "sku-click")
	if err != nil {
		return delayOverrides{}, err
	}
	skuPrice, err := optionalDelayOverride(skuPriceMin, skuPriceMax, "sku-price")
	if err != nil {
		return delayOverrides{}, err
	}
	skuBetween, err := optionalDelayOverride(skuBetweenMin, skuBetweenMax, "sku-between")
	if err != nil {
		return delayOverrides{}, err
	}
	return delayOverrides{page: page, scroll: scroll, skuClick: skuClick, skuPrice: skuPrice, skuBetween: skuBetween}, nil
}

func optionalDelayOverride(minValue float64, maxValue float64, label string) (delayOverride, error) {
	var override delayOverride
	if minValue < 0 && minValue != -1 {
		return override, fmt.Errorf("--%s-delay-min must not be negative", label)
	}
	if maxValue < 0 && maxValue != -1 {
		return override, fmt.Errorf("--%s-delay-max must not be negative", label)
	}
	if minValue >= 0 {
		value := minValue
		override.min = &value
	}
	if maxValue >= 0 {
		value := maxValue
		override.max = &value
	}
	return override, nil
}

func absPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		out = append(out, absPath(path))
	}
	return out
}
