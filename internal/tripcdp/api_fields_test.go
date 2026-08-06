package tripcdp

import "testing"

func TestParseCapturedAPIFieldsUsesProductAndCommentResponses(t *testing.T) {
	fields := parseCapturedAPIFields([]capturedAPIResponse{
		{URL: "https://hk.trip.com/restapi/soa2/21052/getProductInfo", Status: 200, Text: `{"productInfos":[{"basicInfo":{"productName":"API title"},"addInfos":[{"code":"ProductIntroduction","contents":[{"value":"<p>API <strong>product</strong> info</p>"}]}]}]}`},
		{URL: "https://hk.trip.com/restapi/soa2/18084/commonQueryCommentSummary", Status: 200, Text: `{"commentAggregation":{"totalCount":2959,"scoreAvg":4.6,"commonTags":[{"tagName":"MIDBAD","totalCount":263}]}}`},
	})

	for key, want := range map[string]string{
		"ProductName":    "API title",
		"ProductInfo":    "API product info",
		"ReviewCount":    "2959",
		"Score":          "4.6",
		"BadReviewCount": "263",
	} {
		var got string
		switch key {
		case "ProductName":
			got = fields.ProductName
		case "ProductInfo":
			got = fields.ProductInfo
		case "ReviewCount":
			got = fields.ReviewCount
		case "Score":
			got = fields.Score
		case "BadReviewCount":
			got = fields.BadReviewCount
		}
		if got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestParseCapturedAPIFieldsUsesListCommentsSubItemScores(t *testing.T) {
	fields := parseCapturedAPIFields([]capturedAPIResponse{
		{URL: "https://www.trip.com/restapi/soa2/20047/listComments", Status: 200, Text: `{"commentAggregation":{"goodRate":0.911,"subItemTags":[{"tagName":"42","displayName":"客戶服務","scoreAvg":4.6,"totalCount":2939},{"tagName":"43","displayName":"方便程度","scoreAvg":4.7,"totalCount":2939},{"tagName":"44","displayName":"訊號狀態","scoreAvg":4.6,"totalCount":2939}]}}`},
	})

	for key, want := range map[string]string{
		"Customer":    "4.6",
		"Convenience": "4.7",
		"Signal":      "4.6",
		"GoodRate":    "91.1%",
	} {
		var got string
		switch key {
		case "Customer":
			got = fields.Customer
		case "Convenience":
			got = fields.Convenience
		case "Signal":
			got = fields.Signal
		case "GoodRate":
			got = fields.GoodRate
		}
		if got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestParseCapturedAPIFieldsIgnoresFailedResponses(t *testing.T) {
	fields := parseCapturedAPIFields([]capturedAPIResponse{
		{URL: "https://hk.trip.com/restapi/soa2/18084/commonQueryCommentSummary", Status: 403, Text: `{"commentAggregation":{"totalCount":2959,"scoreAvg":4.6,"commonTags":[{"tagName":"MIDBAD","totalCount":263}]}}`},
	})
	if fields.ReviewCount != "" || fields.Score != "" || fields.BadReviewCount != "" {
		t.Fatalf("failed response populated API fields: %+v", fields)
	}
}

func TestApplyCapturedAPIFieldsPrefersCompleteAPIValues(t *testing.T) {
	row := map[string]string{
		"商品名称":  "DOM title",
		"商品资讯":  "DOM product info",
		"评价数":   "12",
		"评分":    "4.1",
		"中差评价数": "3",
		"客户服务":  "4.1",
		"方便程度":  "4.2",
		"讯号状态":  "4.3",
		"好评率":   "80.0%",
	}
	fields := capturedAPIFields{
		ProductName:    "API title",
		ProductInfo:    "API product info",
		ReviewCount:    "2959",
		Score:          "4.6",
		BadReviewCount: "263",
		Customer:       "4.6",
		Convenience:    "4.7",
		Signal:         "4.6",
		GoodRate:       "91.1%",
	}

	applyCapturedAPIFields(row, fields, true)

	for key, want := range map[string]string{
		"商品名称":  "API title",
		"商品资讯":  "API product info",
		"评价数":   "2959",
		"评分":    "4.6",
		"中差评价数": "263",
		"客户服务":  "4.6",
		"方便程度":  "4.7",
		"讯号状态":  "4.6",
		"好评率":   "91.1%",
	} {
		if got := row[key]; got != want {
			t.Fatalf("%s = %q, want API value %q", key, got, want)
		}
	}
}

func TestApplyCapturedAPIFieldsKeepsDOMFallbackForMissingAPIValues(t *testing.T) {
	row := map[string]string{
		"商品名称": "DOM title",
		"商品资讯": "DOM product info",
		"预定数":  "55000",
		"客户服务": "4.6",
		"方便程度": "4.7",
		"讯号状态": "4.6",
	}

	applyCapturedAPIFields(row, capturedAPIFields{}, true)

	for key, want := range map[string]string{
		"商品名称": "DOM title",
		"商品资讯": "DOM product info",
		"预定数":  "55000",
		"客户服务": "4.6",
		"方便程度": "4.7",
		"讯号状态": "4.6",
	} {
		if got := row[key]; got != want {
			t.Fatalf("%s = %q, want DOM fallback %q", key, got, want)
		}
	}
}
