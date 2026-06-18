package tripcdp

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

// regionGroup represents one row in the input config: a set of vendor URLs for the same region.
type regionGroup struct {
	rowIndex       int
	category       string
	destination    string
	baseURL        string
	baseVendor     string
	competitorURLs map[string]string
}

// skuSpec is the structured, cross-vendor comparable interpretation of one SKU row.
type skuSpec struct {
	DurationDays  int
	DataType      string // total / daily / unlimited / unknown
	DataMB        int
	CanonicalSKU1 string
	CanonicalSKU2 string
	MatchKey      string
	MatchRule     string // exact_spec / unit_converted / normalized_text / near_equivalent / unmatched
	Confidence    int
	Valid         bool
}

// productSKU represents one SKU row from a vendor's scraped XLSX.
type productSKU struct {
	ProductID    string
	URL          string
	ProductName  string
	SKU1         string
	SKU2         string
	SKU2Norm     string
	Spec         skuSpec
	RealPrice    float64
	Currency     string
	DisplayPrice string
	PromoTag     string
	Bookings     string
	Reviews      string
	Rating       string
}

// productComparison aggregates matched SKUs for one product pair. All metrics are matched-SKU-only.
type productComparison struct {
	Category      string
	Destination   string
	BaseVendor    string
	CompVendor    string
	BaseProductID string
	CompProductID string
	BaseProdName  string
	CompProdName  string
	MatchCount    int
	AvgDelta      float64 // avg(comp - base) across matched SKUs; positive = base cheaper
	MinDelta      float64 // compMin - baseMin across matched SKUs; positive = base lower minimum
	BaseCheaper   int
	BaseMinPrice  float64
	CompMinPrice  float64
	BaseAvgPrice  float64
	CompAvgPrice  float64
}

type VendorComparisonResult struct {
	BaseVendor       string                    `json:"base_vendor"`
	Comparisons      []productComparison       `json:"-"`
	PerVendorSummary map[string]*vendorSummary `json:"per_vendor_summary"`
	OutputPath       string                    `json:"output_path"`
}

type vendorSummary struct {
	Vendor           string        `json:"vendor"`
	TotalRegions     int           `json:"total_regions"`
	MatchedRegions   int           `json:"matched_regions"`
	TotalMatchedSKUs int           `json:"total_matched_skus"`
	AvgPriceDelta    float64       `json:"avg_price_delta"`
	BaseCheaperPct   float64       `json:"base_cheaper_pct"`
	TopCheaper       []regionDelta `json:"top_cheaper"`
	TopExpensive     []regionDelta `json:"top_expensive"`
}

type regionDelta struct {
	Category    string  `json:"category"`
	Destination string  `json:"destination"`
	MatchCount  int     `json:"match_count"`
	AvgDelta    float64 `json:"avg_delta"`
	MinDelta    float64 `json:"min_delta"`
	BaseCheaper int     `json:"base_cheaper"`
	TotalSKUs   int     `json:"total_skus"`
	BaseMin     float64 `json:"base_min"`
	CompMin     float64 `json:"comp_min"`
}

type compareVendorBlock struct {
	Vendor         string
	SKU            *productSKU
	Delta          *float64 // row-level real price delta vs base for the same SKU spec
	MinDelta       *float64 // product-level minimum delta, populated only on this vendor's lowest matched SKU row
	Conclusion     string
	CandidateCount int
}

type horizontalCompareRow struct {
	InputRow      int
	Category      string
	Destination   string
	DurationDays  int
	DataType      string
	DataMB        int
	CanonicalSKU1 string
	CanonicalSKU2 string
	MatchRule     string
	Confidence    int
	MatchedCount  int
	Blocks        map[string]compareVendorBlock
}

var skuNormRe = regexp.MustCompile(`^(5G網絡-|5G网络-|5G-|4G-|4G網絡-|4G网络-|高速-)`)
var skuNetworkPrefixRe = regexp.MustCompile(`(?i)^(5g網絡|5g网络|5g|4g網絡|4g网络|4g|高速)[\s\-_/]*`)
var urlCleanRe = regexp.MustCompile(`(\?.*)$`)
var durationRe = regexp.MustCompile(`(?i)(\d+)\s*(天|日|days?|day)`)     // Chinese/English duration
var amountRe = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(gb|g|mb|m)`) // data amount

func normalizeSKU2(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "－", "-")
	s = strings.ReplaceAll(s, "–", "-")
	s = strings.ReplaceAll(s, "—", "-")
	s = strings.ReplaceAll(s, "_", "-")
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "　", "")
	s = strings.ReplaceAll(s, "總量", "总量")
	s = strings.ReplaceAll(s, "網絡", "网络")
	s = skuNormRe.ReplaceAllString(s, "")
	// Remove separators between data type and number so 总量-10GB, 总量 10GB and 总量10GB converge.
	s = strings.ReplaceAll(s, "总量-", "总量")
	s = strings.ReplaceAll(s, "每日-", "每日")
	s = strings.ReplaceAll(s, "每天-", "每天")
	return s
}

func skuKey(s productSKU) string {
	if s.Spec.MatchKey != "" {
		return s.Spec.MatchKey
	}
	return strings.TrimSpace(s.SKU1) + "|" + normalizeSKU2(s.SKU2)
}

func parseFloatSafe(v interface{}) float64 {
	s := strings.TrimSpace(fmt.Sprint(v))
	if s == "" || s == "<nil>" {
		return 0
	}
	s = strings.ReplaceAll(s, ",", "")
	var f float64
	if _, err := fmt.Sscanf(s, "%f", &f); err != nil {
		return 0
	}
	return f
}

func parseInputConfig(excelPath string, baseVendor string) ([]regionGroup, error) {
	f, err := excelize.OpenFile(excelPath)
	if err != nil {
		return nil, fmt.Errorf("open input config: %w", err)
	}
	defer f.Close()
	sheet := f.GetSheetName(f.GetActiveSheetIndex())
	rows, err := f.GetRows(sheet)
	if err != nil {
		return nil, fmt.Errorf("read sheet: %w", err)
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("input config must have at least 2 rows")
	}
	type vendorCol struct {
		name   string
		colIdx int
	}
	vendorCols := []vendorCol{}
	categoryColIdx := -1
	destinationColIdx := -1
	for i, h := range rows[0] {
		h = strings.TrimSpace(h)
		switch h {
		case "分類", "分类":
			categoryColIdx = i
			continue
		case "目的地":
			destinationColIdx = i
			continue
		}
		if h == "" || isInputMetadataHeader(h) {
			continue
		}
		vendorCols = append(vendorCols, vendorCol{h, i})
	}
	baseColIdx := -1
	baseVendorName := baseVendor
	for _, vc := range vendorCols {
		if vc.name == baseVendor {
			baseColIdx = vc.colIdx
			baseVendorName = vc.name
			break
		}
	}
	if baseColIdx == -1 {
		return nil, fmt.Errorf("base vendor %q not found in input config headers", baseVendor)
	}
	groups := []regionGroup{}
	for rowIdx := 1; rowIdx < len(rows); rowIdx++ {
		row := rows[rowIdx]
		if baseColIdx >= len(row) || strings.TrimSpace(row[baseColIdx]) == "" {
			continue
		}
		category, destination := "", ""
		if categoryColIdx >= 0 && categoryColIdx < len(row) {
			category = strings.TrimSpace(row[categoryColIdx])
		}
		if destinationColIdx >= 0 && destinationColIdx < len(row) {
			destination = strings.TrimSpace(row[destinationColIdx])
		}
		compURLs := map[string]string{}
		for _, vc := range vendorCols {
			if vc.colIdx == baseColIdx {
				continue
			}
			if vc.colIdx < len(row) {
				url := strings.TrimSpace(row[vc.colIdx])
				if url != "" && strings.Contains(strings.ToLower(url), "trip.com") {
					compURLs[vc.name] = url
				}
			}
		}
		if len(compURLs) == 0 {
			continue
		}
		groups = append(groups, regionGroup{rowIndex: rowIdx + 1, category: category, destination: destination, baseURL: strings.TrimSpace(row[baseColIdx]), baseVendor: baseVendorName, competitorURLs: compURLs})
	}
	return groups, nil
}

func isInputMetadataHeader(header string) bool {
	header = strings.TrimSpace(header)
	switch header {
	case "TSIM ID", "商品ID", "产品ID", "產品ID", "ID", "区域", "地區", "地区", "Region", "region":
		return true
	default:
		return false
	}
}

func loadVendorProducts(xlsxPath string) (map[string][]productSKU, error) {
	f, err := excelize.OpenFile(xlsxPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sheet := f.GetSheetName(f.GetActiveSheetIndex())
	rows, err := f.GetRows(sheet)
	if err != nil {
		return nil, err
	}
	if len(rows) < 2 {
		return map[string][]productSKU{}, nil
	}
	colIdx := map[string]int{}
	for i, h := range rows[0] {
		colIdx[strings.TrimSpace(h)] = i
	}
	getVal := func(row []string, name string) string {
		if i, ok := colIdx[name]; ok && i < len(row) {
			return strings.TrimSpace(row[i])
		}
		return ""
	}
	products := map[string][]productSKU{}
	currentPID, currentName, currentURL := "", "", ""
	hasPriceStatus := false
	if _, ok := colIdx["价格状态"]; ok {
		hasPriceStatus = true
	}
	for _, row := range rows[1:] {
		pid := getVal(row, "商品ID")
		if pid != "" {
			currentPID = pid
			currentName = getVal(row, "商品名称")
			currentURL = getVal(row, "URL")
		}
		if currentPID == "" {
			continue
		}
		if hasPriceStatus && !isTrustedPriceStatus(getVal(row, "价格状态")) {
			continue
		}
		realPriceStr := getVal(row, "实价")
		realPrice := parseFloatSafe(realPriceStr)
		if realPriceStr == "" {
			realPrice = parseFloatSafe(getVal(row, "外显价格"))
		}
		sku := productSKU{ProductID: currentPID, URL: currentURL, ProductName: currentName, SKU1: getVal(row, "SKU层级1"), SKU2: getVal(row, "SKU层级2"), Currency: getVal(row, "货币"), DisplayPrice: getVal(row, "外显价格"), RealPrice: realPrice, PromoTag: getVal(row, "促销标签"), Bookings: getVal(row, "预定数"), Reviews: getVal(row, "评价数"), Rating: getVal(row, "评分")}
		sku.SKU2Norm = normalizeSKU2(sku.SKU2)
		sku.Spec = parseSKUSpec(sku.SKU1, sku.SKU2, sku.ProductName)
		products[currentPID] = append(products[currentPID], sku)
	}
	return products, nil
}

func normalizeSpecText(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ToLower(s)
	repls := [][2]string{{"總量", "总量"}, {"網絡", "网络"}, {"每天", "每日"}, {"daily", "每日"}, {"total", "总量"}, {"－", "-"}, {"–", "-"}, {"—", "-"}, {"_", "-"}, {"　", " "}}
	for _, r := range repls {
		s = strings.ReplaceAll(s, r[0], r[1])
	}
	s = strings.TrimSpace(skuNetworkPrefixRe.ReplaceAllString(s, ""))
	return s
}

func parseDurationDays(parts ...string) int {
	for _, p := range parts {
		m := durationRe.FindStringSubmatch(normalizeSpecText(p))
		if len(m) >= 2 {
			if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
				return n
			}
		}
	}
	return 0
}

func parseDataType(s string) string {
	s = normalizeSpecText(s)
	if strings.Contains(s, "无限") || strings.Contains(s, "無限") || strings.Contains(s, "unlimited") {
		return "unlimited"
	}
	if strings.Contains(s, "每日") || strings.Contains(s, "/天") || strings.Contains(s, "每天") {
		return "daily"
	}
	if strings.Contains(s, "总量") || strings.Contains(s, "總量") {
		return "total"
	}
	return "unknown"
}

func parseAmountMB(s string) (int, bool, string) {
	s = normalizeSpecText(s)
	m := amountRe.FindStringSubmatch(s)
	if len(m) < 3 {
		return 0, false, ""
	}
	val, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, false, ""
	}
	unit := strings.ToLower(m[2])
	if unit == "gb" || unit == "g" {
		return int(math.Round(val * 1024)), true, "gb"
	}
	mb := int(math.Round(val))
	// Common user-facing equivalence: 500MB and 0.5GB are effectively the same SKU family.
	if mb >= 490 && mb <= 530 {
		return 512, true, "mb_rounded_to_half_gb"
	}
	return mb, true, "mb"
}

func dataTypeZH(t string) string {
	switch t {
	case "total":
		return "总量"
	case "daily":
		return "每日"
	case "unlimited":
		return "无限"
	default:
		return "未知"
	}
}

func formatDataAmount(mb int) string {
	if mb <= 0 {
		return ""
	}
	if mb%1024 == 0 {
		return fmt.Sprintf("%dGB", mb/1024)
	}
	if mb == 512 {
		return "0.5GB"
	}
	return fmt.Sprintf("%dMB", mb)
}

func parseSKUSpec(sku1, sku2, productName string) skuSpec {
	duration := parseDurationDays(sku1, sku2, productName)
	dataType := parseDataType(sku2)
	amountMB, hasAmount, amountRule := parseAmountMB(sku2)
	if dataType == "unknown" && hasAmount {
		// Amount without explicit daily/total is kept as unknown to avoid false daily-vs-total merging.
		dataType = "unknown"
	}
	if duration > 0 && (hasAmount || dataType == "unlimited") && dataType != "unknown" {
		rule := "exact_spec"
		confidence := 100
		if amountRule == "mb_rounded_to_half_gb" || amountRule == "gb" || amountRule == "mb" {
			rule = "unit_converted"
			confidence = 95
		}
		canonicalSKU1 := fmt.Sprintf("%d天", duration)
		canonicalSKU2 := dataTypeZH(dataType)
		if dataType != "unlimited" {
			canonicalSKU2 += " " + formatDataAmount(amountMB)
		}
		return skuSpec{DurationDays: duration, DataType: dataType, DataMB: amountMB, CanonicalSKU1: canonicalSKU1, CanonicalSKU2: canonicalSKU2, MatchKey: fmt.Sprintf("%dd|%s|%dmb", duration, dataType, amountMB), MatchRule: rule, Confidence: confidence, Valid: true}
	}
	fallback := strings.TrimSpace(sku1) + "|" + normalizeSKU2(sku2)
	return skuSpec{CanonicalSKU1: strings.TrimSpace(sku1), CanonicalSKU2: normalizeSKU2(sku2), MatchKey: "fallback|" + fallback, MatchRule: "normalized_text", Confidence: 60, Valid: false}
}

func buildURLToProductMap(xlsxPath string) map[string]string {
	f, err := excelize.OpenFile(xlsxPath)
	if err != nil {
		return nil
	}
	defer f.Close()
	sheet := f.GetSheetName(f.GetActiveSheetIndex())
	rows, err := f.GetRows(sheet)
	if err != nil || len(rows) < 2 {
		return nil
	}
	urlCol, pidCol := -1, -1
	for i, h := range rows[0] {
		if strings.TrimSpace(h) == "URL" {
			urlCol = i
		}
		if strings.TrimSpace(h) == "商品ID" {
			pidCol = i
		}
	}
	if urlCol < 0 || pidCol < 0 {
		return nil
	}
	m := map[string]string{}
	currentPID := ""
	for _, row := range rows[1:] {
		if pidCol < len(row) && strings.TrimSpace(row[pidCol]) != "" {
			currentPID = strings.TrimSpace(row[pidCol])
		}
		if urlCol < len(row) && currentPID != "" {
			if url := strings.TrimSpace(row[urlCol]); url != "" {
				m[normalizeURL(url)] = currentPID
			}
		}
	}
	return m
}

func normalizeURL(url string) string {
	url = strings.TrimSpace(url)
	url = urlCleanRe.ReplaceAllString(url, "")
	return strings.TrimRight(url, "/")
}
func findProductForURL(targetURL string, urlMap map[string]string) string {
	normalized := normalizeURL(targetURL)
	if pid, ok := urlMap[normalized]; ok {
		return pid
	}
	for url, pid := range urlMap {
		if strings.Contains(normalized, url) || strings.Contains(url, normalized) {
			return pid
		}
	}
	return ""
}

func CompareVendors(excelInput string, baseVendor string, outputDir string, stamp string, reporter Reporter) (*VendorComparisonResult, error) {
	groups, err := parseInputConfig(excelInput, baseVendor)
	if err != nil {
		return nil, fmt.Errorf("parse input config: %w", err)
	}
	if len(groups) == 0 {
		return nil, fmt.Errorf("no region groups found for base vendor %q", baseVendor)
	}
	reporter(Event{"event": "vendor_compare_start", "base_vendor": baseVendor, "region_count": len(groups), "sku_matching": "structured_spec"})

	vendorXLSX := map[string]string{}
	files, _ := filepath.Glob(filepath.Join(outputDir, "*.xlsx"))
	for _, f := range files {
		name := filepath.Base(f)
		if strings.Contains(name, "compare") || strings.Contains(name, "趋势") || strings.Contains(name, "总表") {
			continue
		}
		vendorName := strings.TrimSuffix(name, "_"+stamp+".xlsx")
		if vendorName != name {
			vendorXLSX[vendorName] = f
		}
	}
	allProducts := map[string]map[string][]productSKU{}
	allURLMaps := map[string]map[string]string{}
	for vendor, xlsx := range vendorXLSX {
		prods, err := loadVendorProducts(xlsx)
		if err != nil {
			reporter(Event{"event": "warning", "message": fmt.Sprintf("failed to load %s: %v", vendor, err)})
			continue
		}
		allProducts[vendor] = prods
		allURLMaps[vendor] = buildURLToProductMap(xlsx)
	}

	vendorOrder := buildVendorOrder(baseVendor, groups, vendorXLSX)
	allComparisons := []productComparison{}
	perVendor := map[string][]productComparison{}
	horizontalRows := []horizontalCompareRow{}
	perVendorRows := map[string][]horizontalCompareRow{}

	for _, group := range groups {
		basePID := findProductForURL(group.baseURL, allURLMaps[baseVendor])
		baseSKUs := allProducts[baseVendor][basePID]
		productSKUsByVendor := map[string][]productSKU{baseVendor: baseSKUs}
		productIDsByVendor := map[string]string{baseVendor: basePID}
		for compVendor, compURL := range group.competitorURLs {
			compPID := findProductForURL(compURL, allURLMaps[compVendor])
			if compPID == "" {
				continue
			}
			productIDsByVendor[compVendor] = compPID
			productSKUsByVendor[compVendor] = allProducts[compVendor][compPID]
		}

		rowsForGroup, compsForGroup := buildHorizontalRowsForGroup(group, baseVendor, vendorOrder, productSKUsByVendor, productIDsByVendor)
		horizontalRows = append(horizontalRows, rowsForGroup...)
		for vendor, rows := range filterRowsByCompetitor(rowsForGroup, baseVendor) {
			perVendorRows[vendor] = append(perVendorRows[vendor], rows...)
		}
		for _, c := range compsForGroup {
			allComparisons = append(allComparisons, c)
			perVendor[c.CompVendor] = append(perVendor[c.CompVendor], c)
		}
	}

	result := &VendorComparisonResult{BaseVendor: baseVendor, Comparisons: allComparisons, PerVendorSummary: map[string]*vendorSummary{}, OutputPath: filepath.Join(outputDir, fmt.Sprintf("vendor_compare_%s.xlsx", stamp))}
	for vendor, comps := range perVendor {
		result.PerVendorSummary[vendor] = summarizeVendor(vendor, len(groups), comps)
	}
	if err := writeCompareXLSX(result, horizontalRows, perVendorRows, vendorOrder); err != nil {
		reporter(Event{"event": "warning", "message": fmt.Sprintf("compare xlsx write failed: %v", err)})
	} else {
		reporter(Event{"event": "vendor_compare_done", "output": result.OutputPath, "comparisons": len(allComparisons), "layout": "horizontal", "sku_matching": "structured_spec", "extra_sheet": "单链接均价对比"})
	}
	writeCompareJSON(result, filepath.Join(outputDir, fmt.Sprintf("vendor_compare_%s.json", stamp)))
	return result, nil
}

func buildVendorOrder(baseVendor string, groups []regionGroup, vendorXLSX map[string]string) []string {
	seen := map[string]bool{baseVendor: true}
	order := []string{baseVendor}
	for _, g := range groups {
		for v := range g.competitorURLs {
			if !seen[v] && vendorXLSX[v] != "" {
				seen[v] = true
				order = append(order, v)
			}
		}
	}
	return order
}

type skuIndexEntry struct {
	SKU            productSKU
	CandidateCount int
}

func buildSKUIndex(skus []productSKU) map[string]skuIndexEntry {
	idx := map[string]skuIndexEntry{}
	for _, s := range skus {
		key := skuKey(s)
		entry, ok := idx[key]
		if !ok || (s.RealPrice > 0 && (entry.SKU.RealPrice == 0 || s.RealPrice < entry.SKU.RealPrice)) {
			entry.SKU = s
		}
		entry.CandidateCount++
		idx[key] = entry
	}
	return idx
}

func buildHorizontalRowsForGroup(group regionGroup, baseVendor string, vendorOrder []string, productSKUs map[string][]productSKU, productIDs map[string]string) ([]horizontalCompareRow, []productComparison) {
	idxByVendor := map[string]map[string]skuIndexEntry{}
	keySet := map[string]bool{}
	specByKey := map[string]skuSpec{}
	for _, vendor := range vendorOrder {
		idx := buildSKUIndex(productSKUs[vendor])
		idxByVendor[vendor] = idx
		for key, entry := range idx {
			keySet[key] = true
			if specByKey[key].MatchKey == "" || entry.SKU.Spec.Valid {
				specByKey[key] = entry.SKU.Spec
			}
		}
	}
	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return compareSpecSort(specByKey[keys[i]], specByKey[keys[j]], keys[i], keys[j]) < 0
	})

	// Precompute product-pair matched-only stats and min-delta placement.
	minDeltaByVendor := map[string]float64{}
	minDeltaKeyByVendor := map[string]string{}
	comparisons := []productComparison{}
	baseIdx := idxByVendor[baseVendor]
	for _, vendor := range vendorOrder {
		if vendor == baseVendor {
			continue
		}
		matchedKeys := []string{}
		for key := range idxByVendor[vendor] {
			if _, ok := baseIdx[key]; ok && isOfficialMatchKey(key) {
				matchedKeys = append(matchedKeys, key)
			}
		}
		if len(matchedKeys) == 0 {
			continue
		}
		sort.Slice(matchedKeys, func(i, j int) bool {
			return compareSpecSort(specByKey[matchedKeys[i]], specByKey[matchedKeys[j]], matchedKeys[i], matchedKeys[j]) < 0
		})
		basePrices, compPrices := []float64{}, []float64{}
		sumDelta := 0.0
		baseCheaper := 0
		compMin := math.MaxFloat64
		compMinKey := ""
		for _, key := range matchedKeys {
			bs, cs := baseIdx[key].SKU, idxByVendor[vendor][key].SKU
			d := cs.RealPrice - bs.RealPrice
			sumDelta += d
			if d > 0 {
				baseCheaper++
			}
			basePrices = append(basePrices, bs.RealPrice)
			compPrices = append(compPrices, cs.RealPrice)
			if cs.RealPrice < compMin {
				compMin = cs.RealPrice
				compMinKey = key
			}
		}
		baseMin := minFloat(basePrices)
		compMinVal := minFloat(compPrices)
		minDeltaByVendor[vendor] = compMinVal - baseMin
		minDeltaKeyByVendor[vendor] = compMinKey
		firstBase, firstComp := baseIdx[matchedKeys[0]].SKU, idxByVendor[vendor][matchedKeys[0]].SKU
		comparisons = append(comparisons, productComparison{Category: group.category, Destination: group.destination, BaseVendor: baseVendor, CompVendor: vendor, BaseProductID: productIDs[baseVendor], CompProductID: productIDs[vendor], BaseProdName: firstBase.ProductName, CompProdName: firstComp.ProductName, MatchCount: len(matchedKeys), AvgDelta: sumDelta / float64(len(matchedKeys)), MinDelta: compMinVal - baseMin, BaseCheaper: baseCheaper, BaseMinPrice: baseMin, CompMinPrice: compMinVal, BaseAvgPrice: avgFloat(basePrices), CompAvgPrice: avgFloat(compPrices)})
	}

	rows := []horizontalCompareRow{}
	for _, key := range keys {
		spec := specByKey[key]
		blocks := map[string]compareVendorBlock{}
		matched := 0
		baseEntry, hasBase := baseIdx[key]
		baseSKU := baseEntry.SKU
		for _, vendor := range vendorOrder {
			entry, ok := idxByVendor[vendor][key]
			if !ok {
				continue
			}
			sku := entry.SKU
			matched++
			block := compareVendorBlock{Vendor: vendor, SKU: &sku, CandidateCount: entry.CandidateCount}
			if vendor != baseVendor {
				if hasBase && isOfficialMatchKey(key) {
					d := sku.RealPrice - baseSKU.RealPrice
					block.Delta = &d
					if key == minDeltaKeyByVendor[vendor] {
						md := minDeltaByVendor[vendor]
						block.MinDelta = &md
					}
					if d > 0 {
						block.Conclusion = "基准更低"
					} else if d < 0 {
						block.Conclusion = vendor + "更低"
					} else {
						block.Conclusion = "持平"
					}
				} else if !hasBase {
					block.Conclusion = "基准无此SKU"
				}
			}
			blocks[vendor] = block
		}
		rows = append(rows, horizontalCompareRow{InputRow: group.rowIndex, Category: group.category, Destination: group.destination, DurationDays: spec.DurationDays, DataType: dataTypeZH(spec.DataType), DataMB: spec.DataMB, CanonicalSKU1: spec.CanonicalSKU1, CanonicalSKU2: spec.CanonicalSKU2, MatchRule: spec.MatchRule, Confidence: spec.Confidence, MatchedCount: matched, Blocks: blocks})
	}
	return rows, comparisons
}

func compareSpecSort(a, b skuSpec, ak, bk string) int {
	if a.DurationDays != b.DurationDays {
		if a.DurationDays < b.DurationDays {
			return -1
		}
		return 1
	}
	if a.DataType != b.DataType {
		if a.DataType < b.DataType {
			return -1
		}
		return 1
	}
	if a.DataMB != b.DataMB {
		if a.DataMB < b.DataMB {
			return -1
		}
		return 1
	}
	if ak < bk {
		return -1
	}
	if ak > bk {
		return 1
	}
	return 0
}

func isOfficialMatchKey(key string) bool {
	return !strings.HasPrefix(key, "fallback|")
}

func filterRowsByCompetitor(rows []horizontalCompareRow, baseVendor string) map[string][]horizontalCompareRow {
	out := map[string][]horizontalCompareRow{}
	for _, r := range rows {
		for vendor := range r.Blocks {
			if vendor != baseVendor {
				out[vendor] = append(out[vendor], r)
			}
		}
	}
	return out
}

func summarizeVendor(vendor string, totalRegions int, comps []productComparison) *vendorSummary {
	vs := &vendorSummary{Vendor: vendor, TotalRegions: totalRegions}
	totalSKUs, totalCheaper := 0, 0
	totalDelta := 0.0
	deltas := []regionDelta{}
	for _, c := range comps {
		if c.MatchCount > 0 {
			vs.MatchedRegions++
		}
		totalSKUs += c.MatchCount
		totalCheaper += c.BaseCheaper
		totalDelta += c.AvgDelta * float64(c.MatchCount)
		deltas = append(deltas, regionDelta{Category: c.Category, Destination: c.Destination, MatchCount: c.MatchCount, AvgDelta: c.AvgDelta, MinDelta: c.MinDelta, BaseCheaper: c.BaseCheaper, TotalSKUs: c.MatchCount, BaseMin: c.BaseMinPrice, CompMin: c.CompMinPrice})
	}
	vs.TotalMatchedSKUs = totalSKUs
	if totalSKUs > 0 {
		vs.AvgPriceDelta = totalDelta / float64(totalSKUs)
		vs.BaseCheaperPct = float64(totalCheaper) / float64(totalSKUs) * 100
	}
	sort.Slice(deltas, func(i, j int) bool { return deltas[i].AvgDelta > deltas[j].AvgDelta })
	vs.TopCheaper = append([]regionDelta(nil), deltas[:min(len(deltas), 5)]...)
	sort.Slice(deltas, func(i, j int) bool { return deltas[i].MinDelta > deltas[j].MinDelta })
	vs.TopExpensive = append([]regionDelta(nil), deltas[:min(len(deltas), 5)]...)
	return vs
}

func writeCompareXLSX(result *VendorComparisonResult, rows []horizontalCompareRow, perVendorRows map[string][]horizontalCompareRow, vendorOrder []string) error {
	f := excelize.NewFile()
	defer f.Close()
	writeSummarySheet(f, "汇总", result)
	writeHorizontalSheet(f, "总表_横向比价", rows, vendorOrder, result.BaseVendor)
	writePriceBoardSheet(f, "单链接均价对比", rows, vendorOrder, result.BaseVendor)
	for _, vendor := range vendorOrder {
		if vendor == result.BaseVendor {
			continue
		}
		if rs := perVendorRows[vendor]; len(rs) > 0 {
			writeHorizontalSheet(f, safeSheetName(vendor+"_vs_"+result.BaseVendor), rs, []string{result.BaseVendor, vendor}, result.BaseVendor)
		}
	}
	// Remove default empty Sheet1 if present and not renamed.
	if idx, err := f.GetSheetIndex("Sheet1"); err == nil && idx >= 0 {
		f.DeleteSheet("Sheet1")
	}
	return f.SaveAs(result.OutputPath)
}

func writeSummarySheet(f *excelize.File, sheet string, result *VendorComparisonResult) {
	f.SetSheetName(f.GetSheetName(f.GetActiveSheetIndex()), sheet)
	headers := []string{"竞对厂商", "地区数", "匹配地区", "匹配SKU数", "均价差(竞对-基准,仅匹配SKU)", "基准更优占比%"}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(sheet, cell, h)
	}
	vendors := make([]string, 0, len(result.PerVendorSummary))
	for v := range result.PerVendorSummary {
		vendors = append(vendors, v)
	}
	sort.Strings(vendors)
	row := 2
	for _, vendor := range vendors {
		vs := result.PerVendorSummary[vendor]
		vals := []interface{}{vendor, vs.TotalRegions, vs.MatchedRegions, vs.TotalMatchedSKUs, round2(vs.AvgPriceDelta), round1(vs.BaseCheaperPct)}
		for i, v := range vals {
			cell, _ := excelize.CoordinatesToCellName(i+1, row)
			f.SetCellValue(sheet, cell, v)
		}
		row++
	}
	setSheetDimension(f, sheet, 1, 1, max(1, len(headers)), max(1, row-1))
}

func vendorFields() []string {
	return []string{"商品ID", "商品名称", "原始SKU1", "原始SKU2", "货币", "外显价格", "实价", "促销标签", "预定数", "评价数", "评分", "同规格候选数", "相对基准实价差", "相对基准最低价差", "价格结论"}
}
func writeHorizontalSheet(f *excelize.File, sheet string, rows []horizontalCompareRow, vendorOrder []string, baseVendor string) {
	f.NewSheet(sheet)
	baseHeaders := []string{"输入行号", "分類", "目的地", "规范SKU层级1", "规范SKU层级2", "有效期天数", "流量类型", "流量MB", "匹配厂商数", "匹配规则", "匹配置信度", "基准厂商"}
	fields := vendorFields()
	col := 1
	for _, h := range baseHeaders {
		cell, _ := excelize.CoordinatesToCellName(col, 2)
		f.SetCellValue(sheet, cell, h)
		col++
	}
	for _, vendor := range vendorOrder {
		start := col
		end := col + len(fields) - 1
		startCell, _ := excelize.CoordinatesToCellName(start, 1)
		endCell, _ := excelize.CoordinatesToCellName(end, 1)
		f.MergeCell(sheet, startCell, endCell)
		f.SetCellValue(sheet, startCell, vendor)
		for _, h := range fields {
			cell, _ := excelize.CoordinatesToCellName(col, 2)
			f.SetCellValue(sheet, cell, h)
			col++
		}
	}
	for rIdx, r := range rows {
		rowNum := rIdx + 3
		values := []interface{}{r.InputRow, r.Category, r.Destination, r.CanonicalSKU1, r.CanonicalSKU2, r.DurationDays, r.DataType, r.DataMB, r.MatchedCount, r.MatchRule, r.Confidence, baseVendor}
		for i, v := range values {
			cell, _ := excelize.CoordinatesToCellName(i+1, rowNum)
			f.SetCellValue(sheet, cell, v)
		}
		col = 1 + len(baseHeaders)
		for _, vendor := range vendorOrder {
			writeVendorBlock(f, sheet, rowNum, col, r.Blocks[vendor])
			col += len(fields)
		}
	}
	f.SetPanes(sheet, &excelize.Panes{Freeze: true, Split: false, XSplit: 0, YSplit: 2, TopLeftCell: "A3", ActivePane: "bottomLeft"})
	styleHeader, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}, Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"}})
	lastCell, _ := excelize.CoordinatesToCellName(col-1, 2)
	f.SetCellStyle(sheet, "A1", lastCell, styleHeader)
	for c := 1; c < col; c++ {
		name, _ := excelize.ColumnNumberToName(c)
		width := 12.0
		if c == 3 || c == 5 {
			width = 24
		}
		f.SetColWidth(sheet, name, name, width)
	}
	setSheetDimension(f, sheet, 1, 1, col-1, len(rows)+2)
}

func writeVendorBlock(f *excelize.File, sheet string, row int, startCol int, block compareVendorBlock) {
	vals := make([]interface{}, len(vendorFields()))
	if block.SKU != nil {
		s := block.SKU
		vals = []interface{}{s.ProductID, truncate(s.ProductName, 60), s.SKU1, s.SKU2, s.Currency, s.DisplayPrice, s.RealPrice, s.PromoTag, s.Bookings, s.Reviews, s.Rating, block.CandidateCount, "", "", block.Conclusion}
		if block.Delta != nil {
			vals[12] = round2(*block.Delta)
		}
		if block.MinDelta != nil {
			vals[13] = round2(*block.MinDelta)
		}
	}
	for i, v := range vals {
		cell, _ := excelize.CoordinatesToCellName(startCol+i, row)
		f.SetCellValue(sheet, cell, v)
	}
}

type priceBoardGroup struct {
	InputRow    int
	Category    string
	Destination string
	Rows        []horizontalCompareRow
}

type destinationVendorStats struct {
	Vendor             string
	ComparableSKUCount int
	CoverageRate       float64
	VendorAvgPrice     float64
	BaseAvgPrice       float64
	AvgDeltaVsBase     float64
	AvgDeltaPctVsBase  float64
	VendorCheaperCount int
	BaseCheaperCount   int
	TieCount           int
	VendorOnlySKUCount int
	BaseOnlySKUCount   int
	GlobalComparable   int
	GlobalAvgPrice     float64
	GlobalRank         int
	Conclusion         string
}

func writePriceBoardSheet(f *excelize.File, sheet string, rows []horizontalCompareRow, vendorOrder []string, baseVendor string) {
	f.NewSheet(sheet)
	baseHeaders := []string{"输入行号", "分類", "目的地", "基准厂商", "参与厂商数", "有重合SKU的竞对数", "总SKU规格数", "基准SKU数", "竞对独有SKU数", "基准独有SKU数", "综合最低均价厂商", "综合最低均价", "综合最高均价厂商", "综合最高均价", "最高-最低均价差", "基准综合均价", "基准综合排名", "基准是否综合最低", "基准较最低均价差", "基准较最低均价差%"}
	vendorFields := []string{"重合SKU数", "重合SKU覆盖率%", "竞对重合SKU均价", "基准重合SKU均价", "相对基准均价差", "相对基准均价差%", "vs基准结论", "竞对低于基准SKU数", "基准低于竞对SKU数", "持平SKU数", "竞对独有SKU数", "基准独有SKU数", "综合均价", "综合排名"}
	col := 1
	for _, h := range baseHeaders {
		cell, _ := excelize.CoordinatesToCellName(col, 1)
		f.SetCellValue(sheet, cell, h)
		col++
	}
	for _, vendor := range vendorOrder {
		if vendor == baseVendor {
			continue
		}
		for _, h := range vendorFields {
			cell, _ := excelize.CoordinatesToCellName(col, 1)
			f.SetCellValue(sheet, cell, vendor+"_"+h)
			col++
		}
	}

	groups := buildPriceBoardGroups(rows)
	for rIdx, group := range groups {
		rowNum := rIdx + 2
		statsByVendor, globalAvgByVendor, globalRankByVendor := buildDestinationStats(group, vendorOrder, baseVendor)
		participantCount := 0
		for _, vendor := range vendorOrder {
			if globalAvgByVendor[vendor] > 0 || countVendorRows(group.Rows, vendor) > 0 {
				participantCount++
			}
		}
		competitorsWithOverlap := 0
		competitorOnlyTotal, baseOnlyTotal := 0, 0
		for _, vendor := range vendorOrder {
			if vendor == baseVendor {
				continue
			}
			st := statsByVendor[vendor]
			if st.ComparableSKUCount > 0 {
				competitorsWithOverlap++
			}
			competitorOnlyTotal += st.VendorOnlySKUCount
			baseOnlyTotal += st.BaseOnlySKUCount
		}
		minVendor, maxVendor := "", ""
		minAvg, maxAvg := 0.0, 0.0
		for _, vendor := range vendorOrder {
			avg := globalAvgByVendor[vendor]
			if avg <= 0 {
				continue
			}
			if minAvg == 0 || avg < minAvg {
				minAvg, minVendor = avg, vendor
			}
			if avg > maxAvg {
				maxAvg, maxVendor = avg, vendor
			}
		}
		baseGlobalAvg := globalAvgByVendor[baseVendor]
		baseRank := globalRankByVendor[baseVendor]
		baseIsLowest := ""
		baseGap, baseGapPct := interface{}(""), interface{}("")
		if baseGlobalAvg > 0 && minAvg > 0 {
			baseIsLowest = yesNo(almostEqual(baseGlobalAvg, minAvg))
			d := baseGlobalAvg - minAvg
			baseGap = round2(d)
			baseGapPct = fmt.Sprintf("%.1f%%", d/minAvg*100)
		}
		vals := []interface{}{group.InputRow, group.Category, group.Destination, baseVendor, participantCount, competitorsWithOverlap, len(group.Rows), countVendorRows(group.Rows, baseVendor), competitorOnlyTotal, baseOnlyTotal, minVendor, emptyIfZero(minAvg), maxVendor, emptyIfZero(maxAvg), emptyIfZero(maxAvg - minAvg), emptyIfZero(baseGlobalAvg), emptyIfZeroFloatAsInt(baseRank), baseIsLowest, baseGap, baseGapPct}
		for i, v := range vals {
			cell, _ := excelize.CoordinatesToCellName(i+1, rowNum)
			f.SetCellValue(sheet, cell, v)
		}
		col = 1 + len(baseHeaders)
		for _, vendor := range vendorOrder {
			if vendor == baseVendor {
				continue
			}
			st := statsByVendor[vendor]
			vals := []interface{}{emptyIfZeroFloatAsInt(st.ComparableSKUCount), emptyIfZero(st.CoverageRate), emptyIfZero(st.VendorAvgPrice), emptyIfZero(st.BaseAvgPrice), emptyIfZeroAllowNegative(st.AvgDeltaVsBase, st.ComparableSKUCount > 0), emptyPct(st.AvgDeltaPctVsBase, st.ComparableSKUCount > 0), st.Conclusion, emptyIfZeroFloatAsInt(st.VendorCheaperCount), emptyIfZeroFloatAsInt(st.BaseCheaperCount), emptyIfZeroFloatAsInt(st.TieCount), emptyIfZeroFloatAsInt(st.VendorOnlySKUCount), emptyIfZeroFloatAsInt(st.BaseOnlySKUCount), emptyIfZero(globalAvgByVendor[vendor]), emptyIfZeroFloatAsInt(globalRankByVendor[vendor])}
			for i, v := range vals {
				cell, _ := excelize.CoordinatesToCellName(col+i, rowNum)
				f.SetCellValue(sheet, cell, v)
			}
			col += len(vendorFields)
		}
	}
	f.SetPanes(sheet, &excelize.Panes{Freeze: true, Split: false, XSplit: 0, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
	styleHeader, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}, Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"}})
	lastCell, _ := excelize.CoordinatesToCellName(col-1, 1)
	f.SetCellStyle(sheet, "A1", lastCell, styleHeader)
	for c := 1; c < col; c++ {
		name, _ := excelize.ColumnNumberToName(c)
		width := 12.0
		if c == 3 || c == 11 || c == 13 || c >= 21 {
			width = 18
		}
		f.SetColWidth(sheet, name, name, width)
	}
	setSheetDimension(f, sheet, 1, 1, col-1, len(groups)+1)
}

func buildPriceBoardGroups(rows []horizontalCompareRow) []priceBoardGroup {
	idx := map[int]int{}
	groups := []priceBoardGroup{}
	for _, r := range rows {
		pos, ok := idx[r.InputRow]
		if !ok {
			idx[r.InputRow] = len(groups)
			groups = append(groups, priceBoardGroup{InputRow: r.InputRow, Category: r.Category, Destination: r.Destination})
			pos = len(groups) - 1
		}
		groups[pos].Rows = append(groups[pos].Rows, r)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].InputRow < groups[j].InputRow })
	return groups
}

func buildDestinationStats(group priceBoardGroup, vendorOrder []string, baseVendor string) (map[string]destinationVendorStats, map[string]float64, map[string]int) {
	stats := map[string]destinationVendorStats{}
	globalPrices := map[string][]float64{}
	for _, r := range group.Rows {
		if r.Confidence < 90 {
			continue
		}
		available := []string{}
		for _, vendor := range vendorOrder {
			if b := r.Blocks[vendor]; b.SKU != nil && b.SKU.RealPrice > 0 {
				available = append(available, vendor)
			}
		}
		if len(available) >= 2 {
			for _, vendor := range available {
				globalPrices[vendor] = append(globalPrices[vendor], r.Blocks[vendor].SKU.RealPrice)
			}
		}
		baseBlock := r.Blocks[baseVendor]
		baseHas := baseBlock.SKU != nil && baseBlock.SKU.RealPrice > 0
		for _, vendor := range vendorOrder {
			if vendor == baseVendor {
				continue
			}
			st := stats[vendor]
			st.Vendor = vendor
			vb := r.Blocks[vendor]
			vendorHas := vb.SKU != nil && vb.SKU.RealPrice > 0
			if baseHas && vendorHas {
				st.ComparableSKUCount++
				st.BaseAvgPrice += baseBlock.SKU.RealPrice
				st.VendorAvgPrice += vb.SKU.RealPrice
				d := vb.SKU.RealPrice - baseBlock.SKU.RealPrice
				if d < 0 {
					st.VendorCheaperCount++
				} else if d > 0 {
					st.BaseCheaperCount++
				} else {
					st.TieCount++
				}
			} else if baseHas && !vendorHas {
				st.BaseOnlySKUCount++
			} else if !baseHas && vendorHas {
				st.VendorOnlySKUCount++
			}
			stats[vendor] = st
		}
	}
	for vendor, st := range stats {
		if st.ComparableSKUCount > 0 {
			st.BaseAvgPrice = st.BaseAvgPrice / float64(st.ComparableSKUCount)
			st.VendorAvgPrice = st.VendorAvgPrice / float64(st.ComparableSKUCount)
			st.AvgDeltaVsBase = st.VendorAvgPrice - st.BaseAvgPrice
			if st.BaseAvgPrice > 0 {
				st.AvgDeltaPctVsBase = st.AvgDeltaVsBase / st.BaseAvgPrice * 100
			}
			st.CoverageRate = float64(st.ComparableSKUCount) / float64(max(1, countVendorRows(group.Rows, vendor))) * 100
			if st.AvgDeltaVsBase < 0 {
				st.Conclusion = vendor + "整体更低"
			} else if st.AvgDeltaVsBase > 0 {
				st.Conclusion = "基准整体更低"
			} else {
				st.Conclusion = "均价持平"
			}
			if st.CoverageRate < 30 {
				st.Conclusion += "；覆盖率过低需谨慎"
			}
		} else {
			st.Conclusion = "无重合SKU不可比"
		}
		stats[vendor] = st
	}
	globalAvg := map[string]float64{}
	for vendor, prices := range globalPrices {
		globalAvg[vendor] = avgFloat(prices)
	}
	globalRank := rankVendorAverages(vendorOrder, globalAvg)
	return stats, globalAvg, globalRank
}

func countVendorRows(rows []horizontalCompareRow, vendor string) int {
	count := 0
	for _, r := range rows {
		if r.Confidence < 90 {
			continue
		}
		if b := r.Blocks[vendor]; b.SKU != nil && b.SKU.RealPrice > 0 {
			count++
		}
	}
	return count
}

func rankVendorAverages(vendorOrder []string, avgByVendor map[string]float64) map[string]int {
	type item struct {
		Vendor string
		Avg    float64
	}
	items := []item{}
	for _, vendor := range vendorOrder {
		if avgByVendor[vendor] > 0 {
			items = append(items, item{Vendor: vendor, Avg: avgByVendor[vendor]})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Avg < items[j].Avg })
	ranks := map[string]int{}
	rank := 1
	last := -1.0
	for i, it := range items {
		if i == 0 || !almostEqual(it.Avg, last) {
			rank = i + 1
			last = it.Avg
		}
		ranks[it.Vendor] = rank
	}
	return ranks
}

func rankPrice(blocks []compareVendorBlock, vendor string) int {
	rank := 1
	lastPrice := -1.0
	for i, b := range blocks {
		if i == 0 || !almostEqual(b.SKU.RealPrice, lastPrice) {
			rank = i + 1
			lastPrice = b.SKU.RealPrice
		}
		if b.Vendor == vendor {
			return rank
		}
	}
	return 0
}

func almostEqual(a, b float64) bool { return math.Abs(a-b) < 0.0001 }
func yesNo(v bool) string {
	if v {
		return "是"
	}
	return "否"
}
func emptyIfZero(v float64) interface{} {
	if v == 0 {
		return ""
	}
	return round2(v)
}
func emptyIfZeroAllowNegative(v float64, ok bool) interface{} {
	if !ok {
		return ""
	}
	return round2(v)
}
func emptyPct(v float64, ok bool) interface{} {
	if !ok {
		return ""
	}
	return fmt.Sprintf("%.1f%%", v)
}
func emptyIfZeroFloatAsInt(v int) interface{} {
	if v == 0 {
		return ""
	}
	return v
}

func setSheetDimension(f *excelize.File, sheet string, minCol, minRow, maxCol, maxRow int) {
	start, _ := excelize.CoordinatesToCellName(minCol, minRow)
	end, _ := excelize.CoordinatesToCellName(maxCol, maxRow)
	_ = f.SetSheetDimension(sheet, start+":"+end)
}

func writeCompareJSON(result *VendorComparisonResult, path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, `{"base_vendor":%q,"output":%q,"layout":"horizontal","sku_matching":"structured_spec","matched_only":true,"extra_sheets":["单链接均价对比"],"price_board":{"layout":"one_row_per_input_destination","metric":"matched_sku_avg_price"},"vendors":{`, result.BaseVendor, result.OutputPath)
	first := true
	vendors := make([]string, 0, len(result.PerVendorSummary))
	for v := range result.PerVendorSummary {
		vendors = append(vendors, v)
	}
	sort.Strings(vendors)
	for _, vendor := range vendors {
		vs := result.PerVendorSummary[vendor]
		if !first {
			fmt.Fprint(f, ",")
		}
		first = false
		fmt.Fprintf(f, `%q:{"matched_regions":%d,"matched_skus":%d,"avg_delta":%.2f,"base_cheaper_pct":%.1f}`, vendor, vs.MatchedRegions, vs.TotalMatchedSKUs, vs.AvgPriceDelta, vs.BaseCheaperPct)
	}
	fmt.Fprint(f, "}}")
}

func safeSheetName(s string) string {
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	if len([]rune(s)) > 31 {
		return string([]rune(s)[:31])
	}
	return s
}
func truncate(s string, maxLen int) string {
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	return string(r[:maxLen-1]) + "…"
}
func minFloat(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	m := vals[0]
	for _, v := range vals[1:] {
		if v < m {
			m = v
		}
	}
	return m
}
func avgFloat(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}
func round2(v float64) float64 { return math.Round(v*100) / 100 }
func round1(v float64) float64 { return math.Round(v*10) / 10 }
