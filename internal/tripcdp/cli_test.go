package tripcdp

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

func TestResolveRunOptionsAppliesModeThenExplicitFlags(t *testing.T) {
	mode1 := resolveRunOptions("mode1", triState{}, triState{}, triState{}, triState{})
	if mode1.headless {
		t.Fatal("mode1 should be visible (headed) by default")
	}
	if mode1.ratingOnly || !mode1.realPrice || !mode1.productInfo {
		t.Fatalf("mode1 should be full scrape with real price and product info: %+v", mode1)
	}

	options := resolveRunOptions(
		"mode2",
		triState{},
		triState{set: true, value: false},
		triState{set: true, value: true},
		triState{},
	)

	if options.headless {
		t.Fatal("mode2 should remain visible (headed) unless explicitly overridden")
	}
	if !options.ratingOnly {
		t.Fatal("mode2 must remain rating-only even if --no-rating-only is passed")
	}
	if options.realPrice {
		t.Fatal("mode2 must never scrape real price even if --real-price is passed")
	}
	if !options.productInfo {
		t.Fatal("mode2 should keep product info enabled")
	}
}

func TestParseRunModeDelayDefaults(t *testing.T) {
	mode1, runMode, _, _, _, err := parseArgs([]string{"--run-mode", "mode1", "--input", "https://www.trip.com/detail/12345"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("mode1 parse failed: %v", err)
	}
	if runMode != "mode1" {
		t.Fatalf("run mode = %q, want mode1", runMode)
	}
	if mode1.BrowserOptions.Headless || mode1.RatingOnly || !mode1.ExtractRealPrice || !mode1.ExtractProductInfo {
		t.Fatalf("unexpected mode1 request: %+v", mode1)
	}
	if !mode1.QualityRerun.Enabled || mode1.QualityRerun.Headless || mode1.QualityRerun.DelayProfile != "safe" {
		t.Fatalf("mode1 quality rerun defaults = %+v, want enabled headed safe", mode1.QualityRerun)
	}
	if mode1.DelayOptions.Profile != "normal" {
		t.Fatalf("mode1 delay profile = %q, want normal", mode1.DelayOptions.Profile)
	}
	if mode1.PriceQualityRetry.MaxRetries != 2 {
		t.Fatalf("mode1 price quality retries = %d, want 2", mode1.PriceQualityRetry.MaxRetries)
	}

	mode2, runMode, _, _, _, err := parseArgs([]string{"--run-mode", "mode2", "--input", "https://www.trip.com/detail/12345"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("mode2 parse failed: %v", err)
	}
	if runMode != "mode2" {
		t.Fatalf("run mode = %q, want mode2", runMode)
	}
	if mode2.BrowserOptions.Headless || !mode2.RatingOnly || mode2.ExtractRealPrice || !mode2.ExtractProductInfo {
		t.Fatalf("unexpected mode2 request: %+v", mode2)
	}
	if mode2.QualityRerun.Enabled {
		t.Fatalf("mode2 quality rerun should be disabled: %+v", mode2.QualityRerun)
	}
	if mode2.DelayOptions.Profile != "fast" {
		t.Fatalf("mode2 delay profile = %q, want fast", mode2.DelayOptions.Profile)
	}
	if mode2.PriceQualityRetry.MaxRetries != 0 {
		t.Fatalf("mode2 price quality retries = %d, want 0", mode2.PriceQualityRetry.MaxRetries)
	}

	overridden, _, _, _, _, err := parseArgs([]string{"--run-mode", "mode2", "--delay-profile", "slow", "--input", "https://www.trip.com/detail/12345"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("mode2 override parse failed: %v", err)
	}
	if overridden.DelayOptions.Profile != "slow" {
		t.Fatalf("explicit delay profile should override mode2 default, got %q", overridden.DelayOptions.Profile)
	}

	retryOverride, _, _, _, _, err := parseArgs([]string{"--run-mode", "mode2", "--price-quality-retries", "3", "--input", "https://www.trip.com/detail/12345"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("retry override parse failed: %v", err)
	}
	if retryOverride.PriceQualityRetry.MaxRetries != 3 {
		t.Fatalf("explicit price quality retries = %d, want 3", retryOverride.PriceQualityRetry.MaxRetries)
	}
}

func TestParseWorkersOutOfRangeIsDeferredToValidation(t *testing.T) {
	request, _, _, _, _, err := parseArgs([]string{"--excel-input", "vendors.xlsx", "--workers", "6"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseArgs returned unexpected parse error: %v", err)
	}
	if _, err := resolveWorkerCount(request.Workers, 2); err == nil {
		t.Fatal("expected --workers 6 to fail validation")
	}
}

func TestBoolFlagsDoNotRequireValues(t *testing.T) {
	request, _, _, _, _, err := parseArgs([]string{"--input", "https://www.trip.com/detail/12345", "--headless", "--rating-only", "--no-real-price", "--no-product-info"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if !request.BrowserOptions.Headless || !request.RatingOnly || request.ExtractRealPrice || request.ExtractProductInfo {
		t.Fatalf("unexpected parsed request: %+v", request)
	}
}

func TestParseManualSelectedProductsContract(t *testing.T) {
	request, runMode, _, _, _, err := parseArgs([]string{
		"-input", "https://hk.trip.com/things-to-do/detail/109223304/?locale=zh-HK&curr=HKD",
		"-input", "https://hk.trip.com/things-to-do/detail/109223305/?locale=zh-HK&curr=HKD",
		"-output-dir", "/Users/example/Documents/trip-sku-price-scrape/manual-selected",
		"-product-info",
		"-no-quality-rerun",
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if runMode != "" {
		t.Fatalf("run mode = %q, want default full scrape", runMode)
	}
	if len(request.InputPaths) != 2 {
		t.Fatalf("input paths = %#v, want 2 URLs", request.InputPaths)
	}
	if request.BrowserOptions.Headless {
		t.Fatal("manual selected-product scrape should use a visible browser")
	}
	if request.DelayOptions.Profile != "fast" {
		t.Fatalf("delay profile = %q, want fast", request.DelayOptions.Profile)
	}
	if request.OutputDir != "/Users/example/Documents/trip-sku-price-scrape/manual-selected" {
		t.Fatalf("output dir = %q", request.OutputDir)
	}
	if request.RatingOnly || !request.ExtractRealPrice || !request.ExtractProductInfo {
		t.Fatalf("unexpected manual scrape options: %+v", request)
	}
	if request.QualityRerun.Enabled {
		t.Fatalf("quality rerun should be disabled: %+v", request.QualityRerun)
	}
}

func TestParseSiteParameter(t *testing.T) {
	request, _, _, _, _, err := parseArgs([]string{
		"--site", "sg",
		"--input", "https://trip.com/things-to-do/detail/45892282/",
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if request.Market.Code != "sg" {
		t.Fatalf("market code = %q, want sg", request.Market.Code)
	}

	if _, _, _, _, _, err := parseArgs([]string{"--site", "xx", "--input", "https://trip.com/detail/1"}, &bytes.Buffer{}); err == nil {
		t.Fatal("unsupported site should fail")
	}
}

func TestParseMarketDefaultsAndOverrides(t *testing.T) {
	r, _, _, _, _, err := parseArgs([]string{"--input", "https://trip.com/detail/1"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Market.Code != "hk" || r.Market.Locale != "zh-HK" || r.Market.Currency != "HKD" {
		t.Fatalf("%+v", r.Market)
	}

	r, _, _, _, _, err = parseArgs([]string{"--site", "sg", "--locale", "zh_sg", "--currency", "myr", "--input", "https://trip.com/detail/1"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Market.Host != "sg.trip.com" || r.Market.Locale != "zh-SG" || r.Market.Currency != "MYR" {
		t.Fatalf("%+v", r.Market)
	}
}

func TestParseMarketFlagErrors(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--site", "xx", "--input", "https://trip.com/detail/1"}, "--site"},
		{[]string{"--locale", "ja-US", "--input", "https://trip.com/detail/1"}, "--locale"},
		{[]string{"--currency", "ZZZ", "--input", "https://trip.com/detail/1"}, "--currency"},
	} {
		_, _, _, _, _, err := parseArgs(tc.args, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("args %v err=%v, want %s", tc.args, err, tc.want)
		}
	}
	_, _, _, _, _, err := parseArgs([]string{"--site", "xx", "--input", "https://trip.com/detail/1"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "supported: ae, au, br") {
		t.Fatalf("site values are not sorted: %v", err)
	}
}

func TestParseInvalidSiteListsSupportedValuesSorted(t *testing.T) {
	_, _, _, _, _, err := parseArgs([]string{"--site", "xx", "--input", "https://trip.com/detail/1"}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("unsupported site should fail")
	}
	message := err.Error()
	if !strings.Contains(message, "supported: ae, au, br") {
		t.Fatalf("site supported values are not sorted: %s", message)
	}
}

func TestParseFreshProfileParameter(t *testing.T) {
	request, _, _, _, _, err := parseArgs([]string{
		"--fresh-profile",
		"--input", "https://trip.com/things-to-do/detail/45892282/",
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if !request.FreshProfile {
		t.Fatal("fresh profile flag was not preserved")
	}
}

func TestParseDefaultsDoNotEnableLogFileOrExplicitSession(t *testing.T) {
	request, _, _, _, _, err := parseArgs([]string{"--input", "https://www.trip.com/detail/12345"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if request.LogFile != "" {
		t.Fatalf("default log file = %q, want empty", request.LogFile)
	}
	if request.SessionFileExplicit {
		t.Fatal("session should not be explicit by default")
	}

	request, _, _, _, _, err = parseArgs([]string{"--input", "https://www.trip.com/detail/12345", "--session-file", "/tmp/session.json"}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseArgs with session failed: %v", err)
	}
	if !request.SessionFileExplicit {
		t.Fatal("session should be explicit when --session-file is passed")
	}
}

func TestParseDelayProfileAndOverrides(t *testing.T) {
	request, _, _, _, _, err := parseArgs([]string{
		"--input", "https://www.trip.com/detail/12345",
		"--delay-profile", "safe",
		"--sku-click-delay-min", "2",
		"--sku-click-delay-max", "3",
		"--page-delay-min", "5",
		"--page-delay-max", "8",
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if request.DelayOptions.Profile != "safe" {
		t.Fatalf("delay profile = %q, want safe", request.DelayOptions.Profile)
	}
	if request.DelayOptions.SKUClick.Min != 2*time.Second || request.DelayOptions.SKUClick.Max != 3*time.Second {
		t.Fatalf("sku click delay = %s-%s, want 2s-3s", request.DelayOptions.SKUClick.Min, request.DelayOptions.SKUClick.Max)
	}
	if request.DelayOptions.Page.Min != 5*time.Second || request.DelayOptions.Page.Max != 8*time.Second {
		t.Fatalf("page delay = %s-%s, want 5s-8s", request.DelayOptions.Page.Min, request.DelayOptions.Page.Max)
	}
	if request.DelayOptions.Scroll.Min != 300*time.Millisecond || request.DelayOptions.Scroll.Max != 700*time.Millisecond {
		t.Fatalf("scroll delay should keep safe defaults, got %s-%s", request.DelayOptions.Scroll.Min, request.DelayOptions.Scroll.Max)
	}
}

func TestParseRejectsInvalidDelayOptions(t *testing.T) {
	if _, _, _, _, _, err := parseArgs([]string{"--input", "https://www.trip.com/detail/12345", "--delay-profile", "danger"}, &bytes.Buffer{}); err == nil {
		t.Fatal("unknown delay profile should fail")
	}
	if _, _, _, _, _, err := parseArgs([]string{"--input", "https://www.trip.com/detail/12345", "--page-delay-min", "3", "--page-delay-max", "2"}, &bytes.Buffer{}); err == nil {
		t.Fatal("min > max delay override should fail")
	}
	if _, _, _, _, _, err := parseArgs([]string{"--input", "https://www.trip.com/detail/12345", "--scroll-delay-min", "-0.5"}, &bytes.Buffer{}); err == nil {
		t.Fatal("negative delay override should fail")
	}
}

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
