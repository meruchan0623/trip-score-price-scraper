package tripcdp

import (
	"context"
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
	want := []string{"厂商", "商品ID", "URL", "商品名称", "SKU层级1", "SKU层级2", "货币", "外显价格", "实价", "促销标签", "预定数", "评价数", "评分", "客户服务", "方便程度", "讯号状态", "中差评价数", "商品资讯"}
	if len(header) == 0 || len(header[0]) != len(want) {
		t.Fatalf("unexpected header length: %v", header)
	}
	for i, value := range want {
		if header[0][i] != value {
			t.Fatalf("header[%d] = %q, want %q", i, header[0][i], value)
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
