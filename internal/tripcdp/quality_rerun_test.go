package tripcdp

import "testing"

func TestQualityRerunCandidatesSkipTrustedNoSKUBaseRows(t *testing.T) {
	rows := []map[string]string{
		{"厂商": "TGT", "商品ID": "1", "URL": "https://hk.trip.com/things-to-do/detail/1/", "SKU层级1": "无SKU选项", "价格状态": priceStatusTrustedBase, "外显价格": "49.13", "实价": "49.13"},
		{"厂商": "JY", "商品ID": "2", "URL": "https://hk.trip.com/things-to-do/detail/2/", "价格状态": priceStatusUntrustedResourceMiss, "抓取备注": "价格质量检查失败: consecutive_resource_miss"},
		{"厂商": "BC", "商品ID": "3", "URL": "https://hk.trip.com/things-to-do/detail/3/", "价格状态": priceStatusRiskBlocked, "抓取备注": "whaleguard block"},
	}

	candidates := collectQualityRerunCandidates(rows)

	if len(candidates) != 0 {
		t.Fatalf("candidates = %d, want 0: %+v", len(candidates), candidates)
	}
}

func TestQualityRerunCandidatesSkipAPISuspectedResourceMiss(t *testing.T) {
	rows := []map[string]string{}
	for i := 0; i < minSamplesForResourceRate; i++ {
		row := map[string]string{
			"厂商":       "BC",
			"商品ID":     "1",
			"URL":      "https://hk.trip.com/things-to-do/detail/1/",
			"价格状态":     priceStatusUntrustedResourceMiss,
			"资源ID":     "0",
			"接口ItemID": "0",
			"抓取备注":     "价格质量检查失败: resource_miss_rate",
		}
		if i > 0 {
			row["厂商"] = ""
			row["商品ID"] = ""
			row["URL"] = ""
		}
		rows = append(rows, row)
	}

	candidates := collectQualityRerunCandidates(rows)
	if len(candidates) != 0 {
		t.Fatalf("api-block suspected rows should not be quality rerun candidates: %+v", candidates)
	}
}

func TestQualityRerunCandidatesKeepPageNotReady(t *testing.T) {
	rows := []map[string]string{{"厂商": "BC", "商品ID": "2", "URL": "https://hk.trip.com/things-to-do/detail/2/", "抓取备注": "页面未加载有效商品内容"}}
	candidates := collectQualityRerunCandidates(rows)
	if len(candidates) != 1 {
		t.Fatalf("page-not-ready rows should be rerun candidates: %+v", candidates)
	}
	if candidates[0].FailureClass != FailurePageNotReady {
		t.Fatalf("class = %q", candidates[0].FailureClass)
	}
}

func TestMergeQualityRerunRowsOnlyReplacesWithBetterResult(t *testing.T) {
	original := []map[string]string{
		{"厂商": "JY", "商品ID": "57490039", "URL": "https://hk.trip.com/things-to-do/detail/57490039/", "价格状态": priceStatusUntrustedResourceMiss, "抓取备注": "价格质量检查失败"},
		{"厂商": "BC", "商品ID": "51934317", "URL": "https://hk.trip.com/things-to-do/detail/51934317/", "SKU层级1": "1天", "价格状态": priceStatusTrustedPromo, "实价": "0.89"},
		{"厂商": "", "商品ID": "", "URL": "", "SKU层级1": "2天", "价格状态": priceStatusTrustedPromo, "实价": "8.66"},
	}
	worse := []map[string]string{{"厂商": "JY", "商品ID": "57490039", "URL": "https://hk.trip.com/things-to-do/detail/57490039/", "价格状态": priceStatusRiskBlocked, "抓取备注": "blocked again"}}
	better := []map[string]string{
		{"厂商": "JY", "商品ID": "57490039", "URL": "https://hk.trip.com/things-to-do/detail/57490039/", "SKU层级1": "1天", "价格状态": priceStatusTrustedPromo, "实价": "5.80"},
		{"厂商": "", "商品ID": "", "URL": "", "SKU层级1": "2天", "价格状态": priceStatusTrustedPromo, "实价": "9.10"},
	}

	kept, replaced := mergeQualityRerunRows(original, "https://hk.trip.com/things-to-do/detail/57490039/", worse)
	if replaced {
		t.Fatal("worse rerun should not replace original rows")
	}
	if len(kept) != len(original) || kept[0]["价格状态"] != priceStatusUntrustedResourceMiss {
		t.Fatalf("worse rerun changed rows: %+v", kept)
	}

	merged, replaced := mergeQualityRerunRows(original, "https://hk.trip.com/things-to-do/detail/57490039/", better)
	if !replaced {
		t.Fatal("better rerun should replace original rows")
	}
	if len(merged) != 4 {
		t.Fatalf("merged rows = %d, want 4: %+v", len(merged), merged)
	}
	if merged[0]["价格状态"] != priceStatusTrustedPromo || merged[1]["实价"] != "9.10" {
		t.Fatalf("rerun rows not inserted at original position: %+v", merged)
	}
	if merged[2]["商品ID"] != "51934317" {
		t.Fatalf("unrelated product order not preserved: %+v", merged)
	}
}
