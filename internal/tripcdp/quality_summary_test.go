package tripcdp

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestQualityRunSummaryWritesFailedArtifacts(t *testing.T) {
	dir := t.TempDir()
	failures := []qualityFailureSummary{{
		Vendor:             "TGT",
		URL:                "https://hk.trip.com/things-to-do/detail/45861197/?curr=HKD",
		ProductID:          "45861197",
		Reason:             "consecutive_resource_miss",
		TrustedRows:        0,
		UntrustedRows:      5,
		ResourceMissRows:   5,
		TotalRows:          5,
		RetryAttempt:       2,
		TopFallbackPrices:  []string{"33:5"},
		SelectedTexts:      []string{"1天 | 每日-100GB"},
		NextDataPresent:    true,
		PackageCount:       14,
		ResourceCount:      112,
		BatchResponseCount: 5,
		LastResponseStatus: 200,
	}}
	failures[0].RetryCommand = "trip-score-cdp --input https://hk.trip.com/things-to-do/detail/45861197/?curr=HKD"

	summary := newQualityRunSummary(failures, 3)
	if summary.AttemptedFailures != 4 || summary.RecoveredURLs != 3 || summary.FinalFailedURLs != 1 {
		t.Fatalf("unexpected summary counts: %+v", summary)
	}
	if summary.ByVendor["TGT"] != 1 || summary.ByProductID["45861197"] != 1 {
		t.Fatalf("unexpected summary maps: %+v", summary)
	}

	jsonPath, xlsxPath := emitQualityRunSummary(dir, summary, nil)
	if jsonPath == "" || xlsxPath == "" {
		t.Fatalf("expected artifact paths, got json=%q xlsx=%q", jsonPath, xlsxPath)
	}
	raw, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []qualityFailureSummary
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 1 || decoded[0].ProductID != "45861197" || !strings.Contains(decoded[0].RetryCommand, "trip-score-cdp") {
		t.Fatalf("unexpected decoded failures: %+v", decoded)
	}
	if _, err := os.Stat(xlsxPath); err != nil {
		t.Fatal(err)
	}
}

func TestQualityFailureSummaryFromEventBuildsRetryCommand(t *testing.T) {
	failure := qualityFailureSummaryFromEvent(Event{
		"vendor":               "TGT",
		"url":                  "https://hk.trip.com/things-to-do/detail/45861197/?curr=HKD",
		"product_id":           "45861197",
		"reason":               "consecutive_resource_miss",
		"trusted_rows":         0,
		"untrusted_rows":       5,
		"resource_miss_rows":   5,
		"total_rows":           5,
		"retry_attempt":        2,
		"top_fallback_prices":  []string{"33:5"},
		"selected_texts":       []string{"1天 | 每日-100GB"},
		"next_data_present":    true,
		"package_count":        14,
		"resource_count":       112,
		"batch_response_count": 5,
		"last_response_status": 200,
	})
	if failure.Vendor != "TGT" || failure.ProductID != "45861197" || failure.RetryAttempt != 2 {
		t.Fatalf("unexpected failure summary: %+v", failure)
	}
	if !strings.Contains(failure.RetryCommand, "--price-quality-retries 2") || !strings.Contains(failure.RetryCommand, "45861197") {
		t.Fatalf("retry command missing expected fields: %s", failure.RetryCommand)
	}
}
