package tripcdp

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type PressureMode string

const (
	PressureNormal    PressureMode = "normal"
	PressureCautious  PressureMode = "cautious"
	PressureSafe      PressureMode = "safe"
	PressureRecovery  PressureMode = "recovery"
	PressureTerminate PressureMode = "terminate"
)

type antiBotPressureController struct {
	mu              sync.Mutex
	options         RuntimeOptions
	reporter        Reporter
	mode            PressureMode
	globalScore     int
	consecutiveRisk int
	vendorScore     map[string]int
}

func newAntiBotPressureController(options RuntimeOptions, reporter Reporter) *antiBotPressureController {
	return &antiBotPressureController{options: options, reporter: reporter, mode: PressureNormal, vendorScore: map[string]int{}}
}

func (c *antiBotPressureController) Mode() PressureMode {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mode
}

func (c *antiBotPressureController) Observe(event Event) {
	if c == nil || event == nil {
		return
	}
	vendor := fmt.Sprint(event["vendor"])
	c.mu.Lock()
	oldMode := c.mode
	switch event["event"] {
	case "risk_challenge_detected":
		c.globalScore += 5
		c.consecutiveRisk++
		if vendor != "" {
			c.vendorScore[vendor] += 5
		}
	case "url_done":
		if elapsedMS, ok := numericEventValue(event["elapsed_ms"]); ok && c.options.FastURLThreshold > 0 && time.Duration(elapsedMS)*time.Millisecond < c.options.FastURLThreshold {
			c.globalScore += 2
			if vendor != "" {
				c.vendorScore[vendor] += 2
			}
		} else if c.globalScore > 0 {
			c.globalScore--
			c.consecutiveRisk = 0
		}
	case "price_quality_recovered":
		if c.globalScore > 0 {
			c.globalScore--
		}
	}
	c.recomputeModeLocked()
	newMode := c.mode
	c.mu.Unlock()
	if oldMode != newMode && c.reporter != nil {
		c.reporter(Event{"event": "pressure_mode_changed", "stage": "risk", "from": oldMode, "to": newMode})
	}
}

func (c *antiBotPressureController) recomputeModeLocked() {
	if c.consecutiveRisk >= max(1, c.options.RiskToSafeThreshold) || c.globalScore >= 15 {
		c.mode = PressureSafe
		return
	}
	if c.globalScore >= 2 {
		c.mode = PressureCautious
		return
	}
	if c.mode == PressureSafe || c.mode == PressureCautious {
		c.mode = PressureRecovery
		return
	}
	c.mode = PressureNormal
}

func (c *antiBotPressureController) EffectiveWorkerLimit(configured int) int {
	if configured < 1 {
		configured = 1
	}
	switch c.Mode() {
	case PressureSafe:
		return 1
	case PressureCautious, PressureRecovery:
		return max(1, min(configured, 2))
	default:
		return configured
	}
}

func (c *antiBotPressureController) DelayBeforeNextStart(ctx context.Context) bool {
	if c == nil {
		return ctx.Err() == nil
	}
	delay := time.Duration(0)
	switch c.Mode() {
	case PressureSafe:
		delay = 20 * time.Second
	case PressureCautious, PressureRecovery:
		delay = 8 * time.Second
	}
	return cancellableSleep(ctx, delay)
}

func numericEventValue(value any) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		return int64(v), true
	default:
		return 0, false
	}
}
