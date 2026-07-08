package tripcdp

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type vendorRuntimeSummary struct {
	Vendor         string `json:"vendor"`
	State          string `json:"state"`
	Reason         string `json:"reason,omitempty"`
	URLsDone       int    `json:"urls_done"`
	URLCount       int    `json:"url_count"`
	Rows           int    `json:"rows"`
	APIBlockedURLs int    `json:"api_blocked_urls"`
	RiskEvents     int    `json:"risk_events"`
	OutputPath     string `json:"output_path,omitempty"`
}

type runtimeRunSummary struct {
	Event           string                 `json:"event"`
	GlobalState     string                 `json:"global_state"`
	Reason          string                 `json:"reason,omitempty"`
	ProcessedURLs   int                    `json:"processed_urls"`
	URLCount        int                    `json:"url_count"`
	APIBlockedURLs  int                    `json:"api_blocked_urls"`
	RiskEvents      int                    `json:"risk_events"`
	DeadlineExpired bool                   `json:"deadline_expired"`
	Vendors         []vendorRuntimeSummary `json:"vendors"`
}

func writeRuntimeSummary(outputDir string, summary runtimeRunSummary, reporter Reporter) {
	if summary.Event == "" {
		summary.Event = "runtime_summary"
	}
	dir := filepath.Join(expandPath(outputDir), "_diagnostics")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	path := filepath.Join(dir, "runtime_summary.jsonl")
	raw, err := json.Marshal(summary)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, append(raw, '\n'), 0o644)
	if reporter != nil {
		reporter(Event{"event": summary.Event, "stage": "runtime", "global_state": summary.GlobalState, "reason": summary.Reason, "processed_urls": summary.ProcessedURLs, "url_count": summary.URLCount, "api_blocked_urls": summary.APIBlockedURLs, "risk_events": summary.RiskEvents, "deadline_expired": summary.DeadlineExpired, "diagnostic_path": path})
	}
}
