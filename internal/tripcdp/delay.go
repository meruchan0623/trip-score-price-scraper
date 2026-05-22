package tripcdp

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"
)

type delayOverride struct {
	min *float64
	max *float64
}

type delayOverrides struct {
	page       delayOverride
	scroll     delayOverride
	skuClick   delayOverride
	skuPrice   delayOverride
	skuBetween delayOverride
}

func defaultDelayOptions() DelayOptions {
	options, _ := delayProfileOptions("normal")
	return options
}

func delayProfileOptions(profile string) (DelayOptions, error) {
	switch profile {
	case "", "normal":
		return DelayOptions{Profile: "normal", Page: secondsRange(0.8, 1.5), Scroll: secondsRange(0.2, 0.45), SKUClick: secondsRange(0.15, 0.35), SKUPrice: secondsRange(0.25, 0.55), SKUBetween: secondsRange(0.12, 0.3)}, nil
	case "fast":
		return DelayOptions{Profile: "fast", Page: secondsRange(0.4, 0.9), Scroll: secondsRange(0.1, 0.25), SKUClick: secondsRange(0.08, 0.2), SKUPrice: secondsRange(0.15, 0.35), SKUBetween: secondsRange(0.08, 0.18)}, nil
	case "safe":
		return DelayOptions{Profile: "safe", Page: secondsRange(1.5, 3), Scroll: secondsRange(0.3, 0.7), SKUClick: secondsRange(0.25, 0.6), SKUPrice: secondsRange(0.4, 0.8), SKUBetween: secondsRange(0.2, 0.5)}, nil
	case "slow":
		return DelayOptions{Profile: "slow", Page: secondsRange(1.5, 3), Scroll: secondsRange(0.6, 1.2), SKUClick: secondsRange(0.6, 1.2), SKUPrice: secondsRange(0.8, 1.6), SKUBetween: secondsRange(0.4, 0.9)}, nil
	default:
		return DelayOptions{}, fmt.Errorf("--delay-profile must be fast, normal, safe, or slow")
	}
}

func resolveDelayOptions(profile string, overrides delayOverrides) (DelayOptions, error) {
	options, err := delayProfileOptions(profile)
	if err != nil {
		return DelayOptions{}, err
	}
	if err := applyDelayOverride(&options.Page, overrides.page, "page"); err != nil {
		return DelayOptions{}, err
	}
	if err := applyDelayOverride(&options.Scroll, overrides.scroll, "scroll"); err != nil {
		return DelayOptions{}, err
	}
	if err := applyDelayOverride(&options.SKUClick, overrides.skuClick, "sku-click"); err != nil {
		return DelayOptions{}, err
	}
	if err := applyDelayOverride(&options.SKUPrice, overrides.skuPrice, "sku-price"); err != nil {
		return DelayOptions{}, err
	}
	if err := applyDelayOverride(&options.SKUBetween, overrides.skuBetween, "sku-between"); err != nil {
		return DelayOptions{}, err
	}
	return options, nil
}

func applyDelayOverride(target *DelayRange, override delayOverride, label string) error {
	if override.min != nil {
		if *override.min < 0 {
			return fmt.Errorf("--%s-delay-min must not be negative", label)
		}
		target.Min = secondsDuration(*override.min)
	}
	if override.max != nil {
		if *override.max < 0 {
			return fmt.Errorf("--%s-delay-max must not be negative", label)
		}
		target.Max = secondsDuration(*override.max)
	}
	if target.Min > target.Max {
		return fmt.Errorf("--%s-delay-min must be <= --%s-delay-max", label, label)
	}
	return nil
}

func secondsRange(minSeconds float64, maxSeconds float64) DelayRange {
	return DelayRange{Min: secondsDuration(minSeconds), Max: secondsDuration(maxSeconds)}
}

func secondsDuration(seconds float64) time.Duration {
	return time.Duration(seconds * float64(time.Second))
}

func randomDelay(minDelay time.Duration, maxDelay time.Duration) time.Duration {
	if minDelay <= 0 && maxDelay <= 0 {
		return 0
	}
	if maxDelay < minDelay {
		maxDelay = minDelay
	}
	if minDelay == maxDelay {
		return minDelay
	}
	return minDelay + time.Duration(rand.Int64N(int64(maxDelay-minDelay)+1))
}

func sleepDelay(ctx context.Context, reporter Reporter, stage string, label string, delayRange DelayRange) bool {
	delay := randomDelay(delayRange.Min, delayRange.Max)
	if delay <= 0 {
		return ctx.Err() == nil
	}
	if reporter != nil {
		reporter(Event{"event": "delay_applied", "stage": stage, "label": label, "delay_ms": delay.Milliseconds(), "min_ms": delayRange.Min.Milliseconds(), "max_ms": delayRange.Max.Milliseconds()})
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func sleepFixed(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
