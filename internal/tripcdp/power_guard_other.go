//go:build !darwin && !windows

package tripcdp

import "fmt"

type platformPowerGuard struct{}

func startPlatformSystemAwakeGuard() (*platformPowerGuard, error) {
	return nil, fmt.Errorf("unsupported platform")
}

func (g *platformPowerGuard) description() string {
	return "no system awake guard available on this platform"
}

func (g *platformPowerGuard) close() {}
