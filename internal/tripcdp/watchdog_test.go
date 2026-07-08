package tripcdp

import (
	"context"
	"testing"
	"time"
)

func TestWorkerProgressWatchdogCancelsAfterStall(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watchdog := newWorkerProgressWatchdog(20*time.Millisecond, nil, "BC", 1, cancel)
	defer watchdog.stop()
	select {
	case <-ctx.Done():
	case <-time.After(200 * time.Millisecond):
		t.Fatal("watchdog did not cancel stalled context")
	}
}

func TestWorkerProgressWatchdogMarkPreventsEarlyCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watchdog := newWorkerProgressWatchdog(80*time.Millisecond, nil, "BC", 1, cancel)
	defer watchdog.stop()
	watchdog.mark("url_started")
	time.Sleep(30 * time.Millisecond)
	watchdog.mark("url_done")
	select {
	case <-ctx.Done():
		t.Fatal("watchdog canceled too early")
	case <-time.After(30 * time.Millisecond):
	}
}
