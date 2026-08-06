package tripcdp

import "testing"

func TestInvalidateNonHKDPriceRowsRejectsCNY(t *testing.T) {
	rows := []map[string]string{{
		"货币":   "CNY",
		"外显价格": "12.30",
		"实价":   "10.50",
		"促销标签": "立减",
	}}

	invalidateUnexpectedCurrencyRows(rows, "HKD")

	row := rows[0]
	for _, key := range []string{"货币", "外显价格", "实价", "促销标签"} {
		if row[key] != "" {
			t.Fatalf("%s = %q, want blank", key, row[key])
		}
	}
	if row["价格状态"] != "currency_mismatch" {
		t.Fatalf("price status = %q", row["价格状态"])
	}
}

func TestInvalidateNonHKDPriceRowsKeepsHKD(t *testing.T) {
	rows := []map[string]string{{"货币": "HKD", "外显价格": "12.30", "实价": "10.50"}}

	invalidateUnexpectedCurrencyRows(rows, "HKD")

	if rows[0]["货币"] != "HKD" || rows[0]["实价"] != "10.50" {
		t.Fatalf("HKD row was changed: %+v", rows[0])
	}
}

func TestInvalidateUnexpectedCurrencyRowsKeepsRequestedSGD(t *testing.T) {
	rows := []map[string]string{{"货币": "SGD", "外显价格": "0.11", "实价": "0.10"}}

	invalidateUnexpectedCurrencyRows(rows, "SGD")

	if rows[0]["货币"] != "SGD" || rows[0]["实价"] != "0.10" {
		t.Fatalf("SGD row was changed: %+v", rows[0])
	}
}

func TestUnexpectedCurrencyRowsUseExpectedCurrency(t *testing.T) {
	rows := []map[string]string{{
		"货币":   "USD",
		"外显价格": "10",
		"实价":   "9",
		"促销标签": "sale",
	}}

	invalidateUnexpectedCurrencyRows(rows, "JPY")

	row := rows[0]
	if row["价格状态"] != "currency_mismatch" {
		t.Fatalf("price status = %q, want currency_mismatch", row["价格状态"])
	}
	for _, key := range []string{"货币", "外显价格", "实价", "促销标签"} {
		if row[key] != "" {
			t.Fatalf("%s = %q, want blank", key, row[key])
		}
	}
}

func TestMarketEventFieldsExposeOnlyResolvedPublicMarketIdentity(t *testing.T) {
	market, err := resolveMarketContext("jp", "", "")
	if err != nil {
		t.Fatal(err)
	}

	fields := marketEventFields(market)
	if len(fields) != 5 {
		t.Fatalf("market event fields = %v, want exactly five public identity fields", fields)
	}
	want := map[string]any{
		"market_code":     "jp",
		"market_site_id":  "JP",
		"market_host":     "jp.trip.com",
		"market_locale":   "ja-JP",
		"market_currency": "JPY",
	}
	for key, expected := range want {
		if got := fields[key]; got != expected {
			t.Fatalf("%s = %#v, want %#v", key, got, expected)
		}
	}
}
