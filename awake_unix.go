//go:build !windows

package main

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
)

// keepAwake prevents idle sleep, and with display idle display sleep and
// the screen lock that follows it, with the system's own tool until released
// or pigo exits: caffeinate on macOS; on Linux gnome-session-inhibit
// for the display where GNOME provides it, otherwise systemd-inhibit. Where
// neither exists it does nothing.
func keepAwake(reason string, display bool) func() {
	pid := strconv.Itoa(os.Getpid())
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		flags := "-i"
		if display {
			flags = "-di"
		}
		cmd = exec.Command("/usr/bin/caffeinate", flags, "-w", pid)
	default:
		if path, err := exec.LookPath("gnome-session-inhibit"); err == nil && display {
			cmd = exec.Command(path, "--inhibit", "idle:suspend", "--app-id", "pigo", "--reason", reason, "tail", "--pid="+pid, "-f", "/dev/null")
			break
		}
		path, err := exec.LookPath("systemd-inhibit")
		if err != nil {
			return func() {}
		}
		cmd = exec.Command(path, "--what=idle", "--who=pigo", "--why="+reason, "--mode=block", "tail", "--pid="+pid, "-f", "/dev/null")
	}
	if err := cmd.Start(); err != nil {
		return func() {}
	}
	go cmd.Wait()
	return func() { cmd.Process.Kill() }
}
