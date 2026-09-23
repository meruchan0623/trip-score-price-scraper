package tripcdp

import (
	"os"
	"strings"
	"testing"

	"github.com/go-rod/rod/lib/proto"
)

// Regression: the September 2026 headed HK runs had visible SKU controls but
// batch_response_count=0. The promotion request is made during initial page
// hydration, before SKU traversal installs its former hook, so it must be part
// of the script registered with EvalOnNewDocument.
func TestPromotionContentCaptureIncludesBatchPromotionBeforeNavigation(t *testing.T) {
	for _, want := range []string{
		"__tripCdpBatchPromotionResponses",
		"/restapi/soa2/31644/batchSearchBookingSalesPromotion",
	} {
		if !strings.Contains(promotionContentCaptureJS, want) {
			t.Fatalf("pre-navigation capture script missing %q", want)
		}
	}
}

// The headed 46011732 canary proved that the page-world fetch/XHR hook can be
// empty even while the visible SKU UI is populated. Keep a CDP-session
// diagnostic available so a worker/renamed endpoint cannot silently look like
// a missing price response again.
func TestNavigationMonitorRecordsBoundedSanitizedTripAPIResponses(t *testing.T) {
	monitor := &navigationMonitor{}
	monitor.recordAPIResponse("https://hk.trip.com/restapi/soa2/99999/newPriceEndpoint?token=secret", 200, proto.NetworkResourceTypeFetch)
	monitor.recordAPIResponse("https://example.test/restapi/soa2/99999/nope", 200, proto.NetworkResourceTypeFetch)
	got := monitor.apiSnapshot()
	if len(got) != 1 {
		t.Fatalf("observations = %#v, want one Trip API response", got)
	}
	if got[0].URL != "https://hk.trip.com/restapi/soa2/99999/newPriceEndpoint" || got[0].Status != 200 || got[0].Type != string(proto.NetworkResourceTypeFetch) {
		t.Fatalf("observation = %#v", got[0])
	}
	for i := 0; i < maxObservedTripAPIResponses+3; i++ {
		monitor.recordAPIResponse("https://hk.trip.com/restapi/soa2/1/a", 204, proto.NetworkResourceTypeXHR)
	}
	if got = monitor.apiSnapshot(); len(got) != maxObservedTripAPIResponses {
		t.Fatalf("bounded observations = %d, want %d", len(got), maxObservedTripAPIResponses)
	}
}

func TestProductPackageDiagnosticsRetainsBrowserWideEvidenceAfterMonitorCloses(t *testing.T) {
	previous := productPackageDiagnostics{ObservedAPIResponses: []networkResponseObservation{{URL: "https://hk.trip.com/restapi/soa2/14580/getProductPriceCalendar", Status: 200, Type: "Fetch"}}}
	latest := productPackageDiagnostics{NextDataPresent: true}
	merged := mergeProductPackageDiagnostics(previous, latest)
	if len(merged.ObservedAPIResponses) != 1 || merged.ObservedAPIResponses[0].Status != 200 {
		t.Fatalf("browser-wide evidence was lost: %#v", merged)
	}
}

func TestCapturedAPIResponsesMergeTrustedCDPBodiesWhenPageCaptureIsEmpty(t *testing.T) {
	cdp := &navigationMonitor{}
	cdp.recordCapturedAPIResponse("https://hk.trip.com/restapi/soa2/21052/getProductInfo?token=secret", 200, `{"productInfos":[{"basicInfo":{"packages":[{"packageId":11,"resourceIds":[101]}]}}]}`)
	cdp.recordCapturedAPIResponse("https://hk.trip.com/restapi/soa2/14580/getProductPriceCalendar", 200, `{"data":{"packageInfos":[{"packageId":11,"resourceInfos":[{"resourceId":101}],"priceInfo":{"originalPrice":10,"price":8}}]}}`)

	responses := mergeCapturedAPIResponses(nil, cdp.capturedAPISnapshot())
	rows, coverage := parseCapturedAPISKUPrices(responses, "HKD", true)
	if !coverage.usable() || len(rows) != 1 || rows[0]["real_price"] != "8" {
		t.Fatalf("CDP body merge produced rows=%#v coverage=%#v", rows, coverage)
	}
	for _, response := range responses {
		if strings.Contains(response.URL, "?") || strings.Contains(response.Text, "secret") {
			t.Fatalf("captured response was not sanitized: %#v", response)
		}
	}
}

func TestNavigationMonitorReportsBoundedSanitizationRejectionWithoutBody(t *testing.T) {
	monitor := &navigationMonitor{}
	monitor.recordCapturedAPIResponse("https://hk.trip.com/restapi/soa2/21052/getProductInfo", 200, "not-json")
	for i := 0; i < maxMonitorDiagnosticErrors+2; i++ {
		monitor.recordCapturedAPIResponse("https://hk.trip.com/restapi/soa2/14580/getProductPriceCalendar", 200, "")
	}
	diagnostics := monitor.diagnosticsSnapshot()
	if diagnostics.SanitizeRejectCount != maxMonitorDiagnosticErrors+3 {
		t.Fatalf("sanitize rejection count = %d", diagnostics.SanitizeRejectCount)
	}
	if len(diagnostics.SanitizeRejects) != maxMonitorDiagnosticErrors || diagnostics.SanitizeRejects[0] != "invalid_json" {
		t.Fatalf("bounded rejection reasons = %#v", diagnostics.SanitizeRejects)
	}
}

func TestCapturedAPISchemaFingerprintsContainOnlyBoundedStructure(t *testing.T) {
	fingerprints := capturedAPISchemaFingerprints([]capturedAPIResponse{{
		URL:    "https://hk.trip.com/restapi/soa2/14580/getProductPriceCalendar?token=secret",
		Status: 200,
		Text:   `{"data":{"packageInfos":[{"packageId":11,"resourceInfos":[{"resourceId":101}],"priceInfo":{"price":8}}]}}`,
	}})
	if len(fingerprints) != 1 || fingerprints[0].EndpointPath != "/restapi/soa2/14580/getProductPriceCalendar" || fingerprints[0].BodyBytes == 0 {
		t.Fatalf("fingerprints = %#v", fingerprints)
	}
	for _, path := range fingerprints[0].KeyPaths {
		if strings.Contains(path.Path, "11") || strings.Contains(path.Path, "101") || strings.Contains(path.Path, "8") {
			t.Fatalf("schema fingerprint leaked scalar content: %#v", path)
		}
	}
}

func TestProductPackageDiagnosticsRetainsMonitorCounters(t *testing.T) {
	previous := productPackageDiagnostics{TrustedResponseCount: 2, TrustedFinishedCount: 2, GetBodyErrorCount: 2, GetBodyErrors: []string{"No resource with given identifier found"}}
	merged := mergeProductPackageDiagnostics(previous, productPackageDiagnostics{NextDataPresent: true})
	if merged.TrustedResponseCount != 2 || merged.GetBodyErrorCount != 2 || len(merged.GetBodyErrors) != 1 {
		t.Fatalf("monitor diagnostics were lost: %#v", merged)
	}
}

func TestParseCapturedAPISKUPricesJoinsTreeAndCurrentPrices(t *testing.T) {
	responses := []capturedAPIResponse{
		{URL: "https://hk.trip.com/restapi/soa2/21052/getProductInfo", Status: 200, Text: `{
			"productInfos":[{"basicInfo":{"packages":[
				{"packageId":11,"salesProperties":[
					{"saleProperty":{"id":208,"name":"套餐天数"},"salePropertyValues":[{"id":1920,"name":"1天"}]},
					{"saleProperty":{"id":1310,"name":"套餐内容"},"salePropertyValues":[{"id":2153383,"name":"每日-0.5GB"}]}
				],"resourceIds":[101]},
				{"packageId":12,"salesProperties":[
					{"saleProperty":{"id":208,"name":"套餐天数"},"salePropertyValues":[{"id":1921,"name":"2天"}]},
					{"saleProperty":{"id":1310,"name":"套餐内容"},"salePropertyValues":[{"id":2153373,"name":"每日-1GB"}]}
				],"resourceIds":[102]}
			]}}]}`},
		{URL: "https://hk.trip.com/restapi/soa2/14580/getProductPriceCalendar", Status: 200, Text: `{
			"data":{"packageInfos":[
				{"packageId":11,"salesProperties":[
					{"salesProperty":{"salesPropertyId":208,"salesPropertyName":"套餐天数"},"salesPropertyValues":[{"salesPropertyId":1920,"salesPropertyName":"1天"}]},
					{"salesProperty":{"salesPropertyId":1310,"salesPropertyName":"套餐内容"},"salesPropertyValues":[{"salesPropertyId":2153383,"salesPropertyName":"每日-0.5GB"}]}
				],"resourceInfos":[{"resourceId":101,"stockStatus":1}],"priceInfo":{"originalPrice":1.38,"price":0.69,"preferentialPriceDetails":[{"preferentialTitle":"限时优惠","preferentialName":"减50%"}]}},
				{"packageId":12,"salesProperties":[
					{"salesProperty":{"salesPropertyId":208,"salesPropertyName":"套餐天数"},"salesPropertyValues":[{"salesPropertyId":1921,"salesPropertyName":"2天"}]},
					{"salesProperty":{"salesPropertyId":1310,"salesPropertyName":"套餐内容"},"salesPropertyValues":[{"salesPropertyId":2153373,"salesPropertyName":"每日-1GB"}]}
				],"resourceInfos":[{"resourceId":102,"stockStatus":1}],"priceInfo":{"originalPrice":4.12,"price":3.79,"preferentialPriceDetails":[{"preferentialTitle":"限时优惠","preferentialName":"减8%"}]}}
			]}}`},
	}

	rows, coverage := parseCapturedAPISKUPrices(responses, "HKD", true)
	if coverage.TreeCount != 2 || coverage.PricedCount != 2 || coverage.MissingPriceCount != 0 || !coverage.Complete {
		t.Fatalf("coverage = %+v", coverage)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	first := rows[0]
	for key, want := range map[string]string{
		"combination":  "1天 | 每日-0.5GB",
		"currency":     "HKD",
		"price":        "1.38",
		"real_price":   "0.69",
		"promo_tag":    "限时优惠 减50%",
		"package_id":   "11",
		"resource_id":  "101",
		"price_status": priceStatusTrustedPromo,
		"price_source": priceSourceProductPriceCalendar,
	} {
		if got := first[key]; got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestParseCapturedAPISKUPricesUsesCalendarAvailabilityBoundary(t *testing.T) {
	responses := []capturedAPIResponse{
		{URL: "/restapi/soa2/21052/getProductInfo", Status: 200, Text: `{"productInfos":[{"basicInfo":{"packages":[{"packageId":11,"salesProperties":[{"saleProperty":{"id":208,"name":"Days"},"salePropertyValues":[{"id":1,"name":"1 day"}]}],"resourceIds":[101]},{"packageId":12,"salesProperties":[{"saleProperty":{"id":208,"name":"Days"},"salePropertyValues":[{"id":2,"name":"2 days"}]}],"resourceIds":[102]}]}}]}`},
		{URL: "/restapi/soa2/14580/getProductPriceCalendar", Status: 200, Text: `{"data":{"packageInfos":[{"packageId":11,"salesProperties":[{"salesProperty":{"salesPropertyId":208,"salesPropertyName":"Days"},"salesPropertyValues":[{"salesPropertyId":1,"salesPropertyName":"1 day"}]}],"resourceInfos":[{"resourceId":101,"stockStatus":1}],"priceInfo":{"originalPrice":10,"price":8}}]}}`},
	}

	rows, coverage := parseCapturedAPISKUPrices(responses, "SGD", true)
	if len(rows) != 1 || coverage.TreeCount != 1 || coverage.PricedCount != 1 || coverage.MissingPriceCount != 0 || !coverage.Complete {
		t.Fatalf("rows=%d coverage=%+v", len(rows), coverage)
	}
	if rows[0]["combination"] != "1 day" {
		t.Fatalf("unexpected row: %+v", rows[0])
	}
	if got := capturedAPISKULeafCount(responses); got != 2 {
		t.Fatalf("captured API SKU leaf count = %d, want 2", got)
	}
	if coverage.ProductInfoLeafCount != 2 || coverage.CalendarLeafCount != 1 || coverage.ExactKeyOverlapCount != 1 || coverage.PricedCalendarCount != 1 || coverage.ZeroOriginalOrRealCount != 0 || coverage.CurrencyMismatchCount != 0 {
		t.Fatalf("aggregate diagnostics = %+v", coverage)
	}
}

func TestParseCapturedAPISKUPricesFailsClosedForUnpricedCalendarLeaf(t *testing.T) {
	responses := []capturedAPIResponse{
		{URL: "/restapi/soa2/21052/getProductInfo", Status: 200, Text: `{"productInfos":[{"basicInfo":{"packages":[{"packageId":11,"resourceIds":[101]}]}}]}`},
		{URL: "/restapi/soa2/14580/getProductPriceCalendar", Status: 200, Text: `{"data":{"packageInfos":[{"packageId":11,"resourceInfos":[{"resourceId":101}],"priceInfo":{"originalPrice":10,"price":8}},{"packageId":12,"resourceInfos":[{"resourceId":102}],"priceInfo":{"originalPrice":0,"price":0}}]}}`},
	}

	rows, coverage := parseCapturedAPISKUPrices(responses, "HKD", true)
	if len(rows) != 1 || coverage.TreeCount != 2 || coverage.PricedCount != 1 || coverage.MissingPriceCount != 1 || coverage.Complete || coverage.usable() || coverage.ZeroOriginalOrRealCount != 1 {
		t.Fatalf("rows=%d coverage=%+v", len(rows), coverage)
	}
}

func TestCapturedAPISKULeafCountIgnoresUnsuccessfulOrNonProductResponses(t *testing.T) {
	responses := []capturedAPIResponse{
		{URL: "/restapi/soa2/21052/getProductInfo", Status: 403, Text: `{"productInfos":[{"basicInfo":{"packages":[{"packageId":11,"resourceIds":[101]}]}}]}`},
		{URL: "/restapi/soa2/14580/getProductPriceCalendar", Status: 200, Text: `{"data":{"packageInfos":[{"packageId":12}]}}`},
	}
	if got := capturedAPISKULeafCount(responses); got != 0 {
		t.Fatalf("captured API SKU leaf count = %d, want 0", got)
	}
}

func TestCapturedAPISKUPricesUseExpectedCurrencyWhenResponseOmitsIt(t *testing.T) {
	responses := []capturedAPIResponse{
		{URL: "/restapi/soa2/21052/getProductInfo", Status: 200, Text: `{"productInfos":[{"basicInfo":{"packages":[{"packageId":11,"salesProperties":[],"resourceIds":[101]}]}}]}`},
		{URL: "/restapi/soa2/14580/getProductPriceCalendar", Status: 200, Text: `{"data":{"packageInfos":[{"packageId":11,"resourceInfos":[{"resourceId":101}],"priceInfo":{"originalPrice":2500,"price":2200}}]}}`},
	}

	rows, coverage := parseCapturedAPISKUPrices(responses, "JPY", true)
	if coverage.TreeCount != 1 || coverage.PricedCount != 1 || len(rows) != 1 {
		t.Fatalf("rows=%d coverage=%+v", len(rows), coverage)
	}
	if rows[0]["currency"] != "JPY" || rows[0]["real_currency"] != "JPY" {
		t.Fatalf("currencies = %q/%q, want JPY/JPY", rows[0]["currency"], rows[0]["real_currency"])
	}
}

func TestCapturedAPISKUPricesRejectsExplicitUnexpectedCurrency(t *testing.T) {
	responses := []capturedAPIResponse{
		{URL: "/restapi/soa2/21052/getProductInfo", Status: 200, Text: `{"productInfos":[{"basicInfo":{"packages":[{"packageId":11,"salesProperties":[],"resourceIds":[101]}]}}]}`},
		{URL: "/restapi/soa2/14580/getProductPriceCalendar", Status: 200, Text: `{"data":{"packageInfos":[{"packageId":11,"resourceInfos":[{"resourceId":101}],"priceInfo":{"currency":"USD","originalPrice":2500,"price":2200}}]}}`},
	}

	rows, coverage := parseCapturedAPISKUPrices(responses, "JPY", true)
	if len(rows) != 0 {
		t.Fatalf("explicit USD response must not produce a JPY row: %+v", rows)
	}
	if coverage.TreeCount != 1 || coverage.PricedCount != 0 || coverage.MissingPriceCount != 1 || coverage.Complete {
		t.Fatalf("coverage = %+v, want one missing JPY price", coverage)
	}
}

func TestParseCapturedAPISKUPricesKeepsResourcesDistinctWithinPackage(t *testing.T) {
	responses := []capturedAPIResponse{
		{URL: "/restapi/soa2/21052/getProductInfo", Status: 200, Text: `{"productInfos":[{"basicInfo":{"packages":[{"packageId":11,"salesProperties":[{"saleProperty":{"id":208,"name":"Days"},"salePropertyValues":[{"id":1,"name":"1 day"}]}],"resourceIds":[101,102]}]}}]}`},
		{URL: "/restapi/soa2/14580/getProductPriceCalendar", Status: 200, Text: `{"data":{"packageInfos":[
			{"packageId":11,"resourceInfos":[{"resourceId":101}],"priceInfo":{"originalPrice":10,"price":8}},
			{"packageId":11,"resourceInfos":[{"resourceId":102}],"priceInfo":{"originalPrice":12,"price":9}}
		]}}`},
	}

	rows, coverage := parseCapturedAPISKUPrices(responses, "HKD", true)
	if coverage.TreeCount != 2 || coverage.PricedCount != 2 || coverage.MissingPriceCount != 0 || !coverage.Complete {
		t.Fatalf("coverage = %+v", coverage)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0]["resource_id"] != "101" || rows[0]["real_price"] != "8" {
		t.Fatalf("first row = %+v", rows[0])
	}
	if rows[1]["resource_id"] != "102" || rows[1]["real_price"] != "9" {
		t.Fatalf("second row = %+v", rows[1])
	}
}

func TestParseCapturedAPISKUPricesRejectsFailedOrUntrustedPrices(t *testing.T) {
	responses := []capturedAPIResponse{
		{URL: "/restapi/soa2/21052/getProductInfo", Status: 200, Text: `{"productInfos":[{"basicInfo":{"packages":[{"packageId":11,"salesProperties":[],"resourceIds":[101]}]}}]}`},
		{URL: "/restapi/soa2/14580/getProductPriceCalendar", Status: 403, Text: `{"data":{"packageInfos":[{"packageId":11,"priceInfo":{"originalPrice":10,"price":8}}]}}`},
	}

	rows, coverage := parseCapturedAPISKUPrices(responses, "HKD", true)
	if len(rows) != 0 || coverage.TreeCount != 0 || coverage.PricedCount != 0 || coverage.Complete {
		t.Fatalf("rows=%d coverage=%+v", len(rows), coverage)
	}
}

func TestAPISKUPriceCoverageUsableForHighCoverageBatch(t *testing.T) {
	coverage := apiSKUPriceCoverage{TreeCount: 216, PricedCount: 197, MissingPriceCount: 19}
	if coverage.usable() {
		t.Fatalf("incomplete batch must not be accepted: %+v", coverage)
	}
}

func TestAPISKUPriceCoverageRejectsLowCoverageBatch(t *testing.T) {
	coverage := apiSKUPriceCoverage{TreeCount: 2, PricedCount: 1, MissingPriceCount: 1}
	if coverage.usable() {
		t.Fatalf("low coverage batch should fall back: %+v", coverage)
	}
}

func TestCalendarTreeSurvivesBlockedProductInfo(t *testing.T) {
	responses := []capturedAPIResponse{
		{URL: "/restapi/soa2/21052/getProductInfo", Status: 200, Text: "whaleguard block"},
		{URL: "/restapi/soa2/14580/getProductPriceCalendar", Status: 200, Text: `{"data":{"packageInfos":[{"packageId":11,"resourceInfos":[{"resourceId":101}],"salesProperties":[{"salesProperty":{"salesPropertyId":208,"salesPropertyName":"Days"},"salesPropertyValues":[{"salesPropertyId":1,"salesPropertyName":"1 day"}]}],"priceInfo":{"originalPrice":1.46,"price":0.73,"preferentialPriceDetails":[{"preferentialTitle":"Sale","preferentialName":"50% off"}]}},{"packageId":12,"resourceInfos":[{"resourceId":102}]}]}}`},
	}
	rows, coverage := parseCapturedAPISKUPrices(responses, "HKD", true)
	if len(rows) != 1 || coverage.TreeCount != 2 || coverage.MissingPriceCount != 1 || coverage.usable() {
		t.Fatalf("rows=%v coverage=%+v", rows, coverage)
	}
	if rows[0]["price"] != "1.46" || rows[0]["real_price"] != "0.73" || rows[0]["combination"] != "1 day" {
		t.Fatal(rows)
	}
	if capturedAPISKULeafCount(responses) != 2 {
		t.Fatal("calendar leaves lost")
	}
}

func TestLiveCalendar65892026CompletePrices(t *testing.T) {
	raw, err := os.ReadFile("testdata/calendar_65892026.json")
	if err != nil {
		t.Fatal(err)
	}
	rows, c := parseCapturedAPISKUPrices([]capturedAPIResponse{{URL: "/restapi/soa2/14580/getProductPriceCalendar", Status: 200, Text: string(raw)}}, "HKD", true)
	if len(rows) != 127 || !c.Complete || !c.usable() {
		t.Fatalf("rows=%d coverage=%+v", len(rows), c)
	}
	seen := map[string]bool{}
	for _, row := range rows {
		key := row["package_id"] + ":" + row["resource_id"]
		if seen[key] || row["combination"] == "" || row["promo_tag"] == "" || row["price"] == "" || row["real_price"] == "" {
			t.Fatal(row)
		}
		seen[key] = true
		if row["resource_id"] == "70334649" && (row["price"] != "1.46" || row["real_price"] != "0.73") {
			t.Fatal(row)
		}
		if row["resource_id"] == "96274729" && (row["price"] != "5.11" || row["real_price"] != "4.7") {
			t.Fatal(row)
		}
	}
}
