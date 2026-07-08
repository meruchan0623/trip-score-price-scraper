package tripcdp

import (
	"context"
	"testing"
	"time"
)

func TestPressureControllerRiskMovesToCautiousThenSafe(t *testing.T) {
	options := defaultRuntimeOptions()
	options.RiskToSafeThreshold = 3
	controller := newAntiBotPressureController(options, nil)
	for i := 0; i < 2; i++ {
		controller.Observe(Event{"event": "risk_challenge_detected", "risk_kind": "whaleguard", "vendor": "BC"})
	}
	if controller.Mode() != PressureCautious {
		t.Fatalf("mode = %q, want cautious", controller.Mode())
	}
	controller.Observe(Event{"event": "risk_challenge_detected", "risk_kind": "whaleguard", "vendor": "BC"})
	if controller.Mode() != PressureSafe {
		t.Fatalf("mode = %q, want safe", controller.Mode())
	}
}

func TestPressureControllerFastURLIncreasesPressure(t *testing.T) {
	options := defaultRuntimeOptions()
	options.FastURLThreshold = 30 * time.Second
	controller := newAntiBotPressureController(options, nil)
	controller.Observe(Event{"event": "url_done", "elapsed_ms": int64(5000), "vendor": "BC"})
	if controller.Mode() != PressureCautious {
		t.Fatalf("mode = %q, want cautious", controller.Mode())
	}
}

func TestPressureControllerEffectiveWorkerLimit(t *testing.T) {
	options := defaultRuntimeOptions()
	options.RiskToSafeThreshold = 1
	controller := newAntiBotPressureController(options, nil)
	if got := controller.EffectiveWorkerLimit(3); got != 3 {
		t.Fatalf("normal workers = %d", got)
	}
	controller.Observe(Event{"event": "risk_challenge_detected", "risk_kind": "whaleguard"})
	if got := controller.EffectiveWorkerLimit(3); got != 1 {
		t.Fatalf("safe workers = %d, want 1", got)
	}
}

func TestPressureControllerDelayIsCancellable(t *testing.T) {
	options := defaultRuntimeOptions()
	options.RiskToSafeThreshold = 1
	controller := newAntiBotPressureController(options, nil)
	controller.Observe(Event{"event": "risk_challenge_detected", "risk_kind": "whaleguard"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if controller.DelayBeforeNextStart(ctx) {
		t.Fatal("delay should stop on canceled context")
	}
}
