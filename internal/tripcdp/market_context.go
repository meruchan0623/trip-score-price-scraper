package tripcdp

import (
	"fmt"
	"strings"
)

type MarketContext struct {
	Code, SiteID, Host, Locale, Currency, AcceptLanguage string
	PricePrefixes                                        []string
}

// marketEventFields carries only requested public market identity through
// structured events. It deliberately excludes cookies, headers, storage, and
// user identity.
func marketEventFields(market MarketContext) Event {
	return Event{
		"market_code":     market.Code,
		"market_site_id":  market.SiteID,
		"market_host":     market.Host,
		"market_locale":   market.Locale,
		"market_currency": market.Currency,
	}
}

// marketObservation contains only the public values needed to verify the
// market returned by Trip after navigation. It deliberately excludes raw
// cookies, headers, storage, and account identifiers.
type marketObservation struct {
	Host     string `json:"host"`
	SiteID   string `json:"site_id"`
	Locale   string `json:"locale"`
	Currency string `json:"currency"`
}

type marketContextError struct {
	Class    string
	Expected MarketContext
	Observed marketObservation
}

func (e *marketContextError) Error() string {
	if e == nil {
		return ""
	}
	return e.Class
}

func validateMarketObservation(expected MarketContext, observed marketObservation) error {
	observed.Host = strings.ToLower(strings.TrimSpace(observed.Host))
	observed.SiteID = strings.ToUpper(strings.TrimSpace(observed.SiteID))
	observed.Locale = normalizeLocale(observed.Locale)
	observed.Currency = strings.ToUpper(strings.TrimSpace(observed.Currency))
	expectedHost := strings.ToLower(strings.TrimSpace(expected.Host))
	expectedSiteID := strings.ToUpper(strings.TrimSpace(expected.SiteID))
	expectedLocale := normalizeLocale(expected.Locale)
	expectedCurrency := strings.ToUpper(strings.TrimSpace(expected.Currency))
	if (observed.Host != "" && observed.Host != expectedHost) ||
		(observed.SiteID != "" && observed.SiteID != expectedSiteID) ||
		(observed.Locale != "" && observed.Locale != expectedLocale) ||
		(observed.Currency != "" && observed.Currency != expectedCurrency) {
		return &marketContextError{Class: "market_context_mismatch", Expected: expected, Observed: observed}
	}
	if observed.Host == "" || observed.SiteID == "" || observed.Locale == "" || observed.Currency == "" {
		return &marketContextError{Class: "market_context_unverified", Expected: expected, Observed: observed}
	}
	return nil
}

var marketSites = []MarketContext{
	{Code: "global", SiteID: "EN", Host: "www.trip.com", Locale: "en-XX", Currency: "USD"},
	{Code: "hk", SiteID: "HK", Host: "hk.trip.com", Locale: "zh-HK", Currency: "HKD"},
	{Code: "sg", SiteID: "SG", Host: "sg.trip.com", Locale: "en-SG", Currency: "SGD"},
	{Code: "us", SiteID: "US", Host: "us.trip.com", Locale: "en-US", Currency: "USD"},
	{Code: "tw", SiteID: "TW", Host: "tw.trip.com", Locale: "zh-TW", Currency: "TWD"},
	{Code: "jp", SiteID: "JP", Host: "jp.trip.com", Locale: "ja-JP", Currency: "JPY"},
	{Code: "kr", SiteID: "KR", Host: "kr.trip.com", Locale: "ko-KR", Currency: "KRW"},
	{Code: "th", SiteID: "TH", Host: "th.trip.com", Locale: "th-TH", Currency: "THB"},
	{Code: "vn", SiteID: "VN", Host: "vn.trip.com", Locale: "vi-VN", Currency: "VND"},
	{Code: "id", SiteID: "ID", Host: "id.trip.com", Locale: "id-ID", Currency: "IDR"},
	{Code: "my", SiteID: "MY", Host: "my.trip.com", Locale: "ms-MY", Currency: "MYR"},
	{Code: "au", SiteID: "AU", Host: "au.trip.com", Locale: "en-AU", Currency: "AUD"},
	{Code: "uk", SiteID: "GB", Host: "uk.trip.com", Locale: "en-GB", Currency: "GBP"},
	{Code: "de", SiteID: "DE", Host: "de.trip.com", Locale: "de-DE", Currency: "EUR"},
	{Code: "fr", SiteID: "FR", Host: "fr.trip.com", Locale: "fr-FR", Currency: "EUR"},
	{Code: "es", SiteID: "ES", Host: "es.trip.com", Locale: "es-ES", Currency: "EUR"},
	{Code: "it", SiteID: "IT", Host: "it.trip.com", Locale: "it-IT", Currency: "EUR"},
	{Code: "nl", SiteID: "NL", Host: "nl.trip.com", Locale: "nl-NL", Currency: "EUR"},
	{Code: "pt", SiteID: "PT", Host: "pt.trip.com", Locale: "pt-PT", Currency: "EUR"},
	{Code: "br", SiteID: "BR", Host: "br.trip.com", Locale: "pt-BR", Currency: "BRL"},
	{Code: "ca", SiteID: "CA", Host: "ca.trip.com", Locale: "en-CA", Currency: "CAD"},
	{Code: "ae", SiteID: "AE", Host: "ae.trip.com", Locale: "en-AE", Currency: "AED"},
	{Code: "sa", SiteID: "SA", Host: "sa.trip.com", Locale: "ar-SA", Currency: "SAR"},
	{Code: "in", SiteID: "IN", Host: "in.trip.com", Locale: "en-IN", Currency: "INR"},
	{Code: "ph", SiteID: "PH", Host: "ph.trip.com", Locale: "en-PH", Currency: "PHP"},
	{Code: "ua", SiteID: "UA", Host: "ua.trip.com", Locale: "uk-UA", Currency: "UAH"},
	{Code: "dk", SiteID: "DK", Host: "dk.trip.com", Locale: "da-DK", Currency: "DKK"},
	{Code: "pl", SiteID: "PL", Host: "pl.trip.com", Locale: "pl-PL", Currency: "PLN"},
	{Code: "fi", SiteID: "FI", Host: "fi.trip.com", Locale: "fi-FI", Currency: "EUR"},
	{Code: "se", SiteID: "SE", Host: "se.trip.com", Locale: "sv-SE", Currency: "SEK"},
	{Code: "tr", SiteID: "TR", Host: "tr.trip.com", Locale: "tr-TR", Currency: "TRY"},
	{Code: "gr", SiteID: "GR", Host: "gr.trip.com", Locale: "el-GR", Currency: "EUR"},
	{Code: "ru", SiteID: "RU", Host: "ru.trip.com", Locale: "ru-RU", Currency: "RUB"},
}

var advertisedTTDLocales = map[string]struct{}{}

var currencyPricePrefixes = map[string][]string{
	"AED": {"AED"}, "AUD": {"AU$", "A$", "AUD"}, "BRL": {"R$", "BRL"},
	"CAD": {"CA$", "C$", "CAD"}, "DKK": {"DKK", "kr"}, "EUR": {"€", "EUR"},
	"GBP": {"£", "GBP"}, "HKD": {"HK$", "HKD"}, "IDR": {"Rp", "IDR"},
	"INR": {"₹", "INR"}, "JPY": {"¥", "￥", "JPY"}, "KRW": {"₩", "KRW"},
	"MYR": {"RM", "MYR"}, "PHP": {"₱", "PHP"}, "PLN": {"zł", "PLN"},
	"RUB": {"₽", "RUB"}, "SAR": {"SAR", "ر.س"}, "SEK": {"SEK", "kr"},
	"SGD": {"S$", "SGD"}, "THB": {"฿", "THB"}, "TRY": {"₺", "TRY"},
	"TWD": {"NT$", "TWD"}, "UAH": {"₴", "UAH"}, "USD": {"US$", "$", "USD"},
	"VND": {"₫", "VND"},
}

func init() {
	for _, locale := range strings.Fields(`da-DK de-AT de-CH de-DE el-GR en-AE en-AU en-BE en-BH en-CA en-CH en-GB en-HK en-ID en-IE en-IL en-IN en-KH en-KW en-MY en-NZ en-OM en-PH en-PK en-QA en-SA en-SG en-TH en-US en-XX es-ES es-MX es-US fi-FI fr-BE fr-CH fr-FR id-ID it-IT ja-JP ko-KR ms-MY nl-BE nl-NL pl-PL pt-BR pt-PT ru-AZ ru-BY ru-KZ ru-RU sv-SE th-TH tr-TR uk-UA vi-VN zh-HK zh-MY zh-SG zh-TW`) {
		advertisedTTDLocales[locale] = struct{}{}
	}
	for i := range marketSites {
		marketSites[i].AcceptLanguage = acceptLanguage(marketSites[i].Locale, marketSites[i].Code == "hk")
		marketSites[i].PricePrefixes = append([]string(nil), currencyPricePrefixes[marketSites[i].Currency]...)
	}
}

func acceptLanguage(locale string, hk bool) string {
	if hk {
		return "zh-HK,zh;q=0.9,en-US;q=0.8,en;q=0.7"
	}
	language := strings.SplitN(locale, "-", 2)[0]
	return locale + "," + language + ";q=0.9,en;q=0.8"
}

func lookupSite(code string) (MarketContext, bool) {
	code = strings.ToLower(strings.TrimSpace(code))
	if code == "www" || code == "en" {
		code = "global"
	}
	for _, market := range marketSites {
		if market.Code == code {
			market.PricePrefixes = append([]string(nil), market.PricePrefixes...)
			return market, true
		}
	}
	return MarketContext{}, false
}

func supportedSiteCodes() []string {
	codes := make([]string, 0, len(marketSites))
	for _, market := range marketSites {
		codes = append(codes, market.Code)
	}
	return codes
}

func advertisedTTDLocale(locale string) bool {
	canonical := normalizeLocale(locale)
	_, ok := advertisedTTDLocales[canonical]
	return ok
}

func supportedCurrency(currency string) bool {
	_, ok := currencyPricePrefixes[strings.ToUpper(strings.TrimSpace(currency))]
	return ok
}

func normalizeLocale(locale string) string {
	parts := strings.Split(strings.ReplaceAll(strings.TrimSpace(locale), "_", "-"), "-")
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}
	parts[0] = strings.ToLower(parts[0])
	if len(parts) > 1 && len(parts[len(parts)-1]) == 2 {
		parts[len(parts)-1] = strings.ToUpper(parts[len(parts)-1])
	}
	return strings.Join(parts, "-")
}

func resolveMarketContext(site, locale, currency string) (MarketContext, error) {
	if strings.TrimSpace(site) == "" {
		site = "hk"
	}
	market, ok := lookupSite(site)
	if !ok {
		return MarketContext{}, fmt.Errorf("unsupported site %q", site)
	}
	if strings.TrimSpace(locale) != "" {
		market.Locale = normalizeLocale(locale)
		if !advertisedTTDLocale(market.Locale) {
			return MarketContext{}, fmt.Errorf("unsupported locale %q", locale)
		}
	}
	if strings.TrimSpace(currency) != "" {
		market.Currency = strings.ToUpper(strings.TrimSpace(currency))
		if !supportedCurrency(market.Currency) {
			return MarketContext{}, fmt.Errorf("unsupported currency %q", currency)
		}
	}
	market.AcceptLanguage = acceptLanguage(market.Locale, market.Code == "hk" && market.Locale == "zh-HK")
	market.PricePrefixes = append([]string(nil), currencyPricePrefixes[market.Currency]...)
	return market, nil
}

func parseMarketSpec(spec string) (MarketContext, error) {
	parts := strings.Split(spec, ":")
	if len(parts) < 1 || len(parts) > 3 || strings.TrimSpace(parts[0]) == "" {
		return MarketContext{}, fmt.Errorf("invalid market specification %q", spec)
	}
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			return MarketContext{}, fmt.Errorf("invalid market specification %q", spec)
		}
	}
	var locale, currency string
	if len(parts) >= 2 {
		locale = parts[1]
	}
	if len(parts) == 3 {
		currency = parts[2]
	}
	market, err := resolveMarketContext(parts[0], locale, currency)
	if err != nil {
		return MarketContext{}, flagValueError(err)
	}
	return market, nil
}

func sameMarketContext(a, b MarketContext) bool {
	return strings.EqualFold(strings.TrimSpace(a.Code), strings.TrimSpace(b.Code)) &&
		strings.EqualFold(strings.TrimSpace(a.SiteID), strings.TrimSpace(b.SiteID)) &&
		strings.EqualFold(strings.TrimSpace(a.Host), strings.TrimSpace(b.Host)) &&
		normalizeLocale(a.Locale) == normalizeLocale(b.Locale) &&
		strings.EqualFold(strings.TrimSpace(a.Currency), strings.TrimSpace(b.Currency))
}

func resolveMarketSpecs(specs []string) ([]MarketContext, error) {
	markets := make([]MarketContext, 0, len(specs))
	for _, spec := range specs {
		market, err := parseMarketSpec(spec)
		if err != nil {
			return nil, err
		}
		duplicate := false
		for _, existing := range markets {
			if sameMarketContext(existing, market) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			markets = append(markets, market)
		}
	}
	return markets, nil
}
