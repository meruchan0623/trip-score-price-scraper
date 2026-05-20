//go:build windows

package tripcdp

import (
	"os/exec"
	"strconv"
	"strings"
)

func setProcessGroup(cmd *exec.Cmd) {}

func terminateProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
}

func killChromeByUserDataDir(userDataDir string) {
	if strings.TrimSpace(userDataDir) == "" {
		return
	}
	// Chrome child processes on Windows are already covered when chromedriver is alive.
}
