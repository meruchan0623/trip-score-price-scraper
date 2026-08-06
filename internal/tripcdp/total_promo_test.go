package tripcdp

import (
	"strings"
	"testing"
)

func TestJoinTotalPromoTagsDeduplicatesInDisplayOrder(t *testing.T) {
	got := joinTotalPromoTags([]string{"限時優惠", "即減50%", "限時優惠", " "})
	if got != "限時優惠 | 即減50%" {
		t.Fatalf("total promo = %q", got)
	}
}

func TestTotalPromoSelectorsUseStableTripTestIDs(t *testing.T) {
	for _, marker := range []string{"act-promotionText", "act-discountTextOtherDesc"} {
		if !strings.Contains(totalPromoTextJS, marker) {
			t.Fatalf("total promo extractor missing %q", marker)
		}
	}
}

func TestTotalPromoContentsUseProductSaleDiscountPreferenceDescription(t *testing.T) {
	for _, marker := range []string{"saleDiscountInfo", "preferences", "tagDesc"} {
		if !strings.Contains(totalPromoContentJS, marker) {
			t.Fatalf("total promo content extractor missing %q", marker)
		}
	}
	if strings.Contains(totalPromoContentJS, "tagContent") {
		t.Fatal("total promo content extractor must use tagDesc, not tagContent")
	}
	if !strings.Contains(promotionContentCaptureJS, "/restapi/soa2/21052/getProductInfo") {
		t.Fatal("promotion content capture must observe getProductInfo")
	}
}

func TestClearRepeatedProductFieldsClearsTotalPromoOnSKUContinuation(t *testing.T) {
	row := map[string]string{
		"商品ID":  "45892282",
		"商品资讯":  "Europe eSIM",
		"总促销标签": "50% off selected option(s) when you book between Jul 1, 2026 and Dec 31, 2026",
	}
	clearRepeatedProductFields(row, true)
	for _, field := range []string{"商品ID", "商品资讯", "总促销标签"} {
		if row[field] != "" {
			t.Fatalf("continuation row %s = %q, want blank", field, row[field])
		}
	}
}
