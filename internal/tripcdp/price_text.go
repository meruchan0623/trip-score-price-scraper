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
