package main

import (
	"fmt"

	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/widget"
)

// updateWidgets writes the desktop widget's timeline: the selected session,
// its project and how many sessions the library holds. It is cheap (one JSON
// file) and runs off the UI thread; a build without the widget extension just
// reports ErrNoWidgets, which is not an error worth showing.
func (s *appState) updateWidgets() {
	title, project := "Pi Web Native", "选择项目"
	if sel := s.selected.GetUntracked(); sel != "" {
		for _, r := range s.rows.GetUntracked() {
			if r.ID == sel {
				title, project = r.Title, r.Project
				break
			}
		}
	}
	count := len(s.rows.GetUntracked())
	running := s.running.GetUntracked()
	// WidgetKit reloads the whole timeline for every write, and the library
	// changes on every message while a session runs. Only what the widget
	// shows is worth a write.
	signature := fmt.Sprintf("%s\x00%s\x00%d\x00%t", project, title, count, running)
	if signature == s.widgetSignature {
		return
	}
	s.widgetSignature = signature
	go func() {
		status := "空闲"
		if running {
			status = "运行中"
		}
		small := widget.Column{Style: widget.Style{AlignItems: widget.AlignStart, FillWidth: true, FillHeight: true, Gap: 4}, Children: []widget.View{
			widget.Text{Content: project, Style: widget.Style{TextStyle: widget.Caption, Color: widget.Secondary}},
			widget.Text{Content: title, Style: widget.Style{TextStyle: widget.Headline}},
			widget.Spacer{},
			widget.Text{Content: fmt.Sprintf("%d 个会话 · %s", count, status), Style: widget.Style{TextStyle: widget.Caption, Color: widget.Secondary}},
		}}
		_ = widget.Update("session", widget.Entry{
			View: small,
			Families: map[widget.Family]widget.View{
				widget.Medium: widget.Row{Style: widget.Style{Gap: 16, FillWidth: true, FillHeight: true}, Children: []widget.View{
					small,
					widget.Column{Style: widget.Style{AlignItems: widget.AlignEnd, Gap: 2}, Children: []widget.View{
						widget.Text{Content: fmt.Sprintf("%d", count), Style: widget.Style{FontSize: 34, FontWeight: 700, FontVariant: core.FontTabularNums, Accented: true}},
						widget.Text{Content: status, Style: widget.Style{TextStyle: widget.Caption, Color: widget.Secondary}},
					}},
				}},
			},
		})
	}()
}
