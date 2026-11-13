//go:build notifyprobe

package main

import (
	"fmt"
	"testing"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// TestNotifyObjects builds the notification without a notification center,
// which needs a bundle: it proves the framework loads and every selector and
// argument of the content and the request is the one the framework knows.
func TestNotifyObjects(t *testing.T) {
	if _, err := purego.Dlopen("/System/Library/Frameworks/UserNotifications.framework/UserNotifications",
		purego.RTLD_NOW|purego.RTLD_GLOBAL); err != nil {
		t.Fatalf("dlopen: %v", err)
	}
	if objc.GetClass("UNMutableNotificationContent") == 0 {
		t.Fatal("UNMutableNotificationContent did not load")
	}
	content := objc.ID(objc.GetClass("UNMutableNotificationContent")).Send(sel("new"))
	if content == 0 {
		t.Fatal("no content")
	}
	content.Send(sel("setTitle:"), nsString("标题"))
	content.Send(sel("setBody:"), nsString("正文"))
	sound := objc.ID(objc.GetClass("UNNotificationSound")).Send(sel("defaultSound"))
	if sound == 0 {
		t.Fatal("no default sound")
	}
	content.Send(sel("setSound:"), sound)
	request := objc.ID(objc.GetClass("UNNotificationRequest")).Send(sel("requestWithIdentifier:content:trigger:"),
		nsString("probe"), content, objc.ID(0))
	if request == 0 {
		t.Fatal("no request")
	}
	desc := objc.ID(request).Send(sel("description"))
	text := objc.Send[string](desc, sel("UTF8String"))
	fmt.Println("request:", text)
	if text == "" {
		t.Fatal("empty description")
	}
}
