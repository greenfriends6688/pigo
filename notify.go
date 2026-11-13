package main

import (
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/oneo-me/nsgo/ui"

	"pigo/internal/reducer"
)

// notifyDesktop shows the platform's own notification when a turn finishes.
func notifyDesktop(title, body string) {
	if os.Getenv("PIGO_NO_AGENT") == "1" {
		return
	}
	platformNotify(title, body)
}

// notifySound plays the system alert tone. Start returns at once, so the UI
// thread never waits for afplay; other targets and test mode stay silent.
func notifySound() {
	if runtime.GOOS != "darwin" || os.Getenv("PIGO_NO_AGENT") == "1" {
		return
	}
	_ = exec.Command("afplay", "/System/Library/Sounds/Glass.aiff").Start()
}

// notifyTurnDone tells the user a run ended while they may be looking
// elsewhere. The three channels are independent, so any combination works
// and all of them off means a silent finish.
func (s *appState) notifyTurnDone(snap reducer.Snapshot) {
	prompt := lastUserText(snap)
	if prompt == "" {
		return
	}
	if s.notifyDone.GetUntracked() {
		notifyDesktop("pigo · 回答完成", prompt)
	}
	if s.notifySound.GetUntracked() {
		notifySound()
	}
	if s.notifyInApp.GetUntracked() {
		s.notify(ui.ToastInfo, "回答完成", prompt)
	}
}

// lastUserText is the newest prompt, for the notification body.
func lastUserText(snap reducer.Snapshot) string {
	for i := len(snap.Messages) - 1; i >= 0; i-- {
		if snap.Messages[i].Role == "user" && snap.Messages[i].Text != "" {
			return truncateRunes(strings.TrimSpace(snap.Messages[i].Text), 80)
		}
	}
	return ""
}
