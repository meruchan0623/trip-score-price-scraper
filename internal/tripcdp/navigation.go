package tripcdp

import (
	"net/url"
	"strings"
)

const hkAcceptLanguage = "zh-HK,zh;q=0.9,en-US;q=0.8,en;q=0.7"

// Transitional compatibility for extraction code migrated in Task 4-6.
type siteConfig struct {
	Code, Host, Locale, Currency, AcceptLanguage, PricePrefix string
}

func siteConfigFor(code string) siteConfig {
	market, ok := lookupSite(code)
	if !ok {
		market, _ = lookupSite("hk")
	}
	prefix := ""
	if len(market.PricePrefixes) > 0 {
		prefix = market.PricePrefixes[0]
	}
	return siteConfig{Code: market.Code, Host: market.Host, Locale: market.Locale, Currency: market.Currency, AcceptLanguage: market.AcceptLanguage, PricePrefix: prefix}
}

type navigationTarget struct {
	OriginalURL string
	NavigateURL string
	Market      MarketContext
}

func newNavigationTarget(rawURL string, market MarketContext) navigationTarget {
	target := navigationTarget{OriginalURL: rawURL, NavigateURL: rawURL}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return target
	}
	sourceHost := strings.ToLower(parsed.Hostname())
	if sourceHost != "trip.com" && !strings.HasSuffix(sourceHost, ".trip.com") {
		return target
	}
	parsed.Scheme = "https"
	parsed.Host = market.Host
	values := parsed.Query()
	values.Set("locale", market.Locale)
	values.Set("curr", market.Currency)
	parsed.RawQuery = values.Encode()
	target.Market = market
	target.NavigateURL = parsed.String()
	return target
}

// preferredLanguageForItems keeps language selection independent from each
// input URL; all navigations in a task use the resolved market language.
func preferredLanguageForItems(_ []ScrapeItem, market MarketContext) string {
	return market.AcceptLanguage
}

func applyMarketBrowserDefaults(request *TaskRequest) {
	if request == nil || request.BrowserOptions.Language != "" {
		return
	}
	request.BrowserOptions.Language = request.Market.AcceptLanguage
}

func targetOrigin(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return rawURL
	}
	return parsed.Scheme + "://" + parsed.Host
}
