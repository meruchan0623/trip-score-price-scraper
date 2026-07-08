package tripcdp

import "testing"

func TestRatioReached(t *testing.T) {
	if ratioReached(6, 10, 0.70, 10) {
		t.Fatal("6/10 should not reach 70%")
	}
	if !ratioReached(7, 10, 0.70, 10) {
		t.Fatal("7/10 should reach 70%")
	}
	if ratioReached(7, 9, 0.70, 10) {
		t.Fatal("threshold must respect minimum total")
	}
}

func TestVendorAPIBlockedByResult(t *testing.T) {
	options := defaultRuntimeOptions()
	result := workerResult{URLCount: 20, URLsDone: 12, APIBlockedURLs: 9}
	if !vendorAPIBlockedByResult(result, options) {
		t.Fatal("9/12 should trip vendor api block threshold")
	}
}
