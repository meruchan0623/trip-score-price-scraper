package tripcdp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod"
)

func TestUCStealthScriptContainsPythonUCCorePatches(t *testing.T) {
	script := ucStealthScript()
	for _, want := range []string{
		"navigator",
		"webdriver",
		"window.chrome",
		"runtime",
		"navigator.permissions",
		"query",
		"Function.prototype.toString",
		"maxTouchPoints",
		"rtt",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("stealth script missing %q", want)
		}
	}
}

func TestApplyUCStealthSmoke(t *testing.T) {
	if os.Getenv("TRIP_CDP_STEALTH_SMOKE") != "1" {
		t.Skip("set TRIP_CDP_STEALTH_SMOKE=1 to run Chrome stealth smoke test")
	}
	sessionPath := filepath.Join(t.TempDir(), "session.json")
	if err := os.WriteFile(sessionPath, []byte(`{"cookies":[],"local_storage":{},"session_storage":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	scraper := newTripScraper(TaskRequest{
		BrowserOptions: BrowserOptions{
			Headless:    true,
			SessionFile: sessionPath,
			Timeout:     90 * time.Second,
		},
	}, func(Event) {}, defaultVendor, 1, 1)
	if err := scraper.start(ctx); err != nil {
		t.Fatal(err)
	}
	defer scraper.close()
	webdriver, err := scraper.page.Context(ctx).Evaluate(rod.Eval(`() => navigator.webdriver`))
	if err != nil {
		t.Fatal(err)
	}
	if webdriver.Value.Bool() {
		t.Fatalf("navigator.webdriver = true")
	}
	ua, err := scraper.page.Context(ctx).Evaluate(rod.Eval(`() => navigator.userAgent`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ua.Value.Str(), "Headless") {
		t.Fatalf("user agent still contains Headless: %s", ua.Value.Str())
	}
}
