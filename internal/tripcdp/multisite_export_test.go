package tripcdp

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestExportMultiSiteRowsSheetsPerSiteWithVendorColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "multi_site.xlsx")
	rows := []map[string]string{
		{"厂商": "TSIM 網址", "商品ID": "1", "商品名称": "A", "站点": "hk", "评分": "4.8"},
		{"厂商": "BC", "商品ID": "2", "商品名称": "B", "站点": "hk", "评分": "4.5"},
		{"厂商": "TSIM 網址", "商品ID": "3", "商品名称": "C", "站点": "tw", "评分": "4.2"},
	}
	saved, err := exportMultiSiteRows(rows, path, true, false, true, true)
	if err != nil {
		t.Fatalf("export failed: %v", err)
	}
	if saved == "" {
		t.Fatal("empty saved path")
	}

	// Combined workbook must have one sheet per site code (HK, TW).
	hkRows, err := readExportRows(saved)
	if err != nil {
		t.Fatalf("read-back failed: %v", err)
	}
	if len(hkRows) != 2 {
		t.Fatalf("HK sheet expected 2 rows, got %d", len(hkRows))
	}
	totalVendor := 0
	for _, row := range hkRows {
		if v := strings.TrimSpace(row["厂商"]); v != "" {
			totalVendor++
		}
	}
	if totalVendor != 2 {
		t.Fatalf("vendor column not preserved in sheet, got %d/2", totalVendor)
	}
}

func TestReadExportRowsSecondSheetRead(t *testing.T) {
	// A two-site export produces per-site sheets; read the TW sheet directly.
	src := []map[string]string{
		{"厂商": "TSIM 網址", "商品ID": "1", "商品名称": "A", "站点": "hk", "评分": "4.8"},
		{"厂商": "TSIM 網址", "商品ID": "3", "商品名称": "C", "站点": "tw", "评分": "4.2"},
	}
	path := filepath.Join(t.TempDir(), "tw_sheet.xlsx")
	saved, err := exportMultiSiteRows(src, path, true, false, true, true)
	if err != nil {
		t.Fatalf("export failed: %v", err)
	}
	rows, err := readExportRowsFromSheet(saved, "TW")
	if err != nil {
		t.Fatalf("read TW sheet failed: %v", err)
	}
	if len(rows) != 1 || strings.TrimSpace(rows[0]["厂商"]) != "TSIM 網址" {
		t.Fatalf("TW sheet rows = %#v", rows)
	}
}

func TestExportMultiSiteRowsSingleSiteFallsBackToPlainExport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "single_site.xlsx")
	rows := []map[string]string{
		{"厂商": "TSIM 網址", "商品ID": "1", "商品名称": "A", "站点": "hk", "评分": "4.8"},
	}
	saved, err := exportMultiSiteRows(rows, path, true, false, true, true)
	if err != nil {
		t.Fatalf("export failed: %v", err)
	}
	back, err := readExportRows(saved)
	if err != nil {
		t.Fatalf("read-back failed: %v", err)
	}
	if len(back) != 1 {
		t.Fatalf("expected 1 row, got %d", len(back))
	}
	if strings.TrimSpace(back[0]["厂商"]) != "TSIM 網址" {
		t.Fatalf("vendor column missing in single-site fallback: %#v", back[0])
	}
}

func TestReadExportRowsEmptySheet(t *testing.T) {
	// Header-only workbook should yield nil rows, no error.
	path := filepath.Join(t.TempDir(), "empty.xlsx")
	_, err := exportMultiSiteRows(nil, path, true, false, true, true)
	if err != nil {
		t.Fatalf("export empty failed: %v", err)
	}
	back, err := readExportRows(path)
	if err != nil {
		t.Fatalf("read-back failed: %v", err)
	}
	if back != nil {
		t.Fatalf("expected nil rows for empty sheet, got %+v", back)
	}
}
