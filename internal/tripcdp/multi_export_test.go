package tripcdp

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestMarketWorksheetNamesAreOrderedSanitizedAndDisambiguated(t *testing.T) {
	markets := []MarketContext{
		{Code: "hk", Currency: "HKD"},
		{Code: "sg", Currency: "MYR", Locale: "zh-SG"},
		{Code: "sg", Currency: "MYR", Locale: "en-SG"},
		{Code: "bad:/?*\\name", Currency: "USD"},
		{Code: "BADNAME", Currency: "usd"},
	}
	want := []string{"HK-HKD", "SG-MYR-zh-SG", "SG-MYR-en-SG", "BADNAME-USD", "BADNAME-USD-2"}
	if got := marketWorksheetNames(markets); !reflect.DeepEqual(got, want) {
		t.Fatalf("names = %#v, want %#v", got, want)
	}
}

func TestExportMarketRowsWritesOnlySuccessfulNonEmptySheetsInRequestOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "markets.xlsx")
	rows := func(id, currency string) []map[string]string {
		return []map[string]string{{"商品ID": id, "商品名称": "Product " + id, "货币": currency, "外显价格": "10"}}
	}
	results := []MarketJobResult{
		{Job: MarketJob{Market: MarketContext{Code: "jp", Currency: "JPY"}}, Rows: rows("jp", "JPY"), Success: true},
		{Job: MarketJob{Market: MarketContext{Code: "hk", Currency: "HKD"}}, Rows: nil, Success: true},
		{Job: MarketJob{Market: MarketContext{Code: "sg", Currency: "SGD"}}, Rows: rows("sg", "SGD"), Success: false},
		{Job: MarketJob{Market: MarketContext{Code: "us", Currency: "USD"}}, Rows: rows("us", "USD"), Success: true},
	}
	if _, err := exportMarketRows(results, path, false, false); err != nil {
		t.Fatal(err)
	}
	file, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if got, want := file.GetSheetList(), []string{"JP-JPY", "US-USD"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sheet order = %#v, want %#v", got, want)
	}
	for _, sheet := range []string{"JP-JPY", "US-USD"} {
		gotRows, err := file.GetRows(sheet)
		if err != nil || len(gotRows) != 2 {
			t.Fatalf("sheet %s rows = %#v, err=%v", sheet, gotRows, err)
		}
		if gotRows[0][0] != "商品ID" || gotRows[1][0] == "" {
			t.Fatalf("sheet %s native header/value = %#v", sheet, gotRows)
		}
	}
}

func TestExportMarketRowsKeepsLocaleSemanticNameWhenAnotherLocaleFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "locale.xlsx")
	row := []map[string]string{{"商品ID": "1", "货币": "MYR"}}
	results := []MarketJobResult{
		{Job: MarketJob{Market: MarketContext{Code: "sg", Currency: "MYR", Locale: "zh-SG"}}, Rows: nil, Success: false},
		{Job: MarketJob{Market: MarketContext{Code: "sg", Currency: "MYR", Locale: "en-SG"}}, Rows: row, Success: true},
	}
	if _, err := exportMarketRows(results, path, false, false); err != nil {
		t.Fatal(err)
	}
	file, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if got := file.GetSheetList(); !reflect.DeepEqual(got, []string{"SG-MYR-en-SG"}) {
		t.Fatalf("sheets=%v", got)
	}
}

func TestMarketWorksheetNamesTruncationAndSuffixStayWithinExcelLimit(t *testing.T) {
	long := "site-with-an-extremely-long-name-abcdefghijk"
	got := marketWorksheetNames([]MarketContext{{Code: long, Currency: "USD"}, {Code: long, Currency: "USD"}})
	if len([]rune(got[0])) > 31 || len([]rune(got[1])) > 31 {
		t.Fatalf("names exceed 31 runes: %q", got)
	}
	if strings.EqualFold(got[0], got[1]) || !strings.HasSuffix(got[1], "-2") {
		t.Fatalf("collision suffix missing: %q", got)
	}
}

func TestExportMarketRowsRefusesEmptyWorkbook(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.xlsx")
	if _, err := exportMarketRows([]MarketJobResult{{Success: false}}, path, false, false); err == nil {
		t.Fatal("expected empty market export to fail")
	}
}
