package tripcdp

import "testing"

func TestPageSnapshotEmptyURLIsUnhealthy(t *testing.T) {
	snapshot := pageSnapshot{
		URL:            "",
		ReadyState:     "complete",
		BodyTextLength: 0,
	}
	if isProductPageReady(snapshot) {
		t.Fatal("empty URL and empty body should be unhealthy")
	}
}

func TestProductIDParsesExactDetailIdentifier(t *testing.T) {
	if got := productID("https://hk.trip.com/things-to-do/detail/65892026/?curr=HKD"); got != "65892026" {
		t.Fatalf("productID = %q, want 65892026", got)
	}
}

func TestPageSnapshotValidNoSKUAllowsFallback(t *testing.T) {
	snapshot := pageSnapshot{
		URL:             "https://hk.trip.com/things-to-do/detail/123/?locale=zh-hk&curr=HKD",
		ReadyState:      "complete",
		BodyTextLength:  2000,
		Title:           "Trip product",
		HasProductTitle: true,
		HasPrice:        true,
	}
	row := map[string]string{
		"商品名称": "Valid product",
		"评分":   "4.6",
	}
	if !isProductPageReady(snapshot) {
		t.Fatal("product title or price should make page ready")
	}
	if !isProductPageExtractable(snapshot, false) {
		t.Fatal("price should make full product page extractable")
	}
	if !allowNoSKUFallback(row, snapshot) {
		t.Fatal("valid no-SKU product page should allow fallback")
	}
}

func TestPageSnapshotScoreOnlyDoesNotAllowFullFallback(t *testing.T) {
	snapshot := pageSnapshot{
		URL:            "https://hk.trip.com/things-to-do/detail/123/?locale=zh-hk&curr=HKD",
		ReadyState:     "complete",
		BodyTextLength: 2000,
		Title:          "Trip product",
		HasScore:       true,
	}
	row := map[string]string{
		"商品名称": "Product shell",
		"评分":   "4.6",
	}
	if !isProductPageReady(snapshot) {
		t.Fatal("score can make page broadly ready")
	}
	if isProductPageExtractable(snapshot, false) {
		t.Fatal("full extraction should wait for SKU or price, not score alone")
	}
	if !isProductPageExtractable(snapshot, true) {
		t.Fatal("rating-only extraction can proceed with score")
	}
	if allowNoSKUFallback(row, snapshot) {
		t.Fatal("score-only page should not be exported as no-SKU fallback")
	}
}

func TestPageSnapshotInvalidNoSKUBlocksFallback(t *testing.T) {
	snapshot := pageSnapshot{
		URL:            "",
		ReadyState:     "",
		BodyTextLength: 0,
	}
	row := map[string]string{
		"商品名称": "",
		"评分":   "",
	}
	if allowNoSKUFallback(row, snapshot) {
		t.Fatal("invalid page should not allow no-SKU fallback")
	}
}

func TestMarketContextFailureBlocksPriceExtraction(t *testing.T) {
	for _, class := range []string{"market_context_mismatch", "market_context_unverified"} {
		snapshot := pageSnapshot{
			URL:                "https://sg.trip.com/things-to-do/detail/45892282/",
			ReadyState:         "complete",
			BodyTextLength:     2000,
			HasProductTitle:    true,
			HasPrice:           true,
			MarketContextClass: class,
		}
		if isProductPageExtractable(snapshot, false) {
			t.Errorf("%s accepted", class)
		}
	}
}

func TestSessionStorageOriginOnlyOnce(t *testing.T) {
	scraper := newTripScraper(TaskRequest{}, nil, defaultVendor, 1, 1)
	if !scraper.shouldApplySessionStorage("https://hk.trip.com/things-to-do/detail/1/?locale=zh-hk") {
		t.Fatal("first origin application should be allowed")
	}
	if scraper.shouldApplySessionStorage("https://hk.trip.com/things-to-do/detail/2/?locale=zh-hk") {
		t.Fatal("same origin should not apply storage twice")
	}
	if !scraper.shouldApplySessionStorage("https://sg.trip.com/things-to-do/detail/2/") {
		t.Fatal("different origin should be allowed")
	}
}

func TestRestartAfterURLMarkedWhenClickFailsOnUnhealthyPage(t *testing.T) {
	scraper := newTripScraper(TaskRequest{}, nil, defaultVendor, 1, 1)
	scraper.markRestartAfterURLIfUnhealthy("test restart")
	if !scraper.restartAfterURL {
		t.Fatal("unhealthy page should mark restart_after_url")
	}
	if scraper.restartReason != "test restart" {
		t.Fatalf("restart reason = %q", scraper.restartReason)
	}
}
