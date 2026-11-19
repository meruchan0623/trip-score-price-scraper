package tripcdp

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (s *tripScraper) waitCapturedAPISKUPrices(ctx context.Context, maxWait time.Duration) ([]map[string]string, apiSKUPriceCoverage, int) {
	started := time.Now()
	deadline := started.Add(maxWait)
	var rows []map[string]string
	var coverage apiSKUPriceCoverage
	for {
		rows, coverage = parseCapturedAPISKUPrices(s.capturedAPIResponses(), s.request.Market.Currency, s.request.ExtractRealPrice)
		if coverage.usable() || time.Now().After(deadline) || ctx.Err() != nil {
			return rows, coverage, elapsedMS(started)
		}
		if !sleepFixed(ctx, 150*time.Millisecond) {
			return rows, coverage, elapsedMS(started)
		}
	}
}

const priceSourceProductPriceCalendar = "getProductPriceCalendar"
const priceSourceResourceMinPrice = "getProductPriceCalendar.resourceInfos.minPrice"

func priceSourceFor(price apiSKUPrice) string {
	if price.FromResourceMinPrice {
		return priceSourceResourceMinPrice
	}
	return priceSourceProductPriceCalendar
}

type apiSKUPriceCoverage struct {
	TreeCount               int
	PricedCount             int
	MissingPriceCount       int
	ProductInfoLeafCount    int
	CalendarLeafCount       int
	ExactKeyOverlapCount    int
	PricedCalendarCount     int
	ZeroOriginalOrRealCount int
	CurrencyMismatchCount   int
	Complete                bool
}

func (c apiSKUPriceCoverage) usable() bool {
	if c.TreeCount <= 0 || c.PricedCount <= 0 || c.PricedCount > c.TreeCount {
		return false
	}
	return c.PricedCount == c.TreeCount
}

type apiSKUProperty struct {
	PropertyID   int64
	PropertyName string
	ValueID      int64
	ValueName    string
}

type apiSKUTreeLeaf struct {
	PackageID  int64
	ResourceID int64
	Properties []apiSKUProperty
}

type apiSKUPrice struct {
	PackageID     int64
	ResourceID    int64
	Properties    []apiSKUProperty
	Currency      string
	OriginalPrice float64
	Price         float64
	PromoTags     []string
	// FromResourceMinPrice marks a price taken from resourceInfos.minPrice because the
	// package had no priceInfo object at all (observed on BC 57042119, Nov 2026).
	FromResourceMinPrice bool
}

type apiSKUKey struct {
	PackageID  int64
	ResourceID int64
}

// capturedAPISKULeafCount detects API-declared SKU packages independently of
// price-calendar coverage, so they cannot be downgraded to a no-SKU fallback.
func capturedAPISKULeafCount(responses []capturedAPIResponse) int {
	seen := map[apiSKUKey]struct{}{}
	for _, response := range responses {
		if response.Status < 200 || response.Status >= 300 || strings.TrimSpace(response.Text) == "" {
			continue
		}
		for _, leaf := range capturedResponseSKUTree(response) {
			if leaf.PackageID > 0 {
				seen[apiSKUKey{PackageID: leaf.PackageID, ResourceID: leaf.ResourceID}] = struct{}{}
			}
		}
	}
	return len(seen)
}

// The calendar declares concrete packages and resources even when the separate
// product-info request is unavailable. Keep unpriced leaves in the coverage boundary.
func capturedResponseSKUTree(response capturedAPIResponse) []apiSKUTreeLeaf {
	if strings.Contains(response.URL, "/restapi/soa2/21052/getProductInfo") {
		return parseAPISKUTree(response.Text)
	}
	if !strings.Contains(response.URL, "/restapi/soa2/14580/getProductPriceCalendar") {
		return nil
	}
	var leaves []apiSKUTreeLeaf
	for _, price := range parseAPIPriceCalendar(response.Text) {
		if price.PackageID > 0 && price.ResourceID > 0 {
			leaves = append(leaves, apiSKUTreeLeaf{PackageID: price.PackageID, ResourceID: price.ResourceID, Properties: price.Properties})
		}
	}
	return leaves
}

func parseCapturedAPISKUPrices(responses []capturedAPIResponse, expectedCurrency string, extractRealPrice bool) ([]map[string]string, apiSKUPriceCoverage) {
	productInfo := map[apiSKUKey]apiSKUTreeLeaf{}
	calendar := map[apiSKUKey]apiSKUPrice{}
	prices := map[apiSKUKey]apiSKUPrice{}
	currency := strings.ToUpper(strings.TrimSpace(expectedCurrency))
	if currency == "" {
		currency = "HKD"
	}
	for _, response := range responses {
		if response.Status < 200 || response.Status >= 300 || strings.TrimSpace(response.Text) == "" {
			continue
		}
		switch {
		case strings.Contains(response.URL, "/restapi/soa2/21052/getProductInfo"):
			for _, leaf := range parseAPISKUTree(response.Text) {
				if leaf.PackageID > 0 && leaf.ResourceID > 0 {
					productInfo[apiSKUKey{PackageID: leaf.PackageID, ResourceID: leaf.ResourceID}] = leaf
				}
			}
		case strings.Contains(response.URL, "/restapi/soa2/14580/getProductPriceCalendar"):
			for _, price := range parseAPIPriceCalendar(response.Text) {
				if price.PackageID <= 0 || price.ResourceID <= 0 {
					continue
				}
				key := apiSKUKey{PackageID: price.PackageID, ResourceID: price.ResourceID}
				calendar[key] = price
				if price.Currency != "" && !strings.EqualFold(price.Currency, currency) {
					continue
				}
				if price.PackageID > 0 && price.ResourceID > 0 && price.OriginalPrice > 0 && price.Price > 0 {
					prices[key] = price
				}
			}
		}
	}

	// The current price calendar declares the currently saleable package/resource
	// boundary. Product info may include unavailable packages, so use it only to
	// enrich matching calendar leaves with property names.
	keys := make([]apiSKUKey, 0, len(calendar))
	for key := range calendar {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].PackageID == keys[j].PackageID {
			return keys[i].ResourceID < keys[j].ResourceID
		}
		return keys[i].PackageID < keys[j].PackageID
	})

	rows := make([]map[string]string, 0, len(prices))
	pricedTreeLeaves := 0
	for _, key := range keys {
		leaf, hasProductInfo := productInfo[key]
		price, ok := prices[key]
		if !ok {
			continue
		}
		pricedTreeLeaves++
		properties := price.Properties
		if hasProductInfo && len(leaf.Properties) > 0 {
			properties = leaf.Properties
		}
		if len(properties) == 0 {
			properties = price.Properties
		}
		parts := apiSKUDisplayValues(properties)
		combination := strings.Join(parts, " | ")
		promoTag := strings.Join(price.PromoTags, " | ")
		promoMatched := promoTag != "" || price.Price < price.OriginalPrice
		status := priceStatusTrustedBase
		if promoMatched {
			status = priceStatusTrustedPromo
		}
		realCurrency, realPrice := "N/A", "N/A"
		if extractRealPrice {
			realCurrency = currency
			realPrice = formatPriceNumber(price.Price)
		}
		rows = append(rows, map[string]string{
			"combination":        combination,
			"selected_texts":     combination,
			"currency":           currency,
			"price":              formatPriceNumber(price.OriginalPrice),
			"real_currency":      realCurrency,
			"real_price":         realPrice,
			"promo_tag":          promoTag,
			"package_id":         strconv.FormatInt(key.PackageID, 10),
			"resource_id":        strconv.FormatInt(price.ResourceID, 10),
			"item_id":            strconv.FormatInt(price.ResourceID, 10),
			"promo_matched":      strconv.FormatBool(promoMatched),
			"price_status":       status,
			"price_source":       priceSourceFor(price),
			"raw_captured_price": formatPriceNumber(price.OriginalPrice),
		})
	}
	overlap := 0
	for key := range calendar {
		if _, ok := productInfo[key]; ok {
			overlap++
		}
	}
	zeroPrice, currencyMismatch := 0, 0
	for _, price := range calendar {
		if price.OriginalPrice <= 0 || price.Price <= 0 {
			zeroPrice++
		}
		if price.Currency != "" && !strings.EqualFold(price.Currency, currency) {
			currencyMismatch++
		}
	}
	coverage := apiSKUPriceCoverage{
		TreeCount:               len(calendar),
		PricedCount:             pricedTreeLeaves,
		MissingPriceCount:       max(0, len(calendar)-pricedTreeLeaves),
		ProductInfoLeafCount:    len(productInfo),
		CalendarLeafCount:       len(calendar),
		ExactKeyOverlapCount:    overlap,
		PricedCalendarCount:     len(prices),
		ZeroOriginalOrRealCount: zeroPrice,
		CurrencyMismatchCount:   currencyMismatch,
	}
	coverage.Complete = coverage.TreeCount > 0 && coverage.MissingPriceCount == 0
	return rows, coverage
}

func parseAPISKUTree(raw string) []apiSKUTreeLeaf {
	var payload struct {
		ProductInfos []struct {
			BasicInfo struct {
				Packages []struct {
					PackageID       int64   `json:"packageId"`
					ResourceIDs     []int64 `json:"resourceIds"`
					SalesProperties []struct {
						SaleProperty struct {
							ID   int64  `json:"id"`
							Name string `json:"name"`
						} `json:"saleProperty"`
						SalePropertyValues []struct {
							ID   int64  `json:"id"`
							Name string `json:"name"`
						} `json:"salePropertyValues"`
					} `json:"salesProperties"`
				} `json:"packages"`
			} `json:"basicInfo"`
		} `json:"productInfos"`
	}
	if json.Unmarshal([]byte(raw), &payload) != nil || len(payload.ProductInfos) == 0 {
		return nil
	}
	out := make([]apiSKUTreeLeaf, 0, len(payload.ProductInfos[0].BasicInfo.Packages))
	for _, item := range payload.ProductInfos[0].BasicInfo.Packages {
		leaf := apiSKUTreeLeaf{PackageID: item.PackageID}
		for _, property := range item.SalesProperties {
			for _, value := range property.SalePropertyValues {
				leaf.Properties = append(leaf.Properties, apiSKUProperty{PropertyID: property.SaleProperty.ID, PropertyName: property.SaleProperty.Name, ValueID: value.ID, ValueName: value.Name})
			}
		}
		if len(item.ResourceIDs) == 0 {
			out = append(out, leaf)
			continue
		}
		for _, resourceID := range item.ResourceIDs {
			resourceLeaf := leaf
			resourceLeaf.ResourceID = resourceID
			out = append(out, resourceLeaf)
		}
	}
	return out
}

func parseAPIPriceCalendar(raw string) []apiSKUPrice {
	var payload struct {
		Data struct {
			PackageInfos []struct {
				PackageID       int64 `json:"packageId"`
				SalesProperties []struct {
					SalesProperty struct {
						ID   int64  `json:"salesPropertyId"`
						Name string `json:"salesPropertyName"`
					} `json:"salesProperty"`
					SalesPropertyValues []struct {
						ID   int64  `json:"salesPropertyId"`
						Name string `json:"salesPropertyName"`
					} `json:"salesPropertyValues"`
				} `json:"salesProperties"`
				ResourceInfos []struct {
					ResourceID  int64   `json:"resourceId"`
					MinPrice    float64 `json:"minPrice"`
					MarketPrice float64 `json:"marketPrice"`
				} `json:"resourceInfos"`
				PriceInfo *struct {
					Currency                 string  `json:"currency"`
					CurrencyCode             string  `json:"currencyCode"`
					OriginalPrice            float64 `json:"originalPrice"`
					Price                    float64 `json:"price"`
					PreferentialPriceDetails []struct {
						Title string `json:"preferentialTitle"`
						Name  string `json:"preferentialName"`
					} `json:"preferentialPriceDetails"`
				} `json:"priceInfo"`
			} `json:"packageInfos"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(raw), &payload) != nil {
		return nil
	}
	out := make([]apiSKUPrice, 0, len(payload.Data.PackageInfos))
	for _, item := range payload.Data.PackageInfos {
		price := apiSKUPrice{PackageID: item.PackageID}
		if item.PriceInfo != nil {
			price.Currency = strings.ToUpper(strings.TrimSpace(firstNonEmpty(item.PriceInfo.Currency, item.PriceInfo.CurrencyCode)))
			price.OriginalPrice = item.PriceInfo.OriginalPrice
			price.Price = item.PriceInfo.Price
		}
		for _, property := range item.SalesProperties {
			for _, value := range property.SalesPropertyValues {
				price.Properties = append(price.Properties, apiSKUProperty{PropertyID: property.SalesProperty.ID, PropertyName: property.SalesProperty.Name, ValueID: value.ID, ValueName: value.Name})
			}
		}
		var promos []struct {
			Title string `json:"preferentialTitle"`
			Name  string `json:"preferentialName"`
		}
		if item.PriceInfo != nil {
			promos = item.PriceInfo.PreferentialPriceDetails
		}
		for _, promo := range promos {
			if text := strings.TrimSpace(strings.Join([]string{strings.TrimSpace(promo.Title), strings.TrimSpace(promo.Name)}, " ")); text != "" {
				price.PromoTags = append(price.PromoTags, text)
			}
		}
		for _, resource := range item.ResourceInfos {
			resourcePrice := price
			resourcePrice.ResourceID = resource.ResourceID
			// Only when the package carries no priceInfo at all: the resource's own
			// minPrice is the displayed per-unit price. Explicit zero priceInfo stays untrusted.
			if item.PriceInfo == nil && resource.MinPrice > 0 {
				resourcePrice.Price = resource.MinPrice
				resourcePrice.OriginalPrice = resource.MinPrice
				if resource.MarketPrice > resource.MinPrice {
					resourcePrice.OriginalPrice = resource.MarketPrice
				}
				resourcePrice.FromResourceMinPrice = true
			}
			out = append(out, resourcePrice)
		}
	}
	return out
}

func apiSKUDisplayValues(properties []apiSKUProperty) []string {
	out := make([]string, 0, len(properties))
	for _, property := range properties {
		name := strings.ToLower(strings.TrimSpace(property.PropertyName))
		if name == "取卡方式" || strings.Contains(name, "pickup") || strings.Contains(name, "delivery method") {
			continue
		}
		if value := strings.TrimSpace(property.ValueName); value != "" {
			out = append(out, value)
		}
	}
	return out
}

// calendarResponseDiagnostics summarizes captured price-calendar responses without
// copying request headers, cookies or response text, so an incomplete result can be
// attributed to missing fields instead of a generic coverage failure.
func calendarResponseDiagnostics(responses []capturedAPIResponse) []map[string]any {
	var out []map[string]any
	for _, response := range responses {
		if !strings.Contains(response.URL, "/restapi/soa2/14580/getProductPriceCalendar") {
			continue
		}
		item := map[string]any{"http_status": response.Status, "body_bytes": len(response.Text)}
		var payload struct {
			Data struct {
				PackageInfos []struct {
					ResourceInfos []struct{} `json:"resourceInfos"`
					PriceInfo     *struct {
						Currency      string  `json:"currency"`
						CurrencyCode  string  `json:"currencyCode"`
						OriginalPrice float64 `json:"originalPrice"`
						Price         float64 `json:"price"`
					} `json:"priceInfo"`
				} `json:"packageInfos"`
			} `json:"data"`
			ResponseStatus struct {
				Ack string `json:"Ack"`
			} `json:"ResponseStatus"`
			ResultCode any `json:"resultCode"`
			ResultMsg  any `json:"resultMessage"`
			ErrorCode  any `json:"errorCode"`
			ErrorMsg   any `json:"errorMessage"`
		}
		if err := json.Unmarshal([]byte(response.Text), &payload); err != nil {
			item["json_error"] = true
			out = append(out, item)
			continue
		}
		withPriceInfo, positiveReal, positiveOriginal, withResource := 0, 0, 0, 0
		currencies := map[string]int{}
		for _, pkg := range payload.Data.PackageInfos {
			if len(pkg.ResourceInfos) > 0 {
				withResource++
			}
			if pkg.PriceInfo == nil {
				continue
			}
			withPriceInfo++
			if pkg.PriceInfo.Price > 0 {
				positiveReal++
			}
			if pkg.PriceInfo.OriginalPrice > 0 {
				positiveOriginal++
			}
			currencies[strings.ToUpper(firstNonEmpty(pkg.PriceInfo.Currency, pkg.PriceInfo.CurrencyCode))]++
		}
		var keys struct {
			Data struct {
				PackageInfos []map[string]json.RawMessage `json:"packageInfos"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(response.Text), &keys) == nil && len(keys.Data.PackageInfos) > 0 {
			names := make([]string, 0, len(keys.Data.PackageInfos[0]))
			for name := range keys.Data.PackageInfos[0] {
				names = append(names, name)
			}
			sort.Strings(names)
			item["first_package_fields"] = names
			var firstRes []map[string]json.RawMessage
			if json.Unmarshal(keys.Data.PackageInfos[0]["resourceInfos"], &firstRes) == nil && len(firstRes) > 0 {
				item["first_resource_price_sample"] = map[string]string{"minPrice": string(firstRes[0]["minPrice"]), "marketPrice": string(firstRes[0]["marketPrice"]), "priceSettingType": string(keys.Data.PackageInfos[0]["priceSettingType"]), "pricingUnit": string(keys.Data.PackageInfos[0]["pricingUnit"])}
			}
			for _, nested := range []string{"resourceInfos", "summaryInfos"} {
				var arr []map[string]json.RawMessage
				if json.Unmarshal(keys.Data.PackageInfos[0][nested], &arr) == nil && len(arr) > 0 {
					sub := make([]string, 0, len(arr[0]))
					for name := range arr[0] {
						sub = append(sub, name)
					}
					sort.Strings(sub)
					item["first_"+nested+"_fields"] = sub
				}
			}
		}
		var top map[string]json.RawMessage
		if json.Unmarshal([]byte(response.Text), &top) == nil {
			names := []string{}
			for name := range top {
				names = append(names, name)
			}
			sort.Strings(names)
			item["top_fields"] = names
			var data map[string]json.RawMessage
			if json.Unmarshal(top["data"], &data) == nil {
				dn := []string{}
				for name := range data {
					dn = append(dn, name)
				}
				sort.Strings(dn)
				item["data_fields"] = dn
				var ps []map[string]json.RawMessage
				if json.Unmarshal(data["priceAndStockInfos"], &ps) == nil && len(ps) > 0 {
					item["price_and_stock_count"] = len(ps)
					pn := []string{}
					for name := range ps[0] {
						pn = append(pn, name)
					}
					sort.Strings(pn)
					item["price_and_stock_fields"] = pn
					for _, k := range []string{"currency", "currencyCode", "price", "originalPrice", "date", "resourceId"} {
						if v, ok := ps[0][k]; ok {
							item["pas_first_"+k] = string(v)
						}
					}
				}
				for _, k := range []string{"currency", "currencyCode", "priceCurrency"} {
					if v, ok := data[k]; ok {
						item["data_"+k] = string(v)
					}
				}
			}
		}
		item["package_infos"] = len(payload.Data.PackageInfos)
		item["with_resource"] = withResource
		item["with_price_info"] = withPriceInfo
		item["positive_price"] = positiveReal
		item["positive_original_price"] = positiveOriginal
		item["currencies"] = currencies
		item["ack"] = payload.ResponseStatus.Ack
		item["result_code"] = payload.ResultCode
		item["result_message"] = payload.ResultMsg
		item["error_code"] = payload.ErrorCode
		item["error_message"] = payload.ErrorMsg
		out = append(out, item)
	}
	return out
}
