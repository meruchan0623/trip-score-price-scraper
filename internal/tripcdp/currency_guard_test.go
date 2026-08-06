package tripcdp

import "testing"

func TestInvalidateNonHKDPriceRowsRejectsCNY(t *testing.T) {
	rows := []map[string]string{{
		"货币":   "CNY",
		"外显价格": "12.30",
		"实价":   "10.50",
		"促销标签": "立减",
	}}

	invalidateNonHKDPriceRows(rows)

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

	invalidateNonHKDPriceRows(rows)

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
