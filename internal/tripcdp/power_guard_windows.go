//go:build windows

package tripcdp

import (
	"fmt"
	"runtime"
	"sync"
	"syscall"
)

const (
	esContinuous       = 0x80000000
	esSystemRequired   = 0x00000001
	esDisplayRequired  = 0x00000002
	esAwayModeRequired = 0x00000040
)

var (
	kernel32                      = syscall.NewLazyDLL("kernel32.dll")
	procSetThreadExecutionState   = kernel32.NewProc("SetThreadExecutionState")
	windowsExecutionStateRequired = uintptr(esContinuous | esSystemRequired | esDisplayRequired | esAwayModeRequired)
	windowsExecutionStateClear    = uintptr(esContinuous)
)

type platformPowerGuard struct {
	done    chan struct{}
	ready   chan error
	stopped chan struct{}
	once    sync.Once
}

func startPlatformSystemAwakeGuard() (*platformPowerGuard, error) {
	guard := &platformPowerGuard{done: make(chan struct{}), ready: make(chan error, 1), stopped: make(chan struct{})}
	go guard.run()
	if err := <-guard.ready; err != nil {
		close(guard.done)
		<-guard.stopped
		return nil, err
	}
	return guard, nil
}

func (g *platformPowerGuard) run() {
	runtime.LockOSThread()
	defer func() {
		runtime.UnlockOSThread()
		close(g.stopped)
	}()
	if err := setWindowsExecutionState(windowsExecutionStateRequired); err != nil {
		g.ready <- err
		return
	}
	g.ready <- nil
	<-g.done
	_ = setWindowsExecutionState(windowsExecutionStateClear)
}

func setWindowsExecutionState(flags uintptr) error {
	ret, _, err := procSetThreadExecutionState.Call(flags)
	if ret == 0 {
		return fmt.Errorf("SetThreadExecutionState failed: %w", err)
	}
	return nil
}

func (g *platformPowerGuard) description() string {
	return "Windows SetThreadExecutionState active; preventing idle system sleep and display sleep while the run is active"
}

func (g *platformPowerGuard) close() {
	if g == nil {
		return
	}
	g.once.Do(func() {
		close(g.done)
		<-g.stopped
	})
}
