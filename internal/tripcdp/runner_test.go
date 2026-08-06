package tripcdp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPrepareRuntimeSessionFreshProfileDoesNotCopySavedSession(t *testing.T) {
	dir := t.TempDir()
	seed := filepath.Join(dir, "saved.json")
	runtimePath := filepath.Join(dir, "runtime", "session.json")
	if err := os.WriteFile(seed, []byte(`{"cookies":[{"name":"saved"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := prepareRuntimeSession(seed, runtimePath, true); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(runtimePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) == `{"cookies":[{"name":"saved"}]}` {
		t.Fatal("fresh profile copied the saved session")
	}
}

func TestCanStartWorkerStopsWhenContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if canStartWorker(ctx, 1, 0, 5) {
		t.Fatal("canceled context should prevent starting workers")
	}
}

func TestCancellableSleepReturnsImmediatelyOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	if cancellableSleep(ctx, time.Second) {
		t.Fatal("cancellableSleep should return false after cancel")
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("cancellableSleep took too long: %s", elapsed)
	}
}

func TestForceStopperRunsRegisteredCancelAndClose(t *testing.T) {
	stopper := &forceStopper{}
	called := 0
	stopper.add(func() { called++ })
	stopper.add(func() { called++ })
	stopper.run()
	if called != 2 {
		t.Fatalf("called %d functions, want 2", called)
	}
}

func TestMaybeRestartDoesNotStartWhenCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	scraper := &tripScraper{
		request: TaskRequest{BrowserOptions: BrowserOptions{MemoryGuardEnabled: true, RestartEvery: 1}},
	}
	if err := scraper.maybeRestart(ctx); err != context.Canceled {
		t.Fatalf("maybeRestart error = %v, want context.Canceled", err)
	}
	if scraper.processed != 0 {
		t.Fatalf("processed = %d, want 0", scraper.processed)
	}
}

func TestValidateSinglePriceScrapeRowsRejectsCloudflareProduct(t *testing.T) {
	rows := []map[string]string{
		{"商品ID": "57501862", "URL": "https://hk.trip.com/things-to-do/detail/57501862/", "商品名称": "Product A", "SKU层级1": "1天", "货币": "HKD", "价格状态": priceStatusTrustedPromo, "实价": "19.90"},
		{"商品ID": "57501863", "URL": "https://hk.trip.com/things-to-do/detail/57501863/", "商品名称": "Product B", "SKU层级1": "1天", "货币": "HKD", "价格状态": priceStatusRiskBlocked, "抓取备注": "被Cloudflare拦截"},
	}

	failed, err := validateSinglePriceScrapeRows(rows)

	if failed != 1 {
		t.Fatalf("failed products = %d, want 1", failed)
	}
	want := "57501863 价格抓取失败，原因：Trip.com/Cloudflare 风险拦截，未获得完整 SKU 价格。"
	if got := fmt.Sprint(err); got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestValidateSinglePriceScrapeRowsRejectsMissingRequiredFields(t *testing.T) {
	rows := []map[string]string{{
		"商品ID":   "57501862",
		"URL":    "https://hk.trip.com/things-to-do/detail/57501862/",
		"商品名称":   "Product A",
		"SKU层级1": "1天",
		"价格状态":   priceStatusTrustedBase,
		"实价":     "19.90",
	}}

	failed, err := validateSinglePriceScrapeRows(rows)

	if failed != 1 || err == nil {
		t.Fatalf("failed products = %d, error = %v", failed, err)
	}
}

func TestScrapeSingleReportsFreshSessionPreparationFailure(t *testing.T) {
	blockedParent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockedParent, []byte("block"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := scrapeSingle(TaskRequest{
		Site:         "hk",
		FreshProfile: true,
		InputPaths:   []string{"https://hk.trip.com/things-to-do/detail/45892282/"},
		OutputPath:   filepath.Join(blockedParent, "result.xlsx"),
	}, func(Event) {}, "")

	if result.Success || result.ExitCode != 4 || !strings.Contains(result.Error, "session prepare failed") {
		t.Fatalf("result = %+v, want explicit session preparation failure", result)
	}
}
