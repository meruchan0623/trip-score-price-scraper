package tripcdp

import (
	"testing"
	"time"
)

func TestDefaultRuntimeOptions(t *testing.T) {
	options := defaultRuntimeOptions()
	if options.DeadlineHHMM != "0530" {
		t.Fatalf("DeadlineHHMM = %q, want 0530", options.DeadlineHHMM)
	}
	if options.URLTotalTimeout != 300*time.Second {
		t.Fatalf("URLTotalTimeout = %s", options.URLTotalTimeout)
	}
	if options.WorkerStallTimeout != 10*time.Minute {
		t.Fatalf("WorkerStallTimeout = %s", options.WorkerStallTimeout)
	}
	if options.VendorAPIBlockRatio != 0.70 {
		t.Fatalf("VendorAPIBlockRatio = %.2f", options.VendorAPIBlockRatio)
	}
}

func TestParseLocalDeadlineSameDay(t *testing.T) {
	now := time.Date(2026, 7, 8, 0, 15, 0, 0, time.Local)
	deadline, ok, err := parseLocalDeadline("0530", now)
	if err != nil || !ok {
		t.Fatalf("deadline parse failed: ok=%v err=%v", ok, err)
	}
	want := time.Date(2026, 7, 8, 5, 30, 0, 0, time.Local)
	if !deadline.Equal(want) {
		t.Fatalf("deadline = %s, want %s", deadline, want)
	}
}

func TestParseLocalDeadlineNextDayWhenAlreadyPassed(t *testing.T) {
	now := time.Date(2026, 7, 8, 8, 0, 0, 0, time.Local)
	deadline, ok, err := parseLocalDeadline("0530", now)
	if err != nil || !ok {
		t.Fatalf("deadline parse failed: ok=%v err=%v", ok, err)
	}
	want := time.Date(2026, 7, 9, 5, 30, 0, 0, time.Local)
	if !deadline.Equal(want) {
		t.Fatalf("deadline = %s, want %s", deadline, want)
	}
}

func TestParseLocalDeadlineDisabled(t *testing.T) {
	_, ok, err := parseLocalDeadline("", time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("empty deadline should be disabled")
	}
}

func TestParseLocalDeadlineRejectsInvalid(t *testing.T) {
	_, _, err := parseLocalDeadline("25:99", time.Now())
	if err == nil {
		t.Fatal("invalid deadline should fail")
	}
}
