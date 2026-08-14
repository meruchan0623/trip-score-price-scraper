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

func TestClearRepeatedProductFieldsKeepsSKUIdentity(t *testing.T) {
	row := map[string]string{
		"厂商":     "TSIM 網址",
		"商品ID":   "57490039",
		"URL":    "https://hk.trip.com/things-to-do/detail/57490039/",
		"商品名称":   "菲律宾 5G eSIM",
		"SKU层级1": "1天",
		"评分":     "4.8",
		"商品资讯":   "product details",
	}
	clearRepeatedProductFields(row, true)
	for field, want := range map[string]string{
		"厂商":     "TSIM 網址",
		"商品ID":   "57490039",
		"URL":    "https://hk.trip.com/things-to-do/detail/57490039/",
		"商品名称":   "菲律宾 5G eSIM",
		"SKU层级1": "1天",
	} {
		if got := row[field]; got != want {
			t.Errorf("%s = %q, want %q", field, got, want)
		}
	}
	if row["评分"] != "" || row["商品资讯"] != "" {
		t.Fatalf("optional repeated fields were not cleared: %#v", row)
	}
}
