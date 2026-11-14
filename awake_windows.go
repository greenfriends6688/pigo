package main

import (
	"runtime"
	"syscall"
)

var setThreadExecutionState = syscall.NewLazyDLL("kernel32.dll").NewProc("SetThreadExecutionState")

const (
	esContinuous      = 0x80000000
	esSystemRequired  = 0x00000001
	esDisplayRequired = 0x00000002
)

// keepAwake prevents idle sleep, and with display the display turning off
// and the screen locking, from a thread of its own until released; the
// request belongs to that thread.
func keepAwake(_ string, display bool) func() {
	flags := uintptr(esContinuous | esSystemRequired)
	if display {
		flags |= esDisplayRequired
	}
	release := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		setThreadExecutionState.Call(flags)
		<-release
		setThreadExecutionState.Call(esContinuous)
	}()
	return func() { close(release) }
}
