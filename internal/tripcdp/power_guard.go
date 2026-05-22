package tripcdp

type powerGuard interface {
	close()
}

type noopPowerGuard struct{}

func (noopPowerGuard) close() {}

func startSystemAwakeGuard(reporter Reporter) powerGuard {
	guard, err := startPlatformSystemAwakeGuard()
	if err != nil {
		if reporter != nil {
			reporter(Event{"event": "warning", "stage": "power_guard", "message": "system awake guard unavailable", "error": err.Error()})
		}
		return noopPowerGuard{}
	}
	if reporter != nil {
		reporter(Event{"event": "power_guard_started", "stage": "power_guard", "message": guard.description()})
	}
	return guard
}
