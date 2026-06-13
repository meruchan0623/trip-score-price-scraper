package tripcdp

import (
	"context"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
)

func TestLoadExcelItemsFiltersTripLinksAndPreservesVendorMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vendors.xlsx")
	file := excelize.NewFile()
	sheet := file.GetSheetName(file.GetActiveSheetIndex())
	_ = file.SetCellValue(sheet, "A1", "Vendor A")
	_ = file.SetCellValue(sheet, "B1", "Vendor B")
	_ = file.SetCellValue(sheet, "A2", "https://www.trip.com/detail/12345")
	_ = file.SetCellValue(sheet, "A3", "https://example.com/not-trip")
	_ = file.SetCellValue(sheet, "B2", "")
	if err := file.SaveAs(path); err != nil {
		t.Fatal(err)
	}

	items, warnings, err := loadExcelItems(path, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 Trip item, got %d", len(items))
	}
	if items[0].Vendor != "Vendor A" || *items[0].SourceColumn != 1 || *items[0].SourceRow != 2 || items[0].InputOrder != 7 {
		t.Fatalf("metadata mismatch: %+v", items[0])
	}
	if len(warnings) != 2 {
		t.Fatalf("expected non-Trip and empty-vendor warnings, got %v", warnings)
	}
}

func TestVendorGroupsSeparatesDuplicateVendorColumns(t *testing.T) {
	col1 := 1
	col2 := 2
	groups := vendorGroups([]ScrapeItem{
		{URL: "u2", Vendor: "Vendor", SourceColumn: &col2, InputOrder: 2},
		{URL: "u1", Vendor: "Vendor", SourceColumn: &col1, InputOrder: 1},
	})

	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	if groups[0].FileVendor != "Vendor" || groups[1].FileVendor != "Vendor_列2" {
		t.Fatalf("unexpected file vendor names: %+v", groups)
	}
}

func TestExportRowsFieldShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.xlsx")
	rows := []map[string]string{{
		"厂商":     "Vendor",
		"商品ID":   "12345",
		"URL":    "https://www.trip.com/detail/12345",
		"商品名称":   "Product",
		"SKU层级1": "1日",
		"SKU层级2": "4G",
		"货币":     "HKD",
		"外显价格":   "10",
		"实价":     "8",
		"促销标签":   "Save",
		"预定数":    "1",
		"评价数":    "2",
		"评分":     "4.8",
		"客户服务":   "4.7",
		"方便程度":   "4.6",
		"讯号状态":   "4.5",
		"中差评价数":  "0",
		"商品资讯":   "info",
	}}

	if _, err := exportRows(rows, path, false, true, true, true); err != nil {
		t.Fatal(err)
	}
	file, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	sheet := file.GetSheetName(file.GetActiveSheetIndex())
	header, err := file.GetRows(sheet)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"厂商", "商品ID", "URL", "商品名称", "SKU层级1", "SKU层级2", "货币", "外显价格", "实价", "促销标签", "商品资讯", "预定数", "评价数", "评分", "客户服务", "方便程度", "讯号状态", "中差评价数"}
	if len(header) == 0 || len(header[0]) != len(want) {
		t.Fatalf("unexpected header length: %v", header)
	}
	for i, value := range want {
		if header[0][i] != value {
			t.Fatalf("header[%d] = %q, want %q", i, header[0][i], value)
		}
	}
}

func TestExportRowsRatingOnlyProductInfoShapeMatchesLegacyGUI(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rating-only.xlsx")
	rows := []map[string]string{{
		"商品ID":  "12345",
		"URL":   "https://www.trip.com/detail/12345",
		"商品名称":  "Product",
		"商品资讯":  "Device info",
		"预定数":   "10",
		"评价数":   "2",
		"评分":    "4.8",
		"客户服务":  "4.7",
		"方便程度":  "4.6",
		"讯号状态":  "4.5",
		"中差评价数": "0",
	}}

	if _, err := exportRows(rows, path, true, false, true, false); err != nil {
		t.Fatal(err)
	}
	file, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	sheet := file.GetSheetName(file.GetActiveSheetIndex())
	gotRows, err := file.GetRows(sheet)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"商品ID", "URL", "商品名称", "商品资讯", "预定数", "评价数", "评分", "客户服务", "方便程度", "讯号状态", "中差评价数"}
	if !reflect.DeepEqual(gotRows[0], want) {
		t.Fatalf("rating-only header = %#v, want %#v", gotRows[0], want)
	}
	dimension, err := file.GetSheetDimension(sheet)
	if err != nil {
		t.Fatal(err)
	}
	if dimension != "A1:K2" {
		t.Fatalf("rating-only sheet dimension = %q, want A1:K2", dimension)
	}
	for _, forbidden := range []string{"SKU层级1", "货币", "外显价格", "实价", "促销标签"} {
		for _, field := range gotRows[0] {
			if field == forbidden {
				t.Fatalf("rating-only header unexpectedly contains %q: %#v", forbidden, gotRows[0])
			}
		}
	}
}

func TestSplitPriceSupportsHKDollarText(t *testing.T) {
	currency, price := splitPrice("HK$ 1,234.50")
	if currency != "HKD" || price != "1234.50" {
		t.Fatalf("got %s %s", currency, price)
	}
}

func TestTraverseSKUCombinationsRefreshesLayerOptions(t *testing.T) {
	layers := []skuLayerInfo{{Index: 0, Title: "套餐"}, {Index: 1, Title: "天数"}}
	selected := []string{}
	layerOptionCalls := map[int]int{}
	results := traverseSKUCombinations(
		context.Background(),
		layers,
		func(index int) []skuOption {
			layerOptionCalls[index]++
			if index == 0 {
				return []skuOption{{Text: "A", Layer: 1}, {Text: "B", Layer: 1}}
			}
			switch strings.Join(selected, "|") {
			case "A":
				return []skuOption{{Text: "A-1", Layer: 2}}
			case "B":
				return []skuOption{{Text: "B-1", Layer: 2}}
			default:
				return nil
			}
		},
		func(option skuOption, deselect bool) bool {
			if deselect {
				selected = selected[:len(selected)-1]
				return true
			}
			selected = append(selected, option.Text)
			return true
		},
		func(options []skuOption) map[string]string {
			parts := make([]string, 0, len(options))
			for _, option := range options {
				parts = append(parts, option.Text)
			}
			return map[string]string{"combination": strings.Join(parts, " | ")}
		},
		func(Event) {},
	)

	got := []string{results[0]["combination"], results[1]["combination"]}
	want := []string{"A | A-1", "B | B-1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("combinations = %v, want %v", got, want)
	}
	if layerOptionCalls[1] != 2 {
		t.Fatalf("child layer options read %d times, want 2", layerOptionCalls[1])
	}
}

func TestTraverseSKUCombinationsDeselectsAfterLeafPrice(t *testing.T) {
	layers := []skuLayerInfo{{Index: 0, Title: "天数"}, {Index: 1, Title: "流量"}}
	selected := []string{}
	clicks := []string{}
	results := traverseSKUCombinations(
		context.Background(),
		layers,
		func(index int) []skuOption {
			if index == 0 {
				return []skuOption{{Text: "1天", Layer: 1}, {Text: "2天", Layer: 1}}
			}
			switch strings.Join(selected, "|") {
			case "1天":
				return []skuOption{{Text: "每日-1GB", Layer: 2}}
			case "2天":
				return []skuOption{{Text: "每日-2GB", Layer: 2}}
			default:
				return nil
			}
		},
		func(option skuOption, deselect bool) bool {
			action := "select"
			if deselect {
				action = "deselect"
				selected = selected[:len(selected)-1]
			} else {
				selected = append(selected, option.Text)
			}
			clicks = append(clicks, action+":"+option.Text)
			return true
		},
		func(options []skuOption) map[string]string {
			return map[string]string{"combination": strings.Join(selected, " | ")}
		},
		func(Event) {},
	)
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	wantClicks := []string{
		"select:1天",
		"select:每日-1GB",
		"deselect:每日-1GB",
		"deselect:1天",
		"select:2天",
		"select:每日-2GB",
		"deselect:每日-2GB",
		"deselect:2天",
	}
	if !reflect.DeepEqual(clicks, wantClicks) {
		t.Fatalf("clicks = %v, want %v", clicks, wantClicks)
	}
}

func TestWaitForSKULayerCountEventuallyFindsLayers(t *testing.T) {
	calls := 0
	count, _ := waitForSKULayerCount(context.Background(), func() int {
		calls++
		if calls < 3 {
			return 0
		}
		return 2
	}, 50*time.Millisecond, time.Millisecond)
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
	if calls < 3 {
		t.Fatalf("calls = %d, want at least 3", calls)
	}
}

func TestWaitForSKULayerCountTimeoutAllowsNoSKUFallback(t *testing.T) {
	count, _ := waitForSKULayerCount(context.Background(), func() int {
		return 0
	}, 3*time.Millisecond, time.Millisecond)
	if count != 0 {
		t.Fatalf("count = %d, want 0", count)
	}
}

func TestWaitForLayerOptionsEventuallyFindsOptions(t *testing.T) {
	calls := 0
	options, _ := waitForLayerOptions(context.Background(), func(index int) []skuOption {
		if index != 1 {
			t.Fatalf("index = %d, want 1", index)
		}
		calls++
		if calls < 3 {
			return nil
		}
		return []skuOption{{Text: "每日-1GB", Layer: 2}}
	}, 1, 50*time.Millisecond, time.Millisecond)
	if len(options) != 1 || options[0].Text != "每日-1GB" {
		t.Fatalf("options = %+v, want one delayed option", options)
	}
}

func TestCurrentCombinationPriceRowFromBatchPromotionDiscounts(t *testing.T) {
	tests := []struct {
		name          string
		bundle        batchPromotionPriceBundle
		wantPrice     string
		wantRealPrice string
		wantPromo     string
	}{
		{
			name: "fifty percent",
			bundle: batchPromotionPriceBundle{
				Currency:        "HKD",
				OriginalPrice:   1.37,
				DeductionAmount: 0.69,
				PromoDisplay:    "減50%",
				PromoMatched:    true,
			},
			wantPrice:     "1.37",
			wantRealPrice: "0.68",
			wantPromo:     "即減50%",
		},
		{
			name: "five percent",
			bundle: batchPromotionPriceBundle{
				Currency:        "HKD",
				OriginalPrice:   12.63,
				DeductionAmount: 0.64,
				PromoDisplay:    "減5%",
				PromoMatched:    true,
			},
			wantPrice:     "12.63",
			wantRealPrice: "11.99",
			wantPromo:     "即減5%",
		},
		{
			name: "large package",
			bundle: batchPromotionPriceBundle{
				Currency:        "HKD",
				OriginalPrice:   214.51,
				DeductionAmount: 10.73,
				PromoDisplay:    "減5%",
				PromoMatched:    true,
			},
			wantPrice:     "214.51",
			wantRealPrice: "203.78",
			wantPromo:     "即減5%",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row := currentCombinationPriceRowFromBatchPromotion([]skuOption{{Text: "1天", Layer: 1}, {Text: "每日-1GB", Layer: 2}}, tt.bundle, true)
			if row["price"] != tt.wantPrice || row["real_price"] != tt.wantRealPrice || row["promo_tag"] != tt.wantPromo {
				t.Fatalf("row = %+v, want price=%s real=%s promo=%s", row, tt.wantPrice, tt.wantRealPrice, tt.wantPromo)
			}
		})
	}
}

func TestCurrentCombinationPriceRowFromBatchPromotionNoPromoKeepsOriginal(t *testing.T) {
	row := currentCombinationPriceRowFromBatchPromotion(
		[]skuOption{{Text: "1天", Layer: 1}},
		batchPromotionPriceBundle{Currency: "HKD", OriginalPrice: 8.63},
		true,
	)
	if row["price"] != "8.63" || row["real_currency"] != "HKD" || row["real_price"] != "8.63" || row["promo_tag"] != "" {
		t.Fatalf("no-promo row should keep original as real price and blank promo: %+v", row)
	}
}

func TestBatchPromotionBundleHasCompleteBasePrice(t *testing.T) {
	tests := []struct {
		name   string
		bundle batchPromotionPriceBundle
		want   bool
	}{
		{name: "complete static price", bundle: batchPromotionPriceBundle{ResourceID: 123, Currency: "HKD", OriginalPrice: 8.63}, want: true},
		{name: "currency defaults allowed", bundle: batchPromotionPriceBundle{ResourceID: 123, OriginalPrice: 8.63}, want: true},
		{name: "missing resource id", bundle: batchPromotionPriceBundle{OriginalPrice: 8.63}, want: false},
		{name: "missing price", bundle: batchPromotionPriceBundle{ResourceID: 123, Currency: "HKD"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := batchPromotionBundleHasCompleteBasePrice(tt.bundle); got != tt.want {
				t.Fatalf("batchPromotionBundleHasCompleteBasePrice(%+v) = %v, want %v", tt.bundle, got, tt.want)
			}
		})
	}
}

func TestWaitForBatchPromotionPriceBundleFastReturnsOnCompleteNoPromoPrice(t *testing.T) {
	baseTime := time.Unix(100, 0)
	now := baseTime
	sleeps := 0
	bundle, waitMS := waitForBatchPromotionPriceBundle(
		context.Background(),
		[]skuOption{{Text: "1天", Layer: 1}, {Text: "每日-1GB", Layer: 2}},
		func([]string) batchPromotionPriceBundle {
			return batchPromotionPriceBundle{ResourceID: 123, Currency: "HKD", OriginalPrice: 8.63}
		},
		batchPromotionWaitOptions{
			MaxWait:             3 * time.Second,
			PollInterval:        150 * time.Millisecond,
			NoPromoFallbackWait: 2500 * time.Millisecond,
			FastNoPromoReturn:   true,
			Now:                 func() time.Time { return now },
			Sleep: func(context.Context, time.Duration) bool {
				sleeps++
				now = now.Add(150 * time.Millisecond)
				return true
			},
		},
	)
	if bundle.OriginalPrice != 8.63 || bundle.ResourceID != 123 {
		t.Fatalf("unexpected bundle: %+v", bundle)
	}
	if waitMS != 0 || sleeps != 0 {
		t.Fatalf("fast complete no-promo price should not wait: waitMS=%d sleeps=%d", waitMS, sleeps)
	}
}

func TestWaitForBatchPromotionPriceBundleNonFastPreservesNoPromoFallbackWait(t *testing.T) {
	baseTime := time.Unix(100, 0)
	now := baseTime
	sleeps := 0
	bundle, waitMS := waitForBatchPromotionPriceBundle(
		context.Background(),
		[]skuOption{{Text: "1天", Layer: 1}},
		func([]string) batchPromotionPriceBundle {
			return batchPromotionPriceBundle{ResourceID: 123, Currency: "HKD", OriginalPrice: 8.63}
		},
		batchPromotionWaitOptions{
			MaxWait:             3 * time.Second,
			PollInterval:        150 * time.Millisecond,
			NoPromoFallbackWait: 2500 * time.Millisecond,
			FastNoPromoReturn:   false,
			Now:                 func() time.Time { return now },
			Sleep: func(context.Context, time.Duration) bool {
				sleeps++
				now = now.Add(150 * time.Millisecond)
				return true
			},
		},
	)
	if bundle.OriginalPrice != 8.63 || bundle.ResourceID != 123 {
		t.Fatalf("unexpected bundle: %+v", bundle)
	}
	if waitMS < 2500 || sleeps == 0 {
		t.Fatalf("non-fast no-promo price should preserve fallback wait: waitMS=%d sleeps=%d", waitMS, sleeps)
	}
}

func TestWaitForBatchPromotionPriceBundlePromotionResponseReturnsImmediately(t *testing.T) {
	baseTime := time.Unix(100, 0)
	now := baseTime
	sleeps := 0
	bundle, waitMS := waitForBatchPromotionPriceBundle(
		context.Background(),
		[]skuOption{{Text: "1天", Layer: 1}},
		func([]string) batchPromotionPriceBundle {
			return batchPromotionPriceBundle{ResourceID: 123, Currency: "HKD", OriginalPrice: 8.63, PromoMatched: true}
		},
		batchPromotionWaitOptions{
			MaxWait:             3 * time.Second,
			PollInterval:        150 * time.Millisecond,
			NoPromoFallbackWait: 2500 * time.Millisecond,
			FastNoPromoReturn:   false,
			Now:                 func() time.Time { return now },
			Sleep: func(context.Context, time.Duration) bool {
				sleeps++
				now = now.Add(150 * time.Millisecond)
				return true
			},
		},
	)
	if !bundle.PromoMatched || waitMS != 0 || sleeps != 0 {
		t.Fatalf("promotion response should return immediately: bundle=%+v waitMS=%d sleeps=%d", bundle, waitMS, sleeps)
	}
}

func TestApplyBatchPromotionResponse(t *testing.T) {
	response := `{
		"searchItemAggregations": [
			{"itemList": [
				{"itemId": 85965312, "salespromotionList": [
					{"displayName": "減50%", "name": "Half", "deductionAmount": 0.69, "deductionPrice": 0.68,
					 "deductionList": [{"hit": true, "deduction": 0.5, "type": 1}]}
				]},
				{"itemId": 104252031, "salespromotionList": [
					{"displayName": "減5%", "deductionAmount": 0.64, "deductionPrice": 11.99},
					{"displayName": "第二個優惠", "deductionAmount": 1.00}
				]},
				{"itemId": 94674472, "salespromotionList": []},
				{"itemId": 102009474, "salespromotionList": [
					{"displayName": "減5%", "deductionAmount": 0, "deductionType": 1,
					 "deductionList": [{"hit": true, "deduction": 0.05, "type": 1}]}
				]}
			]}
		]
	}`
	tests := []struct {
		name            string
		resourceID      int64
		originalPrice   float64
		wantMatched     bool
		wantFound       bool
		wantDisplay     string
		wantDeduction   float64
		wantDeductPrice float64
	}{
		{name: "fifty percent", resourceID: 85965312, originalPrice: 1.37, wantFound: true, wantMatched: true, wantDisplay: "減50%", wantDeduction: 0.69, wantDeductPrice: 0.68},
		{name: "first promo wins", resourceID: 104252031, originalPrice: 12.63, wantFound: true, wantMatched: true, wantDisplay: "減5%", wantDeduction: 0.64, wantDeductPrice: 11.99},
		{name: "no promotion", resourceID: 94674472, originalPrice: 59.51, wantFound: true, wantMatched: false},
		{name: "deduction list fallback", resourceID: 102009474, originalPrice: 214.51, wantFound: true, wantMatched: true, wantDisplay: "減5%", wantDeduction: 10.7255},
		{name: "item id mismatch", resourceID: 1, originalPrice: 9.99, wantFound: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bundle := batchPromotionPriceBundle{ResourceID: tt.resourceID, OriginalPrice: tt.originalPrice}
			found := applyBatchPromotionResponse(&bundle, response, 200, "https://hk.trip.com/restapi/soa2/31644/batchSearchBookingSalesPromotion")
			if found != tt.wantFound {
				t.Fatalf("found = %v, want %v", found, tt.wantFound)
			}
			if !found {
				return
			}
			if bundle.ItemID != tt.resourceID || bundle.PromoMatched != tt.wantMatched || bundle.PromoDisplay != tt.wantDisplay {
				t.Fatalf("bundle identity/promo mismatch: %+v", bundle)
			}
			if math.Abs(bundle.DeductionAmount-tt.wantDeduction) > 0.000001 || math.Abs(bundle.DeductionPrice-tt.wantDeductPrice) > 0.000001 {
				t.Fatalf("deduction mismatch: %+v, want amount=%v price=%v", bundle, tt.wantDeduction, tt.wantDeductPrice)
			}
		})
	}
}

func TestCurrentCombinationPriceRowUsesBundleAndNoRealPriceFlag(t *testing.T) {
	row := currentCombinationPriceRow(
		[]skuOption{{Text: "1日", Layer: 1}},
		priceBundle{DisplayPrice: "HK$ 99", RealPrice: "HK$ 88", PromoTag: "減 10%"},
		"",
		false,
	)
	if row["currency"] != "HKD" || row["price"] != "99" || row["real_price"] != "N/A" || row["promo_tag"] != "減 10%" {
		t.Fatalf("unexpected row: %+v", row)
	}
}

func TestCurrentCombinationPriceRowFallsBackAndKeepsPythonNAAsSuccess(t *testing.T) {
	withFallback := currentCombinationPriceRow(
		[]skuOption{{Text: "1日", Layer: 1}},
		priceBundle{PromoTag: "折扣"},
		"HK$ 123",
		true,
	)
	if withFallback["price"] != "123" {
		t.Fatalf("fallback price not used: %+v", withFallback)
	}

	pythonCompatible := currentCombinationPriceRow(
		[]skuOption{{Text: "1日", Layer: 1}},
		priceBundle{},
		"",
		true,
	)
	if pythonCompatible["price"] != "N/A" || pythonCompatible["currency"] != "N/A" {
		t.Fatalf("empty display should keep Python N/A success semantics, got %+v", pythonCompatible)
	}
}

func TestCurrentCombinationPriceRowRealPriceMissingBecomesEmpty(t *testing.T) {
	row := currentCombinationPriceRow(
		[]skuOption{{Text: "1日", Layer: 1}},
		priceBundle{DisplayPrice: "HK$ 99"},
		"",
		true,
	)
	if row["real_currency"] != "N/A" || row["real_price"] != "" {
		t.Fatalf("missing real price should become empty with N/A currency, got %+v", row)
	}
}

func TestStablePriceBundleRequiresTwoMatchingFooterSamples(t *testing.T) {
	samples := []priceBundle{
		{DisplayPrice: "HK$ 99", RealPrice: "HK$ 88", PromoTag: "即減15%", FooterSignature: "HK$ 99||HK$ 88||即減15%", SampleAtMS: 360},
	}
	if _, ok := stablePriceBundle(samples, 350*time.Millisecond); ok {
		t.Fatal("single sample should not be stable")
	}
	samples = append(samples, priceBundle{DisplayPrice: "HK$ 99", RealPrice: "HK$ 88", PromoTag: "即減15%", FooterSignature: "HK$ 99||HK$ 88||即減15%", SampleAtMS: 480})
	stable, ok := stablePriceBundle(samples, 350*time.Millisecond)
	if !ok {
		t.Fatal("two matching footer samples should be stable")
	}
	if stable.RealPrice != "HK$ 88" || stable.PromoTag != "即減15%" {
		t.Fatalf("stable bundle = %+v", stable)
	}
}

func TestStablePriceBundleDoesNotCarryPreviousPromoToEmptyStableFooter(t *testing.T) {
	samples := []priceBundle{
		{DisplayPrice: "HK$ 99", RealPrice: "HK$ 80", PromoTag: "即減15%", FooterSignature: "old", SampleAtMS: 120},
		{DisplayPrice: "HK$ 100", FooterSignature: "HK$ 100||||", SampleAtMS: 360},
		{DisplayPrice: "HK$ 100", FooterSignature: "HK$ 100||||", SampleAtMS: 480},
	}
	stable, ok := stablePriceBundle(samples, 350*time.Millisecond)
	if !ok {
		t.Fatal("empty footer stable samples should settle")
	}
	if stable.RealPrice != "" || stable.PromoTag != "" {
		t.Fatalf("previous real/promo leaked into current bundle: %+v", stable)
	}
}

func TestStablePriceBundleKeepsDelayedRealAndPromo(t *testing.T) {
	samples := []priceBundle{
		{DisplayPrice: "HK$ 100", FooterSignature: "HK$ 100||||", SampleAtMS: 120},
		{DisplayPrice: "HK$ 100", RealPrice: "HK$ 72", PromoTag: "即減15%", FooterSignature: "HK$ 100||HK$ 72||即減15%", SampleAtMS: 360},
		{DisplayPrice: "HK$ 100", RealPrice: "HK$ 72", PromoTag: "即減15%", FooterSignature: "HK$ 100||HK$ 72||即減15%", SampleAtMS: 480},
	}
	stable, ok := stablePriceBundle(samples, 350*time.Millisecond)
	if !ok {
		t.Fatal("delayed real/promo should settle after matching samples")
	}
	if stable.RealPrice != "HK$ 72" || stable.PromoTag != "即減15%" {
		t.Fatalf("delayed real/promo not kept: %+v", stable)
	}
}

func TestIsValidDisplayPrice(t *testing.T) {
	if !isValidDisplayPrice("HK$ 1,234.50") {
		t.Fatal("expected HK$ price with space and comma to be valid")
	}
	if isValidDisplayPrice("N/A") || isValidDisplayPrice("USD 1") {
		t.Fatal("unexpected valid display price")
	}
}
