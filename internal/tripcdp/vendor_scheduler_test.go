package tripcdp

import (
	"testing"
	"time"
)

func TestVendorSchedulerSkipsAPIBlockedVendor(t *testing.T) {
	groups := []vendorGroup{{Vendor: "BC", Items: []ScrapeItem{{URL: "u1"}}}, {Vendor: "TSIM", Items: []ScrapeItem{{URL: "u2"}}}}
	scheduler := newVendorScheduler(groups)
	now := time.Now()
	first, ok := scheduler.Next(now)
	if !ok || first.Vendor != "BC" {
		t.Fatalf("first = %+v ok=%v", first, ok)
	}
	scheduler.Complete(workerResult{Vendor: "BC", URLsDone: 10, URLCount: 10, APIBlockedURLs: 8}, defaultRuntimeOptions(), now)
	second, ok := scheduler.Next(now)
	if !ok || second.Vendor != "TSIM" {
		t.Fatalf("second = %+v ok=%v", second, ok)
	}
	if scheduler.State("BC") != VendorStateAPIBlocked {
		t.Fatalf("BC state = %q", scheduler.State("BC"))
	}
}

func TestVendorSchedulerCoolingDelaysVendor(t *testing.T) {
	groups := []vendorGroup{{Vendor: "BC", Items: []ScrapeItem{{URL: "u1"}}}}
	scheduler := newVendorScheduler(groups)
	now := time.Now()
	scheduler.SetCooling("BC", now.Add(time.Minute), "risk")
	if _, ok := scheduler.Next(now); ok {
		t.Fatal("cooling vendor should not be schedulable")
	}
	if _, ok := scheduler.Next(now.Add(2 * time.Minute)); !ok {
		t.Fatal("vendor should be schedulable after cooling expires")
	}
}
