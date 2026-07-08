package tripcdp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteRuntimeSummary(t *testing.T) {
	dir := t.TempDir()
	events := []Event{}
	writeRuntimeSummary(dir, runtimeRunSummary{
		Event:       "runtime_summary",
		GlobalState: "partial",
		Reason:      "deadline",
		Vendors:     []vendorRuntimeSummary{{Vendor: "BC", State: "api_blocked", Reason: "vendor API block threshold reached", URLsDone: 12, URLCount: 20, APIBlockedURLs: 9}},
	}, func(event Event) { events = append(events, event) })
	path := filepath.Join(dir, "_diagnostics", "runtime_summary.jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("summary not written: %v", err)
	}
	if !strings.Contains(string(raw), "api_blocked") || !strings.Contains(string(raw), "BC") {
		t.Fatalf("summary missing fields: %s", raw)
	}
	if len(events) != 1 || events[0]["event"] != "runtime_summary" {
		t.Fatalf("summary event not emitted: %+v", events)
	}
}
