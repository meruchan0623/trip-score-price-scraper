package tripcdp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type qualityFailureSummary struct {
	Vendor             string   `json:"vendor"`
	URL                string   `json:"url"`
	ProductID          string   `json:"product_id"`
	Reason             string   `json:"reason"`
	TrustedRows        int      `json:"trusted_rows"`
	UntrustedRows      int      `json:"untrusted_rows"`
	ResourceMissRows   int      `json:"resource_miss_rows"`
	TotalRows          int      `json:"total_rows"`
	RetryAttempt       int      `json:"retry_attempt"`
	TopFallbackPrices  []string `json:"top_fallback_prices"`
	SelectedTexts      []string `json:"selected_texts"`
	NextDataPresent    bool     `json:"next_data_present"`
	PackageCount       int      `json:"package_count"`
	ResourceCount      int      `json:"resource_count"`
	BatchResponseCount int      `json:"batch_response_count"`
	LastResponseStatus int      `json:"last_response_status"`
	RetryCommand       string   `json:"retry_command"`
}

type qualityRunSummary struct {
	AttemptedFailures int                     `json:"attempted_failures"`
	RecoveredURLs     int                     `json:"recovered_urls"`
	FinalFailedURLs   int                     `json:"final_failed_urls"`
	ByVendor          map[string]int          `json:"by_vendor"`
	ByProductID       map[string]int          `json:"by_product_id"`
	Failures          []qualityFailureSummary `json:"failures"`
}

func newQualityRunSummary(failures []qualityFailureSummary, recovered int) qualityRunSummary {
	summary := qualityRunSummary{
		AttemptedFailures: len(failures) + recovered,
		RecoveredURLs:     recovered,
		FinalFailedURLs:   len(failures),
		ByVendor:          map[string]int{},
		ByProductID:       map[string]int{},
		Failures:          append([]qualityFailureSummary(nil), failures...),
	}
	for _, failure := range failures {
		if failure.Vendor != "" {
			summary.ByVendor[failure.Vendor]++
		}
		if failure.ProductID != "" {
			summary.ByProductID[failure.ProductID]++
		}
	}
	return summary
}

func emitQualityRunSummary(outputDir string, summary qualityRunSummary, reporter Reporter) (string, string) {
	jsonPath, xlsxPath := "", ""
	if summary.FinalFailedURLs > 0 {
		jsonPath, xlsxPath = writeFailedQualityArtifacts(outputDir, summary.Failures)
	}
	if reporter != nil {
		reporter(Event{
			"event":              "price_quality_summary",
			"stage":              "summary",
			"attempted_failures": summary.AttemptedFailures,
			"recovered_urls":     summary.RecoveredURLs,
			"final_failed_urls":  summary.FinalFailedURLs,
			"by_vendor":          summary.ByVendor,
			"by_product_id":      summary.ByProductID,
			"failed_json_path":   jsonPath,
			"failed_xlsx_path":   xlsxPath,
		})
	}
	return jsonPath, xlsxPath
}

func writeFailedQualityArtifacts(outputDir string, failures []qualityFailureSummary) (string, string) {
	dir := filepath.Join(expandPath(outputDir), "_diagnostics")
	if dir == "" || len(failures) == 0 {
		return "", ""
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", ""
	}
	jsonPath := filepath.Join(dir, "failed_quality_urls.json")
	raw, err := json.MarshalIndent(failures, "", "  ")
	if err == nil {
		_ = os.WriteFile(jsonPath, raw, 0o644)
	}
	return absPath(jsonPath), ""
}

func qualityFailureSummaryFromEvent(event Event) qualityFailureSummary {
	failure := qualityFailureSummary{
		Vendor:             eventString(event, "vendor"),
		URL:                eventString(event, "url"),
		ProductID:          eventString(event, "product_id"),
		Reason:             eventString(event, "reason"),
		TrustedRows:        eventInt(event, "trusted_rows"),
		UntrustedRows:      eventInt(event, "untrusted_rows"),
		ResourceMissRows:   eventInt(event, "resource_miss_rows"),
		TotalRows:          eventInt(event, "total_rows"),
		RetryAttempt:       eventInt(event, "retry_attempt"),
		TopFallbackPrices:  eventStringSlice(event, "top_fallback_prices"),
		SelectedTexts:      eventStringSlice(event, "selected_texts"),
		NextDataPresent:    eventBool(event, "next_data_present"),
		PackageCount:       eventInt(event, "package_count"),
		ResourceCount:      eventInt(event, "resource_count"),
		BatchResponseCount: eventInt(event, "batch_response_count"),
		LastResponseStatus: eventInt(event, "last_response_status"),
	}
	failure.RetryCommand = fmt.Sprintf("trip-score-cdp --run-mode mode1 --no-headless --delay-profile safe --price-quality-retries 2 --input %q", failure.URL)
	return failure
}

func eventString(event Event, key string) string {
	if event == nil {
		return ""
	}
	raw, ok := event[key]
	if !ok || raw == nil {
		return ""
	}
	switch value := raw.(type) {
	case string:
		return value
	case fmt.Stringer:
		return value.String()
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func eventInt(event Event, key string) int {
	if event == nil {
		return 0
	}
	switch value := event[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		n, _ := value.Int64()
		return int(n)
	default:
		return 0
	}
}

func eventBool(event Event, key string) bool {
	if event == nil {
		return false
	}
	switch value := event[key].(type) {
	case bool:
		return value
	case string:
		return value == "true"
	default:
		return false
	}
}

func eventStringSlice(event Event, key string) []string {
	if event == nil {
		return nil
	}
	switch value := event[key].(type) {
	case []string:
		return append([]string(nil), value...)
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			text := strings.TrimSpace(fmt.Sprint(item))
			if text != "" {
				out = append(out, text)
			}
		}
		return out
	case string:
		if strings.TrimSpace(value) == "" {
			return nil
		}
		return []string{value}
	default:
		return nil
	}
}
