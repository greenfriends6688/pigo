package main

import (
	"testing"
	"time"
)

// TestSessionSearchStartsAsAnIcon keeps the sidebar quiet: the field appears
// only after the search button is pressed, and goes away again.
func TestSessionSearchStartsAsAnIcon(t *testing.T) {
	app := isolatedApp(t)
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:session-search-toggle") })
	if app.Exists("id:session-search") {
		t.Fatal("the search field is shown before the search button is pressed")
	}
	app.Press("id:session-search-toggle")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:session-search") })
	app.Press("id:session-search-close")
	app.WaitUntil(5*time.Second, func() bool { return !app.Exists("id:session-search") })
}

// TestSettingsHasNoCodingAgentPage keeps the coding-agent page out of the
// app's settings: the Models page already shows what pi offers.
func TestSettingsHasNoCodingAgentPage(t *testing.T) {
	app := isolatedApp(t)
	app.Press("id:nav-settings")
	app.WaitUntil(5*time.Second, func() bool { return app.Exists("id:settings-dialog") })
	if app.Exists("id:settings-nav-coding-agents") {
		t.Fatal("the settings still list a coding-agent page")
	}
}
