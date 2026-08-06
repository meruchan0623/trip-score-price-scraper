package tripcdp

import (
	"net/url"
	"testing"
)

func TestNavigationTargetUsesResolvedMarket(t *testing.T) {
	for _, site := range []string{"hk", "sg", "jp", "tw", "de", "sa"} {
		market, err := resolveMarketContext(site, "", "")
		if err != nil {
			t.Fatal(err)
		}
		target := newNavigationTarget("https://hk.trip.com/things-to-do/detail/45892282/?foo=bar&curr=CNY", market)
		u, err := url.Parse(target.NavigateURL)
		if err != nil {
			t.Fatal(err)
		}
		if u.Hostname() != market.Host || u.Query().Get("locale") != market.Locale || u.Query().Get("curr") != market.Currency || u.Query().Get("foo") != "bar" || target.Market.Code != site {
			t.Errorf("%s: %+v", site, target)
		}
	}
}

func TestNavigationTargetPreservesNonTripURL(t *testing.T) {
	market, _ := resolveMarketContext("jp", "", "")
	raw := "https://example.com/things-to-do/detail/92237157/?curr=CNY"
	target := newNavigationTarget(raw, market)
	if target.NavigateURL != raw || target.Market.Code != "" {
		t.Fatalf("non-Trip URL should remain unchanged: %+v", target)
	}
}

func TestApplyMarketBrowserDefaults(t *testing.T) {
	market, err := resolveMarketContext("jp", "", "")
	if err != nil {
		t.Fatal(err)
	}
	r := TaskRequest{Market: market}
	applyMarketBrowserDefaults(&r)
	if r.BrowserOptions.Language != market.AcceptLanguage {
		t.Fatalf("language = %q, want %q", r.BrowserOptions.Language, market.AcceptLanguage)
	}
	r.BrowserOptions.Language = "custom"
	applyMarketBrowserDefaults(&r)
	if r.BrowserOptions.Language != "custom" {
		t.Fatalf("explicit language overwritten: %q", r.BrowserOptions.Language)
	}
}
