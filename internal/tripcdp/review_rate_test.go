package tripcdp

import "testing"

func TestGoodRateFromReviewCounts(t *testing.T) {
	tests := []struct {
		name, total, nonpositive, want string
		ok                             bool
	}{
		{name: "zero nonpositive", total: "100", nonpositive: "0", want: "100.0%", ok: true},
		{name: "all nonpositive", total: "7", nonpositive: "7", want: "0.0%", ok: true},
		{name: "one decimal", total: "2959", nonpositive: "263", want: "91.1%", ok: true},
		{name: "missing nonpositive", total: "100", nonpositive: "", ok: false},
		{name: "zero total", total: "0", nonpositive: "0", ok: false},
		{name: "negative", total: "100", nonpositive: "-1", ok: false},
		{name: "fractional", total: "100.0", nonpositive: "1", ok: false},
		{name: "approximate", total: "1.2K", nonpositive: "1", ok: false},
		{name: "malformed grouping", total: "1,00", nonpositive: "1", ok: false},
		{name: "more nonpositive", total: "3", nonpositive: "4", ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := goodRateFromReviewCounts(tt.total, tt.nonpositive)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("goodRateFromReviewCounts(%q, %q) = (%q, %v), want (%q, %v)", tt.total, tt.nonpositive, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestApplyGoodRateFromReviewCountsPreservesSuppliedRateWithoutValidCounts(t *testing.T) {
	row := map[string]string{"评价数": "100", "好评率": "91.1%"}
	applyGoodRateFromReviewCounts(row)
	if got := row["好评率"]; got != "91.1%" {
		t.Fatalf("好评率 = %q, want supplied fallback", got)
	}
}
