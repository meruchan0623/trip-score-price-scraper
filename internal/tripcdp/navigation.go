package tripcdp

import (
	"net/url"
	"strings"
)

const hkAcceptLanguage = "zh-HK,zh;q=0.9,en-US;q=0.8,en;q=0.7"

type navigationTarget struct {
	OriginalURL    string
	NavigateURL    string
	Host           string
	Locale         string
	Currency       string
	AcceptLanguage string
	IsHK           bool
}

func newNavigationTarget(rawURL string) navigationTarget {
	target := navigationTarget{
		OriginalURL:    rawURL,
		NavigateURL:    rawURL,
		AcceptLanguage: chromeLanguage(),
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return target
	}
	target.Host = strings.ToLower(parsed.Hostname())
	if target.Host != "hk.trip.com" {
		return target
	}
	values := parsed.Query()
	values.Set("locale", "zh-hk")
	values.Set("curr", "HKD")
	parsed.RawQuery = values.Encode()
	target.NavigateURL = parsed.String()
	target.Locale = "zh-hk"
	target.Currency = "HKD"
	target.AcceptLanguage = hkAcceptLanguage
	target.IsHK = true
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
