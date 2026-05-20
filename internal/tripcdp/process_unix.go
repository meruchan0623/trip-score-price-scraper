//go:build !windows

package tripcdp

import (
	"os/exec"
	"strings"
	"syscall"
	"time"
)

func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err == nil && pgid > 0 {
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
		time.Sleep(500 * time.Millisecond)
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		return
	}
	_ = cmd.Process.Kill()
}

func killChromeByUserDataDir(userDataDir string) {
	if strings.TrimSpace(userDataDir) == "" {
		return
	}
	_ = exec.Command("pkill", "-TERM", "-f", userDataDir).Run()
	time.Sleep(300 * time.Millisecond)
	_ = exec.Command("pkill", "-KILL", "-f", userDataDir).Run()
}
