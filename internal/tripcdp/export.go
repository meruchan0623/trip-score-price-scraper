package tripcdp

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

func exportRows(rows []map[string]string, outputPath string, ratingOnly bool, extractRealPrice bool, extractProductInfo bool, includeVendorColumn bool) (string, error) {
	path := expandPath(outputPath)
	if err := ensureParent(path); err != nil {
		return "", err
	}
	return exportToFile(rows, path, ratingOnly, extractRealPrice, extractProductInfo, includeVendorColumn)
}

// exportMultiSiteRows writes rows to one workbook with a sheet per distinct site
// code (read from the "站点" field). If no "站点" field exists, all rows go into
// one default sheet.
func exportMultiSiteRows(rows []map[string]string, outputPath string, ratingOnly bool, extractRealPrice bool, extractProductInfo bool, includeVendorColumn bool) (string, error) {
	path := expandPath(outputPath)
	if err := ensureParent(path); err != nil {
		return "", err
	}

	// Group by site
	sites := make(map[string][]map[string]string)
	siteOrder := []string{}
	for _, row := range rows {
		code := strings.TrimSpace(row["站点"])
		if code == "" {
			code = "default"
		}
		if _, exists := sites[code]; !exists {
			siteOrder = append(siteOrder, code)
		}
		sites[code] = append(sites[code], row)
	}
	// Only one site group — delegate to plain export
	if len(siteOrder) <= 1 {
		return exportToFile(rows, path, ratingOnly, extractRealPrice, extractProductInfo, includeVendorColumn)
	}

	// Multi-sheet workbook
	file := excelize.NewFile()
	// Don't try to DeleteSheet("Sheet1") because excelize refuses to delete
	// the last sheet. Instead, rename the default sheet to the first site code.
	firstSheetName := strings.ToUpper(siteOrder[0])
	if err := file.SetSheetName(file.GetSheetName(file.GetActiveSheetIndex()), firstSheetName); err != nil {
		file.Close()
		return "", fmt.Errorf("rename default sheet to %q: %w", firstSheetName, err)
	}
	writeExportSheet(file, firstSheetName, sites[siteOrder[0]], ratingOnly, extractRealPrice, extractProductInfo, includeVendorColumn)

	for _, code := range siteOrder[1:] {
		sheetName := strings.ToUpper(code)
		sheetRows := sites[code]
		idx, err := file.NewSheet(sheetName)
		if err != nil {
			file.Close()
			return "", fmt.Errorf("create sheet %q: %w", sheetName, err)
		}
		file.SetActiveSheet(idx)
		writeExportSheet(file, sheetName, sheetRows, ratingOnly, extractRealPrice, extractProductInfo, includeVendorColumn)
	}

	if err := file.SaveAs(path); err != nil {
		file.Close()
		return "", err
	}
	_ = file.Close()
	return absPath(path), nil
}

func exportToFile(rows []map[string]string, path string, ratingOnly bool, extractRealPrice bool, extractProductInfo bool, includeVendorColumn bool) (string, error) {
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
	writeExportSheet(file, sheet, rows, ratingOnly, extractRealPrice, extractProductInfo, includeVendorColumn)
	if err := file.SaveAs(path); err != nil {
		return "", err
	}
	return absPath(path), nil
}

func writeExportSheet(file *excelize.File, sheet string, rows []map[string]string, ratingOnly bool, extractRealPrice bool, extractProductInfo bool, includeVendorColumn bool) {
	maxSKULayers := 0
	hasScrapeNote := false
	hasPriceQuality := false
	for _, row := range rows {
		if strings.TrimSpace(row["抓取备注"]) != "" {
			hasScrapeNote = true
		}
		for _, field := range priceQualityExportFields() {
			if strings.TrimSpace(row[field]) != "" {
				hasPriceQuality = true
				break
			}
		}
		for key := range row {
			if strings.HasPrefix(key, "SKU层级") {
				n, err := strconv.Atoi(strings.TrimPrefix(key, "SKU层级"))
				if err == nil && n > maxSKULayers {
					maxSKULayers = n
				}
			}
		}
	}

	fields := []string{}
	if includeVendorColumn {
		fields = append(fields, "厂商")
	}
	fields = append(fields, "商品ID", "URL", "商品名称")
	if !ratingOnly {
		if maxSKULayers < 1 {
			maxSKULayers = 1
		}
		for i := 1; i <= maxSKULayers; i++ {
			fields = append(fields, fmt.Sprintf("SKU层级%d", i))
		}
		fields = append(fields, "货币", "外显价格")
		if extractRealPrice {
			fields = append(fields, "实价")
		}
		fields = append(fields, "促销标签")
		if hasPriceQuality {
			fields = append(fields, priceQualityExportFields()...)
		}
	}
	if extractProductInfo {
		fields = append(fields, "商品资讯")
	}
	fields = append(fields, "总促销标签")
	fields = append(fields, "月销量", "年销量", "总销量", "评价数", "评分", "好评率", "客户服务", "方便程度", "讯号状态", "中差评价数")
	if hasScrapeNote {
		fields = append(fields, "抓取备注")
	}

	for col, field := range fields {
		cell, _ := excelize.CoordinatesToCellName(col+1, 1)
		_ = file.SetCellValue(sheet, cell, exportHeader(field))
	}
	headerStyle, _ := file.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}, Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"}})
	endHeader, _ := excelize.CoordinatesToCellName(len(fields), 1)
	_ = file.SetCellStyle(sheet, "A1", endHeader, headerStyle)

	widths := make([]int, len(fields))
	for i, field := range fields {
		widths[i] = max(12, runeLen(field)+2)
	}
	for rowIndex, row := range rows {
		// Identity fields (厂商/商品ID/URL/商品名称) are written on every SKU row,
		// including continuation rows of the same 商品ID, so each row is
		// self-describing when filtered or sorted.
		for col, field := range fields {
			value := row[field]
			if value == "" {
				continue
			}
			cell, _ := excelize.CoordinatesToCellName(col+1, rowIndex+2)
			_ = file.SetCellValue(sheet, cell, value)
			widths[col] = max(widths[col], min(runeLen(value)+2, 60))
		}
	}
	for col, width := range widths {
		name, _ := excelize.ColumnNumberToName(col + 1)
		_ = file.SetColWidth(sheet, name, name, float64(width))
	}
	lastCell, _ := excelize.CoordinatesToCellName(len(fields), len(rows)+1)
	_ = file.SetSheetDimension(sheet, "A1:"+lastCell)
}

func isProductIdentityField(field string) bool {
	switch field {
	case "厂商", "商品ID", "URL", "商品名称":
		return true
	}
	return false
}

func priceQualityExportFields() []string {
	return []string{"价格状态"}
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

// readExportRows reads a program-exported XLSX back into a slice of row maps,
// using the first row's headers as keys. Used by scrapeVendorMultiSite to merge
// per-vendor-per-market XLSX files into a combined multi-site workbook.
func readExportRows(path string) ([]map[string]string, error) {
	file, err := excelize.OpenFile(expandPath(path))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return exportRowsFromSheet(file, file.GetSheetName(0))
}

// readExportRowsFromSheet reads a program-exported XLSX from a specific sheet.
func readExportRowsFromSheet(path, sheet string) ([]map[string]string, error) {
	file, err := excelize.OpenFile(expandPath(path))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return exportRowsFromSheet(file, sheet)
}

func exportRowsFromSheet(file *excelize.File, sheet string) ([]map[string]string, error) {
	raw, err := file.GetRows(sheet)
	if err != nil {
		return nil, err
	}
	if len(raw) < 2 {
		return nil, nil
	}
	headers := raw[0]
	rows := make([]map[string]string, 0, len(raw)-1)
	for _, line := range raw[1:] {
		row := make(map[string]string, len(headers))
		for i, h := range headers {
			var v string
			if i < len(line) {
				v = line[i]
			}
			row[h] = v
		}
		// Continuation rows are exported with blank identity cells; forward-fill
		// them from the previous row so readback reconstructs the full rows.
		if strings.TrimSpace(row["商品ID"]) == "" && len(rows) > 0 {
			prev := rows[len(rows)-1]
			for _, h := range headers {
				if isProductIdentityField(h) && row[h] == "" {
					row[h] = prev[h]
				}
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}
