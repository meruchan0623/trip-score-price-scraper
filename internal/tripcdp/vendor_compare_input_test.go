package tripcdp

import (
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestParseInputConfigUsesNamedCategoryAndDestinationColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vendors.xlsx")
	f := excelize.NewFile()
	sheet := f.GetSheetName(f.GetActiveSheetIndex())
	headers := []string{"TSIM ID", "分類", "目的地", "TSIM 網址", "BC"}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue(sheet, cell, h)
	}
	values := []string{"65887411", "亞洲", "Philippines", "https://hk.trip.com/things-to-do/detail/65887411/", "https://hk.trip.com/things-to-do/detail/51934317/"}
	for i, value := range values {
		cell, _ := excelize.CoordinatesToCellName(i+1, 2)
		_ = f.SetCellValue(sheet, cell, value)
	}
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}

	groups, err := parseInputConfig(path, "TSIM 網址")
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(groups))
	}
	if groups[0].category != "亞洲" || groups[0].destination != "Philippines" {
		t.Fatalf("category/destination = %q/%q, want 亞洲/Philippines", groups[0].category, groups[0].destination)
	}
}
