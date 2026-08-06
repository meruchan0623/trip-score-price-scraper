package tripcdp

import (
	"archive/zip"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportRowsOmitsBlankContinuationCells(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blank-continuation.xlsx")
	rows := []map[string]string{
		{"商品ID": "45892282", "URL": "https://example.test/45892282", "商品名称": "Product", "SKU层级1": "1 day", "货币": "HKD", "外显价格": "10"},
		{"商品ID": "", "URL": "", "商品名称": "", "SKU层级1": "2 days", "货币": "HKD", "外显价格": "20"},
	}
	if _, err := exportRows(rows, path, false, false, false, false); err != nil {
		t.Fatal(err)
	}

	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var sheetXML string
	for _, file := range zr.File {
		if file.Name != "xl/worksheets/sheet1.xml" {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		sheetXML = string(raw)
	}
	if sheetXML == "" {
		t.Fatal("sheet1.xml not found")
	}
	for _, cell := range []string{`r="A3"`, `r="B3"`, `r="C3"`} {
		if strings.Contains(sheetXML, cell) {
			t.Fatalf("blank continuation cell %s was serialized: %s", cell, sheetXML)
		}
	}
}
