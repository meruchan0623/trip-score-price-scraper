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

type apiSKUPriceCoverage struct {
	TreeCount         int
	PricedCount       int
	MissingPriceCount int
	Complete          bool
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
	tree := map[apiSKUKey]apiSKUTreeLeaf{}
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
				if leaf.PackageID > 0 {
					tree[apiSKUKey{PackageID: leaf.PackageID, ResourceID: leaf.ResourceID}] = leaf
				}
			}
		case strings.Contains(response.URL, "/restapi/soa2/14580/getProductPriceCalendar"):
			for _, price := range parseAPIPriceCalendar(response.Text) {
				if price.Currency != "" && !strings.EqualFold(price.Currency, currency) {
					continue
				}
				if price.PackageID > 0 && price.ResourceID > 0 && price.OriginalPrice > 0 && price.Price > 0 {
					prices[apiSKUKey{PackageID: price.PackageID, ResourceID: price.ResourceID}] = price
				}
			}
		}
	}

	// Only use the calendar as the SKU authority when product info supplied no tree.
	// Otherwise preserve its independent boundary, including mismatched resource IDs.
	if len(tree) == 0 {
		for _, response := range responses {
			if response.Status < 200 || response.Status >= 300 {
				continue
			}
			for _, leaf := range capturedResponseSKUTree(response) {
				tree[apiSKUKey{PackageID: leaf.PackageID, ResourceID: leaf.ResourceID}] = leaf
			}
		}
	}

	keys := make([]apiSKUKey, 0, len(tree))
	for key := range tree {
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
		leaf := tree[key]
		price, ok := prices[key]
		if !ok {
			continue
		}
		pricedTreeLeaves++
		properties := leaf.Properties
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
			"price_source":       priceSourceProductPriceCalendar,
			"raw_captured_price": formatPriceNumber(price.OriginalPrice),
		})
	}
	coverage := apiSKUPriceCoverage{
		TreeCount:         len(tree),
		PricedCount:       pricedTreeLeaves,
		MissingPriceCount: max(0, len(tree)-pricedTreeLeaves),
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
					ResourceID int64 `json:"resourceId"`
				} `json:"resourceInfos"`
				PriceInfo struct {
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
		price := apiSKUPrice{
			PackageID:     item.PackageID,
			Currency:      strings.ToUpper(strings.TrimSpace(firstNonEmpty(item.PriceInfo.Currency, item.PriceInfo.CurrencyCode))),
			OriginalPrice: item.PriceInfo.OriginalPrice,
			Price:         item.PriceInfo.Price,
		}
		for _, property := range item.SalesProperties {
			for _, value := range property.SalesPropertyValues {
				price.Properties = append(price.Properties, apiSKUProperty{PropertyID: property.SalesProperty.ID, PropertyName: property.SalesProperty.Name, ValueID: value.ID, ValueName: value.Name})
			}
		}
		for _, promo := range item.PriceInfo.PreferentialPriceDetails {
			if text := strings.TrimSpace(strings.Join([]string{strings.TrimSpace(promo.Title), strings.TrimSpace(promo.Name)}, " ")); text != "" {
				price.PromoTags = append(price.PromoTags, text)
			}
		}
		for _, resource := range item.ResourceInfos {
			resourcePrice := price
			resourcePrice.ResourceID = resource.ResourceID
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
