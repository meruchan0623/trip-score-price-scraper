package tripcdp

import (
	"os"
	"path/filepath"

	"github.com/xuri/excelize/v2"
)

func exportRows(rows []map[string]string, outputPath string, ratingOnly bool, extractRealPrice bool, extractProductInfo bool, includeVendorColumn bool) (string, error) {
	path := expandPath(outputPath)
	if err := ensureParent(path); err != nil {
		return "", err
	}

	file := excelize.NewFile()
	defer file.Close()
	sheet := "Trip商品数据"
	if ratingOnly {
		sheet = "Trip评分数据"
	}
	defaultSheet := file.GetSheetName(file.GetActiveSheetIndex())
	if err := file.SetSheetName(defaultSheet, sheet); err != nil {
		return "", err
	}

	fields := worksheetFields(rows, ratingOnly, extractRealPrice, extractProductInfo, includeVendorColumn)
	if err := writeRowsToSheet(file, sheet, rows, fields); err != nil {
		return "", err
	}

	if err := file.SaveAs(path); err != nil {
		return "", err
	}
	return absPath(path), nil
}

func priceQualityExportFields() []string {
	return []string{"价格状态", "原始捕获价格"}
}

func exportHeader(field string) string {
	if field == "价格状态" {
		return "价格可信标签"
	}
	return field
}

func ensureParent(path string) error {
	return mkdirAll(filepath.Dir(expandPath(path)))
}

func mkdirAll(path string) error {
	if path == "." || path == "" {
		return nil
	}
	return os.MkdirAll(path, 0o755)
}

func runeLen(value string) int {
	return len([]rune(value))
}
