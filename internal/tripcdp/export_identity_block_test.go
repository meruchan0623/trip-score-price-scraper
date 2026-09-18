package tripcdp

import (
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestExportRowsKeepsIdentityFieldsOnContinuationRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity-block.xlsx")
	rows := []map[string]string{
		{"厂商": "V1", "商品ID": "111", "URL": "https://example.test/111", "商品名称": "P1", "SKU层级1": "1 day", "货币": "HKD", "外显价格": "10"},
		{"厂商": "V1", "商品ID": "111", "URL": "https://example.test/111", "商品名称": "P1", "SKU层级1": "2 days", "货币": "HKD", "外显价格": "20"},
		{"厂商": "V1", "商品ID": "111", "URL": "https://example.test/111", "商品名称": "P1", "SKU层级1": "3 days", "货币": "HKD", "外显价格": "30"},
		{"厂商": "V2", "商品ID": "222", "URL": "https://example.test/222", "商品名称": "P2", "SKU层级1": "1 day", "货币": "HKD", "外显价格": "40"},
		{"厂商": "V2", "商品ID": "222", "URL": "https://example.test/222", "商品名称": "P2", "SKU层级1": "2 days", "货币": "HKD", "外显价格": "50"},
	}
	saved, err := exportRows(rows, path, false, false, false, true)
	if err != nil {
		t.Fatal(err)
	}

	file, err := excelize.OpenFile(saved)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	sheet := file.GetSheetName(0)
	get := func(cell string) string {
		v, err := file.GetCellValue(sheet, cell)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	// Columns: A=厂商 B=商品ID C=URL D=商品名称 E=SKU层级1 ...
	want := map[string]string{
		"A2": "V1", "B2": "111", "C2": "https://example.test/111", "D2": "P1", "E2": "1 day",
		"A3": "V1", "B3": "111", "C3": "https://example.test/111", "D3": "P1", "E3": "2 days", "G3": "20",
		"A4": "V1", "B4": "111", "C4": "https://example.test/111", "D4": "P1", "E4": "3 days", "G4": "30",
		"A5": "V2", "B5": "222", "C5": "https://example.test/222", "D5": "P2", "E5": "1 day",
		"A6": "V2", "B6": "222", "C6": "https://example.test/222", "D6": "P2", "E6": "2 days", "G6": "50",
	}
	for cell, expected := range want {
		if got := get(cell); got != expected {
			t.Errorf("cell %s = %q, want %q", cell, got, expected)
		}
	}
	if merged, _ := file.GetMergeCells(sheet); len(merged) != 0 {
		t.Fatalf("expected no merged cells, got %v", merged)
	}

	// Readback must forward-fill identity data on continuation rows.
	back, err := readExportRows(saved)
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != len(rows) {
		t.Fatalf("readback rows = %d, want %d", len(back), len(rows))
	}
	for i, row := range rows {
		for _, field := range []string{"厂商", "商品ID", "URL", "商品名称", "SKU层级1", "外显价格"} {
			if back[i][field] != row[field] {
				t.Errorf("row %d field %s = %q, want %q", i, field, back[i][field], row[field])
			}
		}
	}
}

func TestExportMultiSiteRowsIdentityBlocksResetPerSheet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity-sheets.xlsx")
	rows := []map[string]string{
		{"厂商": "V1", "商品ID": "111", "URL": "https://example.test/111", "商品名称": "P1", "站点": "hk", "SKU层级1": "1 day", "货币": "HKD", "外显价格": "10"},
		{"厂商": "V1", "商品ID": "111", "URL": "https://example.test/111", "商品名称": "P1", "站点": "hk", "SKU层级1": "2 days", "货币": "HKD", "外显价格": "20"},
		{"厂商": "V1", "商品ID": "111", "URL": "https://example.test/111", "商品名称": "P1", "站点": "tw", "SKU层级1": "1 day", "货币": "TWD", "外显价格": "30"},
	}
	saved, err := exportMultiSiteRows(rows, path, false, false, false, true)
	if err != nil {
		t.Fatal(err)
	}
	file, err := excelize.OpenFile(saved)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	for _, sheet := range []string{"HK", "TW"} {
		for _, cell := range []string{"A2", "B2", "C2", "D2"} {
			v, _ := file.GetCellValue(sheet, cell)
			if v == "" {
				t.Errorf("sheet %s first row cell %s is blank; identity must restart per sheet", sheet, cell)
			}
		}
	}
	for _, cell := range []string{"A3", "B3", "C3", "D3"} {
		if v, _ := file.GetCellValue("HK", cell); v == "" {
			t.Errorf("HK continuation cell %s is blank; identity must be present on every SKU row", cell)
		}
	}
	twRows, err := readExportRowsFromSheet(saved, "TW")
	if err != nil {
		t.Fatal(err)
	}
	if len(twRows) != 1 || twRows[0]["商品ID"] != "111" {
		t.Fatalf("TW rows = %#v", twRows)
	}
}
