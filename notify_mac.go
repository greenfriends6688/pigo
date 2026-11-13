//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// The notification center is asked for permission once, when the first
// notification is posted: asking at launch would greet every open with a
// permission dialog the user has no reason to answer yet.
var (
	notifyOnce   sync.Once
	notifyCenter objc.ID
)

// platformNotify posts a notification through UserNotifications, so it shows
// as this app's own banner in Notification Center. `osascript display
// notification` was the old way out and showed as Script Editor, which is not
// the app that finished the work.
//
// The framework needs a bundle identifier: a bare binary (`go test`, a dev run
// outside the .app) has none and the center would trap, so the guard asks the
// executable where it lives and stays silent elsewhere. The in-app toast and
// the sound still fire, so a run that ends is never silently missed.
func platformNotify(title, body string) {
	if !runningFromAppBundle() {
		return
	}
	notifyOnce.Do(func() {
		if _, err := purego.Dlopen("/System/Library/Frameworks/UserNotifications.framework/UserNotifications",
			purego.RTLD_NOW|purego.RTLD_GLOBAL); err != nil {
			return
		}
		class := objc.GetClass("UNUserNotificationCenter")
		if class == 0 {
			return
		}
		center := objc.ID(class).Send(sel("currentNotificationCenter"))
		if center == 0 {
			return
		}
		// Alert (1<<2) and sound (1<<1). The answer decides whether the
		// system delivers at all; nothing here depends on it.
		done := objc.NewBlock(func(objc.Block, bool, objc.ID) {})
		center.Send(sel("requestAuthorizationWithOptions:completionHandler:"), uintptr(6), done)
		done.Release()
		notifyCenter = center
	})
	if notifyCenter == 0 {
		return
	}
	content := objc.ID(objc.GetClass("UNMutableNotificationContent")).Send(sel("new"))
	if content == 0 {
		return
	}
	content.Send(sel("setTitle:"), nsString(title))
	content.Send(sel("setBody:"), nsString(body))
	content.Send(sel("setSound:"), objc.ID(objc.GetClass("UNNotificationSound")).Send(sel("defaultSound")))
	request := objc.ID(objc.GetClass("UNNotificationRequest")).Send(sel("requestWithIdentifier:content:trigger:"),
		nsString("pi-web-native"), content, objc.ID(0))
	notifyCenter.Send(sel("addNotificationRequest:withCompletionHandler:"), request, objc.ID(0))
}

// sel registers an Objective-C selector, once per name.
var selectors sync.Map // string -> objc.SEL

func sel(name string) objc.SEL {
	if v, ok := selectors.Load(name); ok {
		return v.(objc.SEL)
	}
	actual, _ := selectors.LoadOrStore(name, objc.RegisterName(name))
	return actual.(objc.SEL)
}

// nsString makes an NSString from a Go string, borrowed for the call only:
// every caller passes a fresh one into a message that copies it.
func nsString(s string) objc.ID {
	b := append([]byte(s), 0)
	return objc.ID(objc.GetClass("NSString")).Send(sel("stringWithUTF8String:"), &b[0])
}

// runningFromAppBundle reports whether this process was started from a .app
// bundle, which is what gives UserNotifications a bundle identifier to post
// under.
func runningFromAppBundle() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	return strings.Contains(filepath.ToSlash(exe), ".app/Contents/MacOS/")
}
