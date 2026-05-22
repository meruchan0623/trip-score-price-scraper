package tripcdp

import (
	"context"
	"testing"
	"time"
)

func TestDelayProfileOptions(t *testing.T) {
	tests := []struct {
		profile string
		pageMin time.Duration
		pageMax time.Duration
	}{
		{profile: "fast", pageMin: 400 * time.Millisecond, pageMax: 900 * time.Millisecond},
		{profile: "normal", pageMin: 800 * time.Millisecond, pageMax: 1500 * time.Millisecond},
		{profile: "safe", pageMin: 1500 * time.Millisecond, pageMax: 3 * time.Second},
		{profile: "slow", pageMin: 1500 * time.Millisecond, pageMax: 3 * time.Second},
	}
	for _, tt := range tests {
		options, err := delayProfileOptions(tt.profile)
		if err != nil {
			t.Fatalf("delayProfileOptions(%q) returned error: %v", tt.profile, err)
		}
		if options.Profile != tt.profile {
			t.Fatalf("profile mismatch: got %q want %q", options.Profile, tt.profile)
		}
		if options.Page.Min != tt.pageMin || options.Page.Max != tt.pageMax {
			t.Fatalf("%s page delay = %s-%s, want %s-%s", tt.profile, options.Page.Min, options.Page.Max, tt.pageMin, tt.pageMax)
		}
	}
}

func TestResolveDelayOptionsOverrides(t *testing.T) {
	min := 2.0
	max := 3.0
	options, err := resolveDelayOptions("safe", delayOverrides{
		skuClick: delayOverride{min: &min, max: &max},
	})
	if err != nil {
		t.Fatalf("resolveDelayOptions returned error: %v", err)
	}
	if options.Profile != "safe" {
		t.Fatalf("profile = %q, want safe", options.Profile)
	}
	if options.SKUClick.Min != 2*time.Second || options.SKUClick.Max != 3*time.Second {
		t.Fatalf("sku click override = %s-%s, want 2s-3s", options.SKUClick.Min, options.SKUClick.Max)
	}
	if options.Page.Min != 1500*time.Millisecond || options.Page.Max != 3*time.Second {
		t.Fatalf("page profile default changed unexpectedly: %s-%s", options.Page.Min, options.Page.Max)
	}
}

func TestResolveDelayOptionsRejectsInvalid(t *testing.T) {
	if _, err := resolveDelayOptions("unknown", delayOverrides{}); err == nil {
		t.Fatal("unknown profile should fail")
	}
	min := 3.0
	max := 2.0
	if _, err := resolveDelayOptions("normal", delayOverrides{page: delayOverride{min: &min, max: &max}}); err == nil {
		t.Fatal("min > max should fail")
	}
	negative := -0.5
	if _, err := resolveDelayOptions("normal", delayOverrides{scroll: delayOverride{min: &negative}}); err == nil {
		t.Fatal("negative override should fail")
	}
}

func TestRandomDelayRange(t *testing.T) {
	fixed := randomDelay(2*time.Second, 2*time.Second)
	if fixed != 2*time.Second {
		t.Fatalf("fixed randomDelay = %s, want 2s", fixed)
	}
	for i := 0; i < 100; i++ {
		delay := randomDelay(10*time.Millisecond, 20*time.Millisecond)
		if delay < 10*time.Millisecond || delay > 20*time.Millisecond {
			t.Fatalf("delay out of range: %s", delay)
		}
	}
}

func TestSleepDelayCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	ok := sleepDelay(ctx, nil, "test", "cancel", DelayRange{Min: time.Second, Max: time.Second})
	if ok {
		t.Fatal("sleepDelay should return false when context is canceled")
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("sleepDelay did not cancel promptly: %s", elapsed)
	}
}
