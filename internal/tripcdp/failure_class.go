package tripcdp

import "strings"

type FailureClass string

const (
	FailureNone                FailureClass = "none"
	FailureBrowserFailure      FailureClass = "browser_failure"
	FailureWorkerStalled       FailureClass = "worker_stalled"
	FailureNavigationBlocked   FailureClass = "navigation_blocked"
	FailureRiskChallenge       FailureClass = "risk_challenge"
	FailureAPIBlockedSuspected FailureClass = "api_blocked_suspected"
	FailureBusinessUnavailable FailureClass = "business_unavailable"
	FailurePageNotReady        FailureClass = "page_not_ready"
	FailureDataQualityLow      FailureClass = "data_quality_low"
)

func ClassifyError(err error, message string) FailureClass {
	text := strings.ToLower(strings.TrimSpace(message))
	if err != nil {
		text = strings.TrimSpace(text + " " + strings.ToLower(err.Error()))
	}
	switch {
	case text == "":
		return FailureNone
	case strings.Contains(text, "closed network connection"), strings.Contains(text, "websocket"), strings.Contains(text, "context canceled"), strings.Contains(text, "context deadline exceeded"):
		return FailureBrowserFailure
	case strings.Contains(text, "err_aborted"), strings.Contains(text, "navigation failed"), strings.Contains(text, "tcp"):
		return FailureNavigationBlocked
	case strings.Contains(text, "whaleguard"), strings.Contains(text, "cloudflare"), strings.Contains(text, "captcha"):
		return FailureRiskChallenge
	case strings.Contains(text, "页面未加载"), strings.Contains(text, "page not ready"), strings.Contains(text, "product dom not ready"):
		return FailurePageNotReady
	default:
		return FailureDataQualityLow
	}
}

func ClassifyQualityFailure(reason string, trustedRows int, untrustedRows int, resourceMissRows int, totalRows int, diagnostics productPackageDiagnostics) FailureClass {
	reason = strings.TrimSpace(reason)
	if trustedRows > 0 {
		return FailureNone
	}
	if reason == "page_not_ready" {
		return FailurePageNotReady
	}
	if reason == "consecutive_resource_miss" || reason == "resource_miss_rate" {
		if totalRows >= minSamplesForResourceRate && resourceMissRows > 0 && diagnostics.NextDataPresent && diagnostics.PackageCount > 0 && diagnostics.BatchResponseCount > 0 {
			return FailureAPIBlockedSuspected
		}
		return FailureBusinessUnavailable
	}
	if untrustedRows > 0 || totalRows > 0 {
		return FailureDataQualityLow
	}
	return FailureNone
}

func ClassifyRows(rows []map[string]string) FailureClass {
	if len(rows) == 0 {
		return FailureNone
	}
	trusted, untrusted, resourceMiss := 0, 0, 0
	for _, row := range rows {
		status := strings.TrimSpace(row["价格状态"])
		note := strings.ToLower(strings.TrimSpace(row["抓取备注"] + " " + row["评分"]))
		if status == priceStatusRiskBlocked || strings.Contains(note, "whaleguard") || strings.Contains(note, "cloudflare") {
			return FailureRiskChallenge
		}
		if isTrustedPriceStatus(status) {
			trusted++
			continue
		}
		if status != "" {
			untrusted++
		}
		if status == priceStatusUntrustedResourceMiss || strings.Contains(note, "resource_miss") || strings.Contains(note, "价格质量检查失败") {
			resourceMiss++
		}
		if strings.Contains(note, "页面未加载") || strings.Contains(note, "product dom not ready") {
			return FailurePageNotReady
		}
	}
	if trusted > 0 {
		return FailureNone
	}
	if resourceMiss >= minSamplesForResourceRate && untrusted > 0 {
		return FailureAPIBlockedSuspected
	}
	if resourceMiss > 0 {
		return FailureBusinessUnavailable
	}
	if untrusted > 0 {
		return FailureDataQualityLow
	}
	return FailureNone
}

func FailureClassRetryableByQualityRerun(class FailureClass) bool {
	switch class {
	case FailurePageNotReady, FailureDataQualityLow, FailureNavigationBlocked, FailureBrowserFailure:
		return true
	default:
		return false
	}
}

func FailureClassAllowsBrowserRestart(class FailureClass) bool {
	switch class {
	case FailureBrowserFailure, FailureWorkerStalled, FailureNavigationBlocked, FailurePageNotReady:
		return true
	default:
		return false
	}
}
