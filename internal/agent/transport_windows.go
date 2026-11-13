//go:build windows

package agent

import (
	"os/exec"
	"strconv"
	"syscall"
)

// createNewProcessGroup is CREATE_NEW_PROCESS_GROUP: without it taskkill /T
// cannot see the node process as part of this tree.
const createNewProcessGroup = 0x00000200

func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
}

// terminateGroup asks the tree to stop. Windows has no SIGTERM, and closing
// stdin is already pi's own shutdown signal, so this is taskkill's polite form
// (no /F) and a failure here is expected for console children.
func terminateGroup(pid int) {
	_ = exec.Command("taskkill", "/T", "/PID", strconv.Itoa(pid)).Run()
}

// killGroup takes the tree down without asking.
func killGroup(pid int) {
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run()
}
