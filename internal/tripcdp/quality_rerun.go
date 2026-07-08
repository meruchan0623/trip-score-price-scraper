package tripcdp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type qualityRerunCandidate struct {
	URL          string
	Vendor       string
	ProductID    string
	Reason       string
	FailureClass FailureClass
}

type qualityRerunStats struct {
	Attempted   int
	Recovered   int
	FinalFailed int
}

func collectQualityRerunCandidates(rows []map[string]string) []qualityRerunCandidate {
	candidates := []qualityRerunCandidate{}
	seen := map[string]bool{}
	for _, block := range groupRowsByURL(rows) {
		if len(block) == 0 {
			continue
		}
		url := firstNonEmptyRowValue(block, "URL")
		if url == "" || seen[normalizeURL(url)] {
			continue
		}
		score := qualityScoreRows(block)
		if score.needsRerun {
			failureClass := ClassifyRows(block)
			if !FailureClassRetryableByQualityRerun(failureClass) {
				continue
			}
			seen[normalizeURL(url)] = true
			candidates = append(candidates, qualityRerunCandidate{
				URL:          url,
				Vendor:       firstNonEmptyRowValue(block, "厂商"),
				ProductID:    firstNonEmptyRowValue(block, "商品ID"),
				Reason:       score.reason,
				FailureClass: failureClass,
			})
		}
	}
	return candidates
}

func mergeQualityRerunRows(rows []map[string]string, url string, rerunRows []map[string]string) ([]map[string]string, bool) {
	target := normalizeURL(url)
	if target == "" || len(rerunRows) == 0 {
		return rows, false
	}
	groups := groupRowsByURL(rows)
	merged := make([]map[string]string, 0, len(rows)+len(rerunRows))
	replaced := false
	found := false
	for _, group := range groups {
		groupURL := normalizeURL(firstNonEmptyRowValue(group, "URL"))
		if groupURL == target {
			found = true
			if qualityScoreRows(rerunRows).score > qualityScoreRows(group).score {
				merged = append(merged, cloneRows(rerunRows)...)
				replaced = true
				continue
			}
			merged = append(merged, cloneRows(group)...)
			continue
		}
		merged = append(merged, cloneRows(group)...)
	}
	if !found && qualityScoreRows(rerunRows).score > 0 {
		merged = append(merged, cloneRows(rerunRows)...)
		replaced = true
	}
	return merged, replaced
}

type qualityScore struct {
	score      int
	needsRerun bool
	reason     string
}

func qualityScoreRows(rows []map[string]string) qualityScore {
	trusted, untrusted, hasRisk, hasError := 0, 0, false, false
	for _, row := range rows {
		status := strings.TrimSpace(row["价格状态"])
		switch {
		case isTrustedPriceStatus(status):
			trusted++
		case status != "":
			untrusted++
			if status == priceStatusRiskBlocked || status == priceStatusPartialUnhealthy || status == priceStatusUntrustedResourceMiss {
				hasRisk = true
			}
		}
		note := strings.ToLower(strings.TrimSpace(row["抓取备注"] + " " + row["评分"]))
		if strings.Contains(note, "处理错误") || strings.Contains(note, "页面未加载") || strings.Contains(note, "product dom not ready") || strings.Contains(note, "whaleguard") || strings.Contains(note, "cloudflare") || strings.Contains(note, "价格质量检查失败") {
			hasError = true
		}
	}
	if trusted > 0 {
		return qualityScore{score: 1000 + trusted}
	}
	if untrusted > 0 || hasRisk || hasError {
		reason := "low_quality"
		if hasRisk {
			reason = "risk_or_untrusted_price"
		} else if hasError {
			reason = "error_row"
		}
		return qualityScore{score: max(1, len(rows)), needsRerun: true, reason: reason}
	}
	return qualityScore{score: 100 + len(rows)}
}

func groupRowsByURL(rows []map[string]string) [][]map[string]string {
	groups := [][]map[string]string{}
	current := []map[string]string{}
	currentURL := ""
	for _, row := range rows {
		url := strings.TrimSpace(row["URL"])
		if url != "" && normalizeURL(url) != normalizeURL(currentURL) {
			if len(current) > 0 {
				groups = append(groups, current)
			}
			current = []map[string]string{row}
			currentURL = url
			continue
		}
		if len(current) == 0 {
			currentURL = url
		}
		current = append(current, row)
	}
	if len(current) > 0 {
		groups = append(groups, current)
	}
	return groups
}

func firstNonEmptyRowValue(rows []map[string]string, key string) string {
	for _, row := range rows {
		if value := strings.TrimSpace(row[key]); value != "" {
			return value
		}
	}
	return ""
}

func cloneRows(rows []map[string]string) []map[string]string {
	out := make([]map[string]string, 0, len(rows))
	for _, row := range rows {
		cloned := map[string]string{}
		for key, value := range row {
			cloned[key] = value
		}
		out = append(out, cloned)
	}
	return out
}

func runQualityReruns(ctx context.Context, rows []map[string]string, request TaskRequest, vendor string, outputPath string, globalTotal int, reporter Reporter, limiter chan struct{}) ([]map[string]string, qualityRerunStats) {
	candidates := collectQualityRerunCandidates(rows)
	stats := qualityRerunStats{Attempted: len(candidates)}
	if len(candidates) == 0 || !request.QualityRerun.Enabled || request.RatingOnly {
		stats.FinalFailed = len(candidates)
		return rows, stats
	}
	if reporter != nil {
		reporter(Event{"event": "quality_rerun_planned", "stage": "quality_rerun", "vendor": vendor, "url_count": len(candidates)})
	}
	if limiter != nil {
		select {
		case limiter <- struct{}{}:
			defer func() { <-limiter }()
		case <-ctx.Done():
			stats.FinalFailed = len(candidates)
			return rows, stats
		}
	}
	rerunRequest := request
	rerunRequest.InputPaths = nil
	rerunRequest.URLsFile = ""
	rerunRequest.ExcelInput = ""
	rerunRequest.OutputPath = outputPath
	rerunRequest.Workers = 1
	rerunRequest.BrowserOptions.Headless = request.QualityRerun.Headless
	rerunRequest.BrowserOptions.UserDataDir = filepath.Join(filepath.Dir(outputPath), "_quality_rerun", safeFilename(vendor), "profile")
	rerunRequest.BrowserOptions.SessionFile = filepath.Join(filepath.Dir(outputPath), "_quality_rerun", safeFilename(vendor), "session.json")
	if delay, err := resolveDelayOptions(request.QualityRerun.DelayProfile, delayOverrides{}); err == nil {
		rerunRequest.DelayOptions = delay
	}
	rerunReporter := func(event Event) {
		event["quality_rerun"] = true
		if reporter != nil {
			reporter(event)
		}
	}
	scraper := newTripScraper(rerunRequest, rerunReporter, vendor, 1, globalTotal)
	if err := scraper.start(ctx); err != nil {
		if reporter != nil {
			reporter(Event{"event": "quality_rerun_failed", "stage": "quality_rerun", "vendor": vendor, "error": err.Error(), "url_count": len(candidates)})
		}
		stats.FinalFailed = len(candidates)
		return rows, stats
	}
	defer scraper.close()
	merged := rows
	for index, candidate := range candidates {
		if ctx.Err() != nil {
			stats.FinalFailed += len(candidates) - index
			break
		}
		if reporter != nil {
			reporter(Event{"event": "quality_rerun_started", "stage": "quality_rerun", "vendor": vendor, "url": candidate.URL, "product_id": candidate.ProductID, "reason": candidate.Reason, "failure_class": candidate.FailureClass, "url_index": index + 1, "url_total": len(candidates), "headless": rerunRequest.BrowserOptions.Headless, "delay_profile": rerunRequest.DelayOptions.Profile})
		}
		item := ScrapeItem{URL: candidate.URL, Vendor: vendor, InputOrder: index}
		rerunRows := scraper.scrapeOne(ctx, item, index+1, len(candidates))
		next, replaced := mergeQualityRerunRows(merged, candidate.URL, rerunRows)
		if replaced {
			stats.Recovered++
			merged = next
			if reporter != nil {
				reporter(Event{"event": "quality_rerun_replaced", "stage": "quality_rerun", "vendor": vendor, "url": candidate.URL, "product_id": candidate.ProductID, "rows": len(rerunRows)})
			}
		} else {
			stats.FinalFailed++
			if reporter != nil {
				reporter(Event{"event": "quality_rerun_kept", "stage": "quality_rerun", "vendor": vendor, "url": candidate.URL, "product_id": candidate.ProductID, "rows": len(rerunRows)})
			}
		}
		if index < len(candidates)-1 {
			scraper.cleanupAfterURL()
			time.Sleep(500 * time.Millisecond)
		}
	}
	if reporter != nil {
		reporter(Event{"event": "quality_rerun_summary", "stage": "quality_rerun", "vendor": vendor, "attempted_urls": stats.Attempted, "recovered_urls": stats.Recovered, "final_failed_urls": stats.FinalFailed})
	}
	writeQualityRerunSummary(filepath.Dir(outputPath), vendor, stats, collectQualityRerunCandidates(merged))
	return merged, stats
}

func filterQualityFailuresForRows(failures []qualityFailureSummary, rows []map[string]string) []qualityFailureSummary {
	if len(failures) == 0 {
		return failures
	}
	candidates := collectQualityRerunCandidates(rows)
	failedURLs := map[string]bool{}
	for _, candidate := range candidates {
		failedURLs[normalizeURL(candidate.URL)] = true
	}
	out := failures[:0]
	for _, failure := range failures {
		if failedURLs[normalizeURL(failure.URL)] {
			out = append(out, failure)
		}
	}
	return out
}

func writeQualityRerunSummary(outputDir string, vendor string, stats qualityRerunStats, finalCandidates []qualityRerunCandidate) {
	dir := filepath.Join(expandPath(outputDir), "_diagnostics")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	path := filepath.Join(dir, "quality_rerun_summary.jsonl")
	payload := Event{
		"event":             "quality_rerun_summary",
		"vendor":            vendor,
		"attempted_urls":    stats.Attempted,
		"recovered_urls":    stats.Recovered,
		"final_failed_urls": stats.FinalFailed,
		"final_candidates":  finalCandidates,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	handle, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer handle.Close()
	_, _ = handle.Write(append(raw, '\n'))
}
