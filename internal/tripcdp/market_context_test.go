package tripcdp

import (
	"errors"
	"testing"
)

func TestSiteRegistryMatchesVerifiedDefaults(t *testing.T) {
	if got := len(supportedSiteCodes()); got != 33 {
		t.Fatalf("site count = %d, want 33", got)
	}
	checks := []struct{ code, site, host, locale, currency string }{
		{"global", "EN", "www.trip.com", "en-XX", "USD"}, {"hk", "HK", "hk.trip.com", "zh-HK", "HKD"},
		{"sg", "SG", "sg.trip.com", "en-SG", "SGD"}, {"us", "US", "us.trip.com", "en-US", "USD"},
		{"tw", "TW", "tw.trip.com", "zh-TW", "TWD"}, {"jp", "JP", "jp.trip.com", "ja-JP", "JPY"},
		{"kr", "KR", "kr.trip.com", "ko-KR", "KRW"}, {"th", "TH", "th.trip.com", "th-TH", "THB"},
		{"vn", "VN", "vn.trip.com", "vi-VN", "VND"}, {"id", "ID", "id.trip.com", "id-ID", "IDR"},
		{"my", "MY", "my.trip.com", "ms-MY", "MYR"}, {"au", "AU", "au.trip.com", "en-AU", "AUD"},
		{"uk", "GB", "uk.trip.com", "en-GB", "GBP"}, {"de", "DE", "de.trip.com", "de-DE", "EUR"},
		{"fr", "FR", "fr.trip.com", "fr-FR", "EUR"}, {"es", "ES", "es.trip.com", "es-ES", "EUR"},
		{"it", "IT", "it.trip.com", "it-IT", "EUR"}, {"nl", "NL", "nl.trip.com", "nl-NL", "EUR"},
		{"pt", "PT", "pt.trip.com", "pt-PT", "EUR"}, {"br", "BR", "br.trip.com", "pt-BR", "BRL"},
		{"ca", "CA", "ca.trip.com", "en-CA", "CAD"}, {"ae", "AE", "ae.trip.com", "en-AE", "AED"},
		{"sa", "SA", "sa.trip.com", "ar-SA", "SAR"}, {"in", "IN", "in.trip.com", "en-IN", "INR"},
		{"ph", "PH", "ph.trip.com", "en-PH", "PHP"}, {"ua", "UA", "ua.trip.com", "uk-UA", "UAH"},
		{"dk", "DK", "dk.trip.com", "da-DK", "DKK"}, {"pl", "PL", "pl.trip.com", "pl-PL", "PLN"},
		{"fi", "FI", "fi.trip.com", "fi-FI", "EUR"}, {"se", "SE", "se.trip.com", "sv-SE", "SEK"},
		{"tr", "TR", "tr.trip.com", "tr-TR", "TRY"}, {"gr", "GR", "gr.trip.com", "el-GR", "EUR"},
		{"ru", "RU", "ru.trip.com", "ru-RU", "RUB"},
	}
	for _, c := range checks {
		got, ok := lookupSite(c.code)
		if !ok || got.SiteID != c.site || got.Host != c.host || got.Locale != c.locale || got.Currency != c.currency || got.AcceptLanguage == "" || len(got.PricePrefixes) == 0 {
			t.Errorf("%s = %+v, found=%v", c.code, got, ok)
		}
	}
}

func TestAdvertisedTTDLocalesAndOverrides(t *testing.T) {
	if len(advertisedTTDLocales) != 60 {
		t.Fatalf("locale count = %d", len(advertisedTTDLocales))
	}
	for _, v := range []string{"zh-HK", "en-SG", "fr-CH", "ru-KZ", "ar-SA"} {
		want := v != "ar-SA"
		if advertisedTTDLocale(v) != want {
			t.Errorf("advertisedTTDLocale(%s) = %v, want %v", v, advertisedTTDLocale(v), want)
		}
	}
	defaultSA, err := resolveMarketContext("sa", "", "")
	if err != nil || defaultSA.Locale != "ar-SA" || defaultSA.Currency != "SAR" {
		t.Fatalf("SA default = %+v, err=%v", defaultSA, err)
	}
	got, err := resolveMarketContext("sg", "zh_sg", "myr")
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != "sg.trip.com" || got.Locale != "zh-SG" || got.Currency != "MYR" {
		t.Fatalf("%+v", got)
	}
	for _, bad := range [][3]string{{"xx", "", ""}, {"hk", "ja-US", "HKD"}, {"hk", "zh-HK", "ZZZ"}} {
		if _, err := resolveMarketContext(bad[0], bad[1], bad[2]); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
}

func TestValidateMarketObservation(t *testing.T) {
	expected, err := resolveMarketContext("sg", "zh-SG", "MYR")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		observed marketObservation
		class    string
	}{
		{"match", marketObservation{"sg.trip.com", "SG", "zh-SG", "MYR"}, ""},
		{"host", marketObservation{"hk.trip.com", "SG", "zh-SG", "MYR"}, "market_context_mismatch"},
		{"host despite missing secondary evidence", marketObservation{Host: "hk.trip.com"}, "market_context_mismatch"},
		{"locale", marketObservation{"sg.trip.com", "SG", "en-SG", "MYR"}, "market_context_mismatch"},
		{"currency", marketObservation{"sg.trip.com", "SG", "zh-SG", "SGD"}, "market_context_mismatch"},
		{"missing", marketObservation{Host: "sg.trip.com"}, "market_context_unverified"},
	}
	for _, c := range cases {
		err := validateMarketObservation(expected, c.observed)
		if c.class == "" && err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
		if c.class != "" {
			var contextErr *marketContextError
			if !errors.As(err, &contextErr) || contextErr.Class != c.class {
				t.Errorf("%s: %#v", c.name, err)
			}
		}
	}
}
