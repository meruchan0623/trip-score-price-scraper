package tripcdp

import (
	"strings"
	"testing"
)

func TestBaseRowFromGUIBundle(t *testing.T) {
	item := ScrapeItem{URL: "https://hk.trip.com/things-to-do/detail/92237157/", Vendor: "vendor"}
	row := baseRowFromBundle(item, "product", "", baseRatingBundle{
		BookingCount:   "123,150次預訂",
		ReviewCount:    "(5,674則評價)",
		Score:          "4.6",
		Customer:       "4.6",
		Convenience:    "4.7",
		Signal:         "4.5",
		BadReviewCount: "中性評價及負面評價 (601)",
		ProductInfo:    "• 適用地區：中國內地 • 電訊商：China Unicom",
	}, true)
	assertRowValue(t, row, "预定数", "123150")
	assertRowValue(t, row, "评价数", "5674")
	assertRowValue(t, row, "评分", "4.6")
	assertRowValue(t, row, "客户服务", "4.6")
	assertRowValue(t, row, "方便程度", "4.7")
	assertRowValue(t, row, "讯号状态", "4.5")
	assertRowValue(t, row, "中差评价数", "601")
	if !strings.Contains(row["商品资讯"], "China Unicom") {
		t.Fatalf("商品资讯 = %q, want DOM product info", row["商品资讯"])
	}
}

func TestBaseRowClearsReviewDimensionFieldsWhenScoreMissing(t *testing.T) {
	item := ScrapeItem{URL: "https://hk.trip.com/things-to-do/detail/92237157/"}
	text := "客戶服務 4.0 方便程度 4.0 訊號狀態 4.0 中性評價及負面評價 (601)"
	row := baseRowFromBundle(item, "product", text, baseRatingBundle{}, true)
	for _, key := range []string{"评分", "客户服务", "方便程度", "讯号状态", "中差评价数"} {
		if row[key] != "" {
			t.Fatalf("%s = %q, want empty when score is missing", key, row[key])
		}
	}
}

func TestBaseRowFallbackLabelsSupportTraditionalSimplifiedAndEnglish(t *testing.T) {
	item := ScrapeItem{URL: "https://hk.trip.com/things-to-do/detail/92237157/"}
	text := "Customer Service 4.1\n方便程度 4.2\n訊號狀態 4.3\n中性評價及負面評價 (12)"
	row := baseRowFromBundle(item, "product", text, baseRatingBundle{Score: "4.6"}, false)
	assertRowValue(t, row, "客户服务", "4.1")
	assertRowValue(t, row, "方便程度", "4.2")
	assertRowValue(t, row, "讯号状态", "4.3")
	assertRowValue(t, row, "中差评价数", "12")
	if _, ok := row["商品资讯"]; ok {
		t.Fatal("商品资讯 should not be present when product info extraction is disabled")
	}
}

func TestBaseRatingStableSelectionPrefersCompleteSample(t *testing.T) {
	samples := []baseRatingBundle{
		normalizeBaseRatingBundle(baseRatingBundle{Score: "4.6"}),
		normalizeBaseRatingBundle(baseRatingBundle{Score: "4.6", Customer: "4.6", Convenience: "4.7", Signal: "4.5", BadReviewCount: "601"}),
	}
	selected := selectBaseRatingBundle(samples)
	if selected.Customer != "4.6" || selected.BadReviewCount != "601" {
		t.Fatalf("selected incomplete bundle: %+v", selected)
	}
}

func TestBaseRatingJSContainsGUISelectors(t *testing.T) {
	for _, want := range []string{
		`span[testid="productdetail-productInfo-commentScore"]`,
		`span[testid="productdetail-productInfo-commentCountDesc"]`,
		`span[testid="saleInfoId"]`,
		`ct-review-list-subitem-title`,
		`ct-review-list-filiter-label-item`,
		`activity_product_detail_html_box`,
		`#group_1001`,
	} {
		if !strings.Contains(baseRatingBundleJS, want) {
			t.Fatalf("baseRatingBundleJS missing %q", want)
		}
	}
}

func assertRowValue(t *testing.T, row map[string]string, key string, want string) {
	t.Helper()
	if row[key] != want {
		t.Fatalf("%s = %q, want %q", key, row[key], want)
	}
}
