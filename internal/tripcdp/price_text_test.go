package tripcdp

import "testing"

func TestParseDisplayPriceUsesExpectedMarket(t *testing.T) {
	cases := []struct {
		site, text, currency, amount string
	}{
		{"hk", "HK$ 1,234.50", "HKD", "1234.50"},
		{"sg", "S$ 0.11", "SGD", "0.11"},
		{"tw", "NT$ 299", "TWD", "299"},
		{"jp", "¥ 2,500", "JPY", "2500"},
		{"kr", "₩ 15,000", "KRW", "15000"},
		{"th", "฿ 899.00", "THB", "899.00"},
		{"de", "€ 19.95", "EUR", "19.95"},
		{"uk", "£ 8.50", "GBP", "8.50"},
		{"sa", "SAR 42.00", "SAR", "42.00"},
		{"in", "₹ 999", "INR", "999"},
	}
	for _, c := range cases {
		market, err := resolveMarketContext(c.site, "", "")
		if err != nil {
			t.Fatal(err)
		}
		currency, amount := parseDisplayPrice(c.text, market)
		if currency != c.currency || amount != c.amount || !isValidDisplayPriceForMarket(c.text, market) {
			t.Errorf("%s: %q %q", c.site, currency, amount)
		}
	}
}

func TestParseDisplayPriceRejectsWrongPrefix(t *testing.T) {
	hk, _ := resolveMarketContext("hk", "", "")
	jp, _ := resolveMarketContext("jp", "", "")
	if c, a := parseDisplayPrice("S$ 10", hk); c != "N/A" || a != "N/A" {
		t.Fatalf("%q %q", c, a)
	}
	if c, a := parseDisplayPrice("$ 10", jp); c != "N/A" || a != "N/A" {
		t.Fatalf("%q %q", c, a)
	}
	for _, text := range []string{"", "N/A", "价格未找到", "JPY --", "€ free"} {
		if isValidDisplayPriceForMarket(text, jp) {
			t.Errorf("accepted %q", text)
		}
	}
}

func TestSplitPricePreservesHistoricalCurrencyBehavior(t *testing.T) {
	for _, tc := range []struct {
		text, currency, amount string
	}{
		{"¥ 2,500", "CNY", "2500"},
		{"CNY 2,500", "CNY", "2500"},
		{"$ 10", "USD", "10"},
	} {
		currency, amount := splitPrice(tc.text)
		if currency != tc.currency || amount != tc.amount {
			t.Errorf("splitPrice(%q) = %q %q, want %q %q", tc.text, currency, amount, tc.currency, tc.amount)
		}
	}
}
