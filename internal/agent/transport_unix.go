//go:build !windows

package agent

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the child in its own group so terminating the parent
// (or the whole tree) never misses the node process it started.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminateGroup asks the whole group to stop.
func terminateGroup(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGTERM)
}

// killGroup takes the whole group down without asking.
func killGroup(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}
