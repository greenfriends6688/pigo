package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/oneo-me/nsgo/base"
	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/icons/lucide"
	"github.com/oneo-me/nsgo/ui"
)

// The project header's menu acts on a whole group: its place in the list, its
// name, its old sessions and its files. Every action is one press, as PI
// NEXT's project menu is; nothing here opens a settings page.

// toggleProjectPin moves a project to the top of the list, or back into the
// order it had.
func (s *appState) toggleProjectPin(group string) {
	if s.projectPin == nil {
		s.projectPin = map[string]bool{}
	}
	s.projectPin[group] = !s.projectPin[group]
	s.savePrefsAsync(s.projectRoot(), s.showHidden.GetUntracked())
	s.refreshProjectOrder()
	s.notify(ui.ToastInfo, "项目顺序已更新", groupPinLabel(s.projectPin[group]))
}

// refreshProjectOrder re-sorts the rows the sidebar already has, so pinning
// or moving a project shows at once without re-reading the library.
func (s *appState) refreshProjectOrder() {
	s.rows.Set(s.orderByProject(s.rows.GetUntracked()))
	s.applySearch(s.query.GetUntracked())
}

func groupPinLabel(pinned bool) string {
	if pinned {
		return "已置顶"
	}
	return "已取消置顶"
}

// moveProject swaps a project with its neighbour in the saved order. The
// order starts from the one on screen, so the first move is the one the user
// saw.
func (s *appState) moveProject(group string, delta int) {
	order := append([]string(nil), s.projectOrder...)
	seen := make(map[string]bool, len(order))
	for _, g := range order {
		seen[g] = true
	}
	// Seed the order with the groups on screen, so a project that was never
	// moved still has a place to move from.
	for _, r := range s.searchRows.GetUntracked() {
		if !seen[r.Group] {
			order = append(order, r.Group)
			seen[r.Group] = true
		}
	}
	at := -1
	for i, g := range order {
		if g == group {
			at = i
			break
		}
	}
	if at < 0 {
		return
	}
	to := at + delta
	if to < 0 || to >= len(order) {
		return
	}
	order[at], order[to] = order[to], order[at]
	s.projectOrder = order
	s.savePrefsAsync(s.projectRoot(), s.showHidden.GetUntracked())
	s.refreshProjectOrder()
}

// beginProjectRename asks for a display name for a project. The group key is
// the project path; only the sidebar's label changes.
func (s *appState) beginProjectRename(group, label string) {
	s.renameTarget.Set("")
	s.renameGroup.Set(group)
	s.renameName.Set(label)
	s.renameOpen.Set(true)
}

// commitProjectRename saves the label, or drops it when the field is emptied.
func (s *appState) commitProjectRename(group string) {
	name := strings.TrimSpace(s.renameName.GetUntracked())
	if s.projectAlias == nil {
		s.projectAlias = map[string]string{}
	}
	if name == "" {
		delete(s.projectAlias, group)
	} else {
		s.projectAlias[group] = name
	}
	s.savePrefsAsync(s.projectRoot(), s.showHidden.GetUntracked())
	s.publishSessions() // the label is baked into the rows
	s.notify(ui.ToastInfo, "项目名称已更新", projectNameOr(group, name))
}

func projectNameOr(group, name string) string {
	if name != "" {
		return name
	}
	return group
}

// archiveOldSessions archives every session of a project older than a week,
// as PI NEXT's "归档 7 天前的会话" does. The files stay; the group's list
// shrinks to what the user is still working on.
func (s *appState) archiveOldSessions(group string, days int) {
	cutoff := time.Now().AddDate(0, 0, -days)
	n, err := s.store.SetAllArchivedBefore(group, cutoff, true)
	if err != nil {
		s.notify(ui.ToastError, "归档失败", err.Error())
		return
	}
	if n == 0 {
		s.notify(ui.ToastInfo, "没有需要归档的会话", fmt.Sprintf("%d 天前的会话已全部归档", days))
		return
	}
	s.publishSessions()
	s.notify(ui.ToastSuccess, "已归档", fmt.Sprintf("%d 个 %d 天前的会话", n, days))
}

// deleteProjectSessions removes every session file of one project after the
// confirmation dialog said so.
func (s *appState) deleteProjectSessions(group string) {
	n, err := s.store.DeleteAll(group)
	if err != nil && n == 0 {
		s.notify(ui.ToastError, "删除失败", err.Error())
		return
	}
	s.publishSessions()
	s.notify(ui.ToastSuccess, "已删除会话", fmt.Sprintf("%d 个会话文件", n))
	if s.selected.GetUntracked() != "" {
		if _, err := os.Stat(s.selected.GetUntracked()); err != nil {
			s.selected.Set("")
		}
	}
}

// setSessionUnread marks a row unread (or read) and repaints the list.
func (s *appState) setSessionUnread(path string, unread bool) {
	if err := s.store.SetUnread(path, unread); err != nil {
		s.notify(ui.ToastWarning, "标记失败", err.Error())
		return
	}
	s.publishSessions()
}

// clearSessionUnread is the open path's half: a session that is opened has
// been read, so its mark goes away.
func (s *appState) clearSessionUnread(path string) {
	for _, r := range s.rows.GetUntracked() {
		if r.ID == path && r.Unread {
			_ = s.store.SetUnread(path, false)
			s.publishSessions()
			return
		}
	}
}

// projectMenuItems is the project header's menu, in PI NEXT's order. The
// counts beside the sweep rows say what each would touch, as PI NEXT's do.
func (s *appState) projectMenuItems(group, label string) []core.Node {
	// The counts come from the last publish: reading the whole library here
	// ran on every repaint of the list, which made a big sidebar lag.
	stat := s.projectStats[group]
	old, archived, total := stat.old, stat.archived, stat.total
	pinned := s.projectPin[group]
	pinLabel, pinIcon := "置顶项目", lucide.Pin
	if pinned {
		pinLabel, pinIcon = "取消置顶", lucide.PinOff
	}
	archiveLabel := "查看已归档"
	if s.archiveView.Get() {
		archiveLabel = "查看全部会话"
	}
	return []core.Node{
		ui.MenuItem{Text: pinLabel, Icon: pinIcon, TestID: "session-group-pin",
			OnSelect: func() { s.toggleProjectPin(group) }},
		ui.MenuItem{Text: "重命名项目…", Icon: lucide.Pencil, TestID: "session-group-rename",
			OnSelect: func() { s.beginProjectRename(group, label) }},
		ui.MenuSeparator{},
		ui.MenuItem{Text: "上移", Icon: lucide.ChevronUp, Disabled: func() bool { return s.projectIsFirst(group) },
			TestID: "session-group-up", OnSelect: func() { s.moveProject(group, -1) }},
		ui.MenuItem{Text: "下移", Icon: lucide.ChevronDown, Disabled: func() bool { return s.projectIsLast(group) },
			TestID: "session-group-down", OnSelect: func() { s.moveProject(group, 1) }},
		ui.MenuItem{Text: "折叠其他项目", Icon: lucide.ChevronRight, TestID: "session-group-open-only",
			OnSelect: func() { s.openOnlyGroup(group) }},
		ui.MenuItem{Text: "展开全部项目", Icon: lucide.ChevronsUpDown, TestID: "session-group-fold-all",
			OnSelect: s.foldAllGroups},
		ui.MenuSeparator{},
		ui.MenuItem{Text: fmt.Sprintf("归档 7 天前的会话 · %d", old), Icon: lucide.Archive,
			Disabled: func() bool { return old == 0 }, TestID: "session-group-archive-old",
			OnSelect: func() { s.archiveOldSessions(group, 7) }},
		ui.MenuItem{Text: fmt.Sprintf("%s · %d", archiveLabel, archived), Icon: lucide.ArchiveRestore,
			TestID: "session-group-archived", OnSelect: func() { s.toggleArchiveView() }},
		ui.MenuSeparator{},
		ui.MenuItem{Text: fmt.Sprintf("删除全部会话… · %d", total), Icon: lucide.Trash, Variant: ui.MenuItemDanger,
			Disabled: func() bool { return total == 0 }, TestID: "session-group-delete-all",
			OnSelect: func() { s.confirmDeleteProject(group, label) }},
		ui.MenuItem{Text: "新建会话", Icon: lucide.Plus, TestID: "session-group-new",
			OnSelect: s.newSession},
	}
}

// projectIsFirst and projectIsLast say whether a project already sits at the
// end of the list, so the menu can grey the move it cannot do.
func (s *appState) projectIsFirst(group string) bool {
	order := s.visibleProjects()
	for i, g := range order {
		if g == group {
			return i == 0
		}
	}
	return true
}

func (s *appState) projectIsLast(group string) bool {
	order := s.visibleProjects()
	for i, g := range order {
		if g == group {
			return i == len(order)-1
		}
	}
	return true
}

// visibleProjects is the sidebar's group order as it shows.
func (s *appState) visibleProjects() []string {
	var order []string
	seen := map[string]bool{}
	for _, r := range s.searchRows.Get() {
		if !seen[r.Group] {
			seen[r.Group] = true
			order = append(order, r.Group)
		}
	}
	return order
}

// confirmDeleteProject keeps the destructive action behind one dialog.
func (s *appState) confirmDeleteProject(group, label string) {
	s.deleteProject.Set(group)
	s.deleteProjectOpen.Set(true)
	_ = label
}

// forkSessionFromRow forks a session the user picked in the sidebar: it is
// opened first (pi forks the session it has open) and the fork runs as soon
// as that session is live.
func (s *appState) forkSessionFromRow(id string) {
	if id == "" {
		return
	}
	if id == s.selected.GetUntracked() && s.connected.GetUntracked() {
		s.forkSession()
		return
	}
	s.forkPending = id
	s.openSession(id)
	if s.selected.GetUntracked() != id {
		s.forkPending = ""
	}
}

// deleteProjectDialog confirms removing every session of a project. The files
// are gone afterwards, so the dialog names the project and the count.
func (s *appState) deleteProjectDialog() core.Node {
	return ui.AlertDialog{
		Open:         s.deleteProjectOpen.Get,
		OnOpenChange: func(open bool, _ base.Reason) { s.deleteProjectOpen.Set(open) },
		Title:        "删除全部会话？",
		Description:  "整个项目的会话文件会从磁盘删除，无法通过本应用恢复。",
		Actions: []ui.AlertAction{
			{Text: "取消", TestID: "delete-project-cancel"},
			{Text: "删除", Variant: ui.ButtonPrimary, Color: func() core.Color { return s.colors.Danger() },
				TestID: "delete-project-submit",
				OnPress: func() {
					group := s.deleteProject.GetUntracked()
					s.deleteProjectOpen.Set(false)
					if group != "" {
						s.deleteProjectSessions(group)
					}
				}},
		},
		TestID: "delete-project-dialog",
	}
}
