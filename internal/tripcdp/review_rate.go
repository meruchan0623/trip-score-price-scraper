package tripcdp

import (
	"fmt"
	"strconv"
	"strings"
)

// goodRateFromReviewCounts returns a rate only for exact, non-negative integer
// counts. Values with units, decimals, signs, or other approximations are not
// safe inputs for a derived percentage.
func goodRateFromReviewCounts(totalText, nonpositiveText string) (string, bool) {
	total, ok := exactReviewCount(totalText)
	if !ok || total == 0 {
		return "", false
	}
	nonpositive, ok := exactReviewCount(nonpositiveText)
	if !ok || nonpositive > total {
		return "", false
	}
	return fmt.Sprintf("%.1f%%", float64(total-nonpositive)/float64(total)*100), true
}

func exactReviewCount(value string) (uint64, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	parts := strings.Split(value, ",")
	for index, part := range parts {
		if part == "" || (len(parts) > 1 && index == 0 && len(part) > 3) || (index > 0 && len(part) != 3) {
			return 0, false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return 0, false
			}
		}
	}
	value = strings.Join(parts, "")
	count, err := strconv.ParseUint(value, 10, 64)
	return count, err == nil
}

func applyGoodRateFromReviewCounts(row map[string]string) {
	if row == nil {
		return
	}
	if rate, ok := goodRateFromReviewCounts(row["评价数"], row["中差评价数"]); ok {
		row["好评率"] = rate
	}
}
