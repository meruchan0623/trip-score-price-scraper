package tripcdp

import (
	"errors"
	"testing"
)

func TestClassifyQualityFailureResourceMissAsAPISuspected(t *testing.T) {
	class := ClassifyQualityFailure(
		"resource_miss_rate",
		0,
		12,
		10,
		12,
		productPackageDiagnostics{NextDataPresent: true, PackageCount: 4, ResourceCount: 4, BatchResponseCount: 12, LastResponseStatus: 200},
	)
	if class != FailureAPIBlockedSuspected {
		t.Fatalf("class = %q, want %q", class, FailureAPIBlockedSuspected)
	}
	if FailureClassRetryableByQualityRerun(class) {
		t.Fatal("api_blocked_suspected must not be quality-rerun retryable")
	}
	if FailureClassAllowsBrowserRestart(class) {
		t.Fatal("api_blocked_suspected must not allow browser restart recovery")
	}
}

func TestClassifyQualityFailurePageNotReady(t *testing.T) {
	class := ClassifyQualityFailure(
		"page_not_ready",
		0,
		1,
		0,
		1,
		productPackageDiagnostics{},
	)
	if class != FailurePageNotReady {
		t.Fatalf("class = %q, want %q", class, FailurePageNotReady)
	}
	if !FailureClassRetryableByQualityRerun(class) {
		t.Fatal("page_not_ready should be quality-rerun retryable")
	}
}

func TestClassifyRowsRiskBlocked(t *testing.T) {
	rows := []map[string]string{{"价格状态": priceStatusRiskBlocked, "抓取备注": "whaleguard block"}}
	if class := ClassifyRows(rows); class != FailureRiskChallenge {
		t.Fatalf("class = %q, want %q", class, FailureRiskChallenge)
	}
}

func TestClassifyRowsBusinessUnavailableForSingleResourceMissWithoutDiagnostics(t *testing.T) {
	rows := []map[string]string{{"价格状态": priceStatusUntrustedResourceMiss, "资源ID": "0", "接口ItemID": "0", "抓取备注": "价格质量检查失败: consecutive_resource_miss"}}
	if class := ClassifyRows(rows); class != FailureBusinessUnavailable {
		t.Fatalf("class = %q, want %q", class, FailureBusinessUnavailable)
	}
}

func TestClassifyErrorBrowserFailure(t *testing.T) {
	class := ClassifyError(errors.New("use of closed network connection"), "")
	if class != FailureBrowserFailure {
		t.Fatalf("class = %q, want %q", class, FailureBrowserFailure)
	}
}

func TestClassifyErrorNavigationBlocked(t *testing.T) {
	class := ClassifyError(nil, "navigation failed: net::ERR_ABORTED")
	if class != FailureNavigationBlocked {
		t.Fatalf("class = %q, want %q", class, FailureNavigationBlocked)
	}
}
