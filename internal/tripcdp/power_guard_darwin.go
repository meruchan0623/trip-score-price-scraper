//go:build darwin

package tripcdp

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"
)

type platformPowerGuard struct {
	cmd  *exec.Cmd
	once sync.Once
}

func startPlatformSystemAwakeGuard() (*platformPowerGuard, error) {
	cmd := exec.Command("caffeinate", "-dimsu", "-w", strconv.Itoa(os.Getpid()))
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start caffeinate: %w", err)
	}
	return &platformPowerGuard{cmd: cmd}, nil
}

func (g *platformPowerGuard) description() string {
	return "macOS caffeinate active; preventing idle sleep, display sleep, and system sleep while the run is active"
}

func (g *platformPowerGuard) close() {
	if g == nil || g.cmd == nil {
		return
	}
	g.once.Do(func() {
		if g.cmd.Process != nil {
			_ = g.cmd.Process.Kill()
		}
		_ = g.cmd.Wait()
	})
}
