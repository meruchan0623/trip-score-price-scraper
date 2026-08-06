package tripcdp

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

// worksheetFields returns the native Trip worksheet columns.
func worksheetFields(rows []map[string]string, ratingOnly, extractRealPrice, extractProductInfo, includeVendor bool) []string {
	maxSKULayers, hasNote, hasQuality := 0, false, false
	for _, row := range rows {
		if strings.TrimSpace(row["抓取备注"]) != "" {
			hasNote = true
		}
		for _, field := range priceQualityExportFields() {
			if strings.TrimSpace(row[field]) != "" {
				hasQuality = true
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
	if includeVendor {
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
		if hasQuality {
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
	if hasNote {
		fields = append(fields, "抓取备注")
	}
	return fields
}

func writeRowsToSheet(file *excelize.File, sheet string, rows []map[string]string, fields []string) error {
	for col, field := range fields {
		cell, err := excelize.CoordinatesToCellName(col+1, 1)
		if err != nil {
			return err
		}
		if err := file.SetCellValue(sheet, cell, exportHeader(field)); err != nil {
			return err
		}
	}
	headerStyle, err := file.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}, Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"}})
	if err != nil {
		return err
	}
	if len(fields) > 0 {
		end, _ := excelize.CoordinatesToCellName(len(fields), 1)
		_ = file.SetCellStyle(sheet, "A1", end, headerStyle)
	}
	widths := make([]int, len(fields))
	for i, field := range fields {
		widths[i] = max(12, runeLen(field)+2)
	}
	for ri, row := range rows {
		for ci, field := range fields {
			value := row[field]
			if value == "" {
				continue
			}
			cell, err := excelize.CoordinatesToCellName(ci+1, ri+2)
			if err != nil {
				return err
			}
			if err := file.SetCellValue(sheet, cell, value); err != nil {
				return err
			}
			widths[ci] = max(widths[ci], min(runeLen(value)+2, 60))
		}
	}
	for ci, width := range widths {
		name, _ := excelize.ColumnNumberToName(ci + 1)
		_ = file.SetColWidth(sheet, name, name, float64(width))
	}
	if len(fields) > 0 {
		last, _ := excelize.CoordinatesToCellName(len(fields), len(rows)+1)
		_ = file.SetSheetDimension(sheet, "A1:"+last)
	}
	return nil
}

func marketWorksheetNames(markets []MarketContext) []string {
	baseCounts := map[string]int{}
	for _, market := range markets {
		baseCounts[strings.ToUpper(cleanSheetPart(market.Code))+"-"+strings.ToUpper(cleanSheetPart(market.Currency))]++
	}
	used := map[string]bool{}
	result := make([]string, len(markets))
	for i, market := range markets {
		base := strings.ToUpper(cleanSheetPart(market.Code)) + "-" + strings.ToUpper(cleanSheetPart(market.Currency))
		name := base
		if baseCounts[base] > 1 && cleanSheetPart(market.Locale) != "" {
			name += "-" + cleanSheetPart(market.Locale)
		}
		name = truncateSheetName(name)
		candidate := name
		for n := 2; used[strings.ToLower(candidate)]; n++ {
			suffix := fmt.Sprintf("-%d", n)
			prefix := []rune(name)
			if len(prefix)+len([]rune(suffix)) > 31 {
				prefix = prefix[:31-len([]rune(suffix))]
			}
			candidate = string(prefix) + suffix
		}
		used[strings.ToLower(candidate)] = true
		result[i] = candidate
	}
	return result
}

func cleanSheetPart(value string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ':', '\\', '/', '?', '*', '[', ']':
			return -1
		}
		return r
	}, strings.TrimSpace(value))
}

func truncateSheetName(value string) string {
	r := []rune(value)
	if len(r) > 31 {
		return string(r[:31])
	}
	return value
}

// exportMarketRows writes successful, non-empty market jobs in request order.
func exportMarketRows(results []MarketJobResult, outputPath string, extractRealPrice, extractProductInfo bool) (string, error) {
	valid := make([]MarketJobResult, 0, len(results))
	for _, result := range results {
		if result.Success && len(result.Rows) > 0 {
			valid = append(valid, result)
		}
	}
	if len(valid) == 0 {
		return "", fmt.Errorf("no successful non-empty market rows to export")
	}
	path := expandPath(outputPath)
	if err := ensureParent(path); err != nil {
		return "", err
	}
	file := excelize.NewFile()
	defer file.Close()
	allMarkets := make([]MarketContext, len(results))
	for i := range results {
		allMarkets[i] = results[i].Job.Market
	}
	allNames := marketWorksheetNames(allMarkets)
	names := make([]string, len(valid))
	validIndex := 0
	for i, result := range results {
		if result.Success && len(result.Rows) > 0 {
			names[validIndex] = allNames[i]
			if strings.TrimSpace(result.Job.SheetName) != "" {
				names[validIndex] = truncateSheetName(cleanSheetPart(result.Job.SheetName))
			}
			validIndex++
		}
	}
	first := file.GetSheetName(file.GetActiveSheetIndex())
	if err := file.SetSheetName(first, names[0]); err != nil {
		return "", err
	}
	for i := range valid {
		if i > 0 {
			if _, err := file.NewSheet(names[i]); err != nil {
				return "", err
			}
		}
		fields := worksheetFields(valid[i].Rows, false, extractRealPrice, extractProductInfo, false)
		if err := writeRowsToSheet(file, names[i], valid[i].Rows, fields); err != nil {
			return "", err
		}
	}
	if err := file.SaveAs(path); err != nil {
		return "", err
	}
	return absPath(path), nil
}
