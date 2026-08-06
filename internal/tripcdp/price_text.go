package tripcdp

import (
	"regexp"
	"sort"
	"strings"
)

var displayPriceAmountPattern = regexp.MustCompile(`^[0-9][0-9,]*(?:\.[0-9]+)?`)

func parseDisplayPrice(value string, market MarketContext) (currency, amount string) {
	text := strings.TrimSpace(value)
	prefixes := append([]string(nil), market.PricePrefixes...)
	sort.SliceStable(prefixes, func(i, j int) bool { return len([]rune(prefixes[i])) > len([]rune(prefixes[j])) })
	for _, prefix := range prefixes {
		if !strings.HasPrefix(text, prefix) {
			continue
		}
		remainder := strings.TrimSpace(strings.TrimPrefix(text, prefix))
		match := displayPriceAmountPattern.FindString(remainder)
		if match == "" {
			return "N/A", "N/A"
		}
		return market.Currency, strings.ReplaceAll(match, ",", "")
	}
	return "N/A", "N/A"
}

func isValidDisplayPriceForMarket(value string, market MarketContext) bool {
	currency, amount := parseDisplayPrice(value, market)
	return currency != "N/A" && amount != "N/A"
}

// splitPrice is retained for transitional callers until all extraction paths
// carry a MarketContext. It preserves the historical prefix behavior.
func splitPrice(value string) (string, string) {
	priceText := strings.TrimSpace(value)
	if priceText == "" || priceText == "价格未找到" || priceText == "未找到" {
		return "N/A", "N/A"
	}
	currency := "N/A"
	switch {
	case strings.Contains(priceText, "HK$"):
		currency = "HKD"
		priceText = strings.ReplaceAll(priceText, "HK$", "")
	case strings.Contains(priceText, "S$"):
		currency = "SGD"
		priceText = strings.ReplaceAll(priceText, "S$", "")
	case strings.Contains(priceText, "$"):
		currency = "USD"
		priceText = strings.ReplaceAll(priceText, "$", "")
	case strings.Contains(priceText, "¥") || strings.Contains(priceText, "￥") || strings.Contains(priceText, "CNY"):
		currency = "CNY"
		priceText = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(priceText, "¥", ""), "￥", ""), "CNY", "")
	}
	match := regexp.MustCompile(`([0-9,]+\.?[0-9]*)`).FindStringSubmatch(priceText)
	if len(match) == 0 {
		return currency, strings.TrimSpace(priceText)
	}
	return currency, strings.ReplaceAll(match[1], ",", "")
}

func isValidDisplayPrice(value string) bool {
	currency, amount := splitPrice(value)
	return currency != "N/A" && amount != "N/A"
}
