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
	if !ratingOnly {
		fields = append(fields, "总促销标签")
	}
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
