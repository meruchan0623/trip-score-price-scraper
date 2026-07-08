package tripcdp

import "testing"

func TestShouldRetryPriceQualityRejectsAPISuspected(t *testing.T) {
	s := newTripScraper(TaskRequest{
		PriceQualityRetry: PriceQualityRetryOptions{MaxRetries: 2},
		Runtime:           defaultRuntimeOptions(),
	}, nil, "BC", 1, 1)
	quality := &priceQualityTracker{
		failed:           true,
		failReason:       "resource_miss_rate",
		trustedRows:      0,
		untrustedRows:    12,
		resourceMissRows: 12,
		totalRows:        12,
		diagnostics:      productPackageDiagnostics{NextDataPresent: true, PackageCount: 4, ResourceCount: 4, BatchResponseCount: 12, LastResponseStatus: 200},
	}
	if s.shouldRetryPriceQuality(quality, 0, 0) {
		t.Fatal("api-block suspected quality failure must not be retried")
	}
}

func TestShouldRetryPriceQualityAllowsDataQualityLowWithinBudget(t *testing.T) {
	s := newTripScraper(TaskRequest{
		PriceQualityRetry: PriceQualityRetryOptions{MaxRetries: 2},
		Runtime:           defaultRuntimeOptions(),
	}, nil, "BC", 1, 1)
	quality := &priceQualityTracker{failed: true, failReason: "same_fallback_price_streak", trustedRows: 0, untrustedRows: 8, totalRows: 8}
	if !s.shouldRetryPriceQuality(quality, 0, 0) {
		t.Fatal("data quality low should be retried within budget")
	}
}
