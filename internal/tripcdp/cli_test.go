package tripcdp

import (
	"bytes"
	"testing"
	"time"
)

func TestResolveRunOptionsAppliesModeThenExplicitFlags(t *testing.T) {
	options := resolveRunOptions(
		"mode2",
		triState{},
		triState{set: true, value: false},
		triState{set: true, value: true},
		triState{},
	)

	if options.headless {
		t.Fatal("mode2 should remain headed unless explicitly overridden")
	}
	if options.ratingOnly {
		t.Fatal("explicit --no-rating-only should override mode2")
	}
	if !options.realPrice {
		t.Fatal("explicit --real-price should override mode2")
	}
	if !options.productInfo {
		t.Fatal("mode2 should keep product info enabled")
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
