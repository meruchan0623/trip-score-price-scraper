package tripcdp

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/xuri/excelize/v2"
)

func loadItems(request TaskRequest) ([]ScrapeItem, []string, error) {
	items := make([]ScrapeItem, 0)
	order := 0
	for _, raw := range request.InputPaths {
		url := strings.TrimSpace(raw)
		if url == "" {
			continue
		}
		items = append(items, ScrapeItem{URL: url, Vendor: defaultVendor, InputOrder: order})
		order++
	}
	if request.URLsFile != "" {
		content, err := os.ReadFile(expandPath(request.URLsFile))
		if err != nil {
			return nil, nil, err
		}
		for row, line := range strings.Split(string(content), "\n") {
			url := strings.TrimSpace(line)
			if url == "" {
				continue
			}
			sourceRow := row + 1
			items = append(items, ScrapeItem{URL: url, Vendor: defaultVendor, SourceRow: &sourceRow, InputOrder: order})
			order++
		}
	}
	warnings := []string{}
	if request.ExcelInput != "" {
		excelItems, excelWarnings, err := loadExcelItems(request.ExcelInput, order)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, excelItems...)
		warnings = append(warnings, excelWarnings...)
	}
	if len(items) == 0 {
		return nil, warnings, fmt.Errorf("At least one URL is required via --input, --urls-file, or --excel-input.")
	}
	return items, warnings, nil
}

func loadExcelItems(path string, startOrder int) ([]ScrapeItem, []string, error) {
	file, err := excelize.OpenFile(expandPath(path))
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()

	sheet := file.GetSheetName(file.GetActiveSheetIndex())
	if sheet == "" {
		sheets := file.GetSheetList()
		if len(sheets) > 0 {
			sheet = sheets[0]
		}
	}
	rows, err := file.GetRows(sheet)
	if err != nil {
		return nil, nil, err
	}
	maxCols := 0
	for _, row := range rows {
		if len(row) > maxCols {
			maxCols = len(row)
		}
	}

	items := []ScrapeItem{}
	warnings := []string{}
	order := startOrder
	for col := 0; col < maxCols; col++ {
		vendor := ""
		if len(rows) > 0 && col < len(rows[0]) {
			vendor = strings.TrimSpace(rows[0][col])
		}
		if vendor == "" {
			continue
		}
		found := 0
		for rowIndex := 1; rowIndex < len(rows); rowIndex++ {
			if col >= len(rows[rowIndex]) {
				continue
			}
			url := strings.TrimSpace(rows[rowIndex][col])
			if url == "" {
				continue
			}
			lowered := strings.ToLower(url)
			if strings.Contains(lowered, "http") && strings.Contains(lowered, "trip.com") {
				sourceColumn := col + 1
				sourceRow := rowIndex + 1
				items = append(items, ScrapeItem{
					URL:          url,
					Vendor:       vendor,
					SourceColumn: &sourceColumn,
					SourceRow:    &sourceRow,
					InputOrder:   order,
				})
				order++
				found++
				continue
			}
			warnings = append(warnings, fmt.Sprintf("ignored non-Trip link at column %d, row %d: %s", col+1, rowIndex+1, url))
		}
		if found == 0 {
			warnings = append(warnings, fmt.Sprintf("vendor column %d (%s) has no Trip links", col+1, vendor))
		}
	}
	return items, warnings, nil
}

func vendorGroups(items []ScrapeItem) []vendorGroup {
	sorted := append([]ScrapeItem(nil), items...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].InputOrder < sorted[j].InputOrder
	})
	groups := []vendorGroup{}
	type key struct {
		vendor string
		col    int
		hasCol bool
	}
	seenGroups := map[key]int{}
	vendorSeen := map[string]int{}
	for _, item := range sorted {
		vendor := item.Vendor
		if vendor == "" {
			vendor = defaultVendor
		}
		col := 0
		hasCol := false
		if item.SourceColumn != nil {
			col = *item.SourceColumn
			hasCol = true
		}
		currentKey := key{vendor: vendor, col: col, hasCol: hasCol}
		if index, ok := seenGroups[currentKey]; ok {
			groups[index].Items = append(groups[index].Items, item)
			continue
		}
		vendorSeen[vendor]++
		fileVendor := vendor
		if vendorSeen[vendor] > 1 && hasCol {
			fileVendor = fmt.Sprintf("%s_列%d", vendor, col)
		}
		seenGroups[currentKey] = len(groups)
		groups = append(groups, vendorGroup{Vendor: vendor, FileVendor: fileVendor, Items: []ScrapeItem{item}})
	}
	return groups
}

func resolveWorkerCount(requested int, vendorCount int) (int, error) {
	if requested != 0 && (requested < 1 || requested > maxWorkers) {
		return 0, fmt.Errorf("--workers must be between 1 and %d.", maxWorkers)
	}
	if requested != 0 {
		return requested, nil
	}
	if vendorCount < 1 {
		return 1, nil
	}
	if vendorCount < maxWorkers {
		return vendorCount, nil
	}
	return maxWorkers, nil
}
