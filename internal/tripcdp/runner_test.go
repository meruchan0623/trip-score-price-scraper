package tripcdp

import (
	"context"
	"testing"
	"time"
)

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
