package tripcdp

import (
	"net/url"
	"strings"
)

const hkAcceptLanguage = "zh-HK,zh;q=0.9,en-US;q=0.8,en;q=0.7"

type siteConfig struct {
	Code           string
	Host           string
	Locale         string
	Currency       string
	AcceptLanguage string
	PricePrefix    string
}

func siteConfigFor(code string) siteConfig {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "sg":
		return siteConfig{Code: "sg", Host: "sg.trip.com", Locale: "en-SG", Currency: "SGD", AcceptLanguage: "en-SG,en;q=0.9", PricePrefix: "S$"}
	default:
		return siteConfig{Code: "hk", Host: "hk.trip.com", Locale: "zh-HK", Currency: "HKD", AcceptLanguage: hkAcceptLanguage, PricePrefix: "HK$"}
	}
}

func validSite(code string) bool {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "hk", "sg":
		return true
	default:
		return false
	}
}

type navigationTarget struct {
	OriginalURL    string
	NavigateURL    string
	Host           string
	Locale         string
	Currency       string
	AcceptLanguage string
	IsHK           bool
}

func newNavigationTarget(rawURL string, configs ...siteConfig) navigationTarget {
	config := siteConfigFor("hk")
	if len(configs) > 0 {
		config = configs[0]
	}
	target := navigationTarget{
		OriginalURL:    rawURL,
		NavigateURL:    rawURL,
		AcceptLanguage: chromeLanguage(),
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return target
	}
	sourceHost := strings.ToLower(parsed.Hostname())
	if sourceHost != "trip.com" && !strings.HasSuffix(sourceHost, ".trip.com") {
		return target
	}
	parsed.Scheme = "https"
	parsed.Host = config.Host
	values := parsed.Query()
	values.Set("locale", config.Locale)
	values.Set("curr", config.Currency)
	parsed.RawQuery = values.Encode()
	target.Host = config.Host
	target.NavigateURL = parsed.String()
	target.Locale = config.Locale
	target.Currency = config.Currency
	target.AcceptLanguage = config.AcceptLanguage
	target.IsHK = config.Code == "hk"
	return target
}

func preferredLanguageForItems(items []ScrapeItem) string {
	for _, item := range items {
		target := newNavigationTarget(item.URL)
		if target.IsHK {
			return hkAcceptLanguage
		}
	}
	return ""
}

func targetOrigin(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return rawURL
	}
	return parsed.Scheme + "://" + parsed.Host
}
