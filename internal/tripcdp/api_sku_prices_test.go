package tripcdp

import "testing"

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

func TestParseCapturedAPISKUPricesReportsTreeBoundaryWithoutInventingRows(t *testing.T) {
	responses := []capturedAPIResponse{
		{URL: "/restapi/soa2/21052/getProductInfo", Status: 200, Text: `{"productInfos":[{"basicInfo":{"packages":[{"packageId":11,"salesProperties":[{"saleProperty":{"id":208,"name":"Days"},"salePropertyValues":[{"id":1,"name":"1 day"}]}],"resourceIds":[101]},{"packageId":12,"salesProperties":[{"saleProperty":{"id":208,"name":"Days"},"salePropertyValues":[{"id":2,"name":"2 days"}]}],"resourceIds":[102]}]}}]}`},
		{URL: "/restapi/soa2/14580/getProductPriceCalendar", Status: 200, Text: `{"data":{"packageInfos":[{"packageId":11,"salesProperties":[{"salesProperty":{"salesPropertyId":208,"salesPropertyName":"Days"},"salesPropertyValues":[{"salesPropertyId":1,"salesPropertyName":"1 day"}]}],"resourceInfos":[{"resourceId":101,"stockStatus":1}],"priceInfo":{"originalPrice":10,"price":8}}]}}`},
	}

	rows, coverage := parseCapturedAPISKUPrices(responses, "SGD", true)
	if len(rows) != 1 || coverage.TreeCount != 2 || coverage.PricedCount != 1 || coverage.MissingPriceCount != 1 || coverage.Complete {
		t.Fatalf("rows=%d coverage=%+v", len(rows), coverage)
	}
	if rows[0]["combination"] != "1 day" {
		t.Fatalf("unexpected row: %+v", rows[0])
	}
	if got := capturedAPISKULeafCount(responses); got != 2 {
		t.Fatalf("captured API SKU leaf count = %d, want 2", got)
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
	if len(rows) != 0 || coverage.TreeCount != 1 || coverage.PricedCount != 0 || coverage.Complete {
		t.Fatalf("rows=%d coverage=%+v", len(rows), coverage)
	}
}

func TestAPISKUPriceCoverageUsableForHighCoverageBatch(t *testing.T) {
	coverage := apiSKUPriceCoverage{TreeCount: 216, PricedCount: 197, MissingPriceCount: 19}
	if !coverage.usable() {
		t.Fatalf("high coverage batch should be usable: %+v", coverage)
	}
}

func TestAPISKUPriceCoverageRejectsLowCoverageBatch(t *testing.T) {
	coverage := apiSKUPriceCoverage{TreeCount: 2, PricedCount: 1, MissingPriceCount: 1}
	if coverage.usable() {
		t.Fatalf("low coverage batch should fall back: %+v", coverage)
	}
}
