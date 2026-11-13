package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/oneo-me/nsgo/base"
	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/icons/lucide"
	"github.com/oneo-me/nsgo/ui"

	"piwebnative/internal/files"
)

// extraFileState holds the signals and bookkeeping this file's features need
// but appState does not declare: content search, unsaved-confirmation, file
// watching, large-file notices, commit details and terminal renaming. It is
// created lazily for an appState, so a window that never opens the dock pays
// nothing for it.
type extraFileState struct {
	contentSearch *core.Signal[bool]
	grepResults   *core.Signal[[]files.Match]
	grepBusy      *core.Signal[bool]
	grepGen       int

	largeNotice *core.Signal[string]

	unsavedOpen *core.Signal[bool]
	unsavedPath string
	unsavedNext func()

	termRenameOpen *core.Signal[bool]
	termRenameName *core.Signal[string]

	commitSel    *core.Signal[string]
	commitDetail *core.Signal[string]
	commitBusy   *core.Signal[bool]

	// stamps is the size and mtime the watcher last saw per open file. The
	// first sighting records without notifying, and a save refreshes it, so
	// the user's own write never reads as an external change.
	stamps map[string]fileStamp
	watch  sync.Once
}

type fileStamp struct {
	size int64
	mod  int64 // UnixNano
}

var (
	extraMu    sync.Mutex
	extraByApp = map[*appState]*extraFileState{}
)

// extras returns the per-window extension state, creating it on first use.
// Signals are created with the app's scope, so they subscribe and dispose
// with the rest of the window.
func (s *appState) extras() *extraFileState {
	extraMu.Lock()
	defer extraMu.Unlock()
	x := extraByApp[s]
	if x != nil {
		return x
	}
	x = &extraFileState{stamps: map[string]fileStamp{}}
	x.contentSearch = core.NewSignal(s.cx, false)
	x.grepResults = core.NewSignalFunc(s.cx, []files.Match(nil), nil)
	x.grepBusy = core.NewSignal(s.cx, false)
	x.largeNotice = core.NewSignal(s.cx, "")
	x.unsavedOpen = core.NewSignal(s.cx, false)
	x.termRenameOpen = core.NewSignal(s.cx, false)
	x.termRenameName = core.NewSignal(s.cx, "")
	x.commitSel = core.NewSignal(s.cx, "")
	x.commitDetail = core.NewSignal(s.cx, "")
	x.commitBusy = core.NewSignal(s.cx, false)
	extraByApp[s] = x
	return x
}

// ---------------------------------------------------------------- file ops

// fileOpTargets are the names the dialog suggests and the directories a new
// entry belongs in. fileOpDir is the parent for a new entry and the existing
// path for rename and delete.
func (s *appState) openFileOp(mode, dir, name string) {
	s.fileOpMode.Set(mode)
	s.fileOpDir.Set(dir)
	s.fileOpName.Set(name)
	s.fileOpOpen.Set(true)
}

// fileOpDialog is the self-contained dialog: a name field for new-file,
// new-folder and rename, and a confirm alert for delete.
func (s *appState) fileOpDialog() core.Node {
	return core.Dynamic(func(*core.Scope) core.Node {
		if !s.fileOpOpen.Get() {
			return nil
		}
		mode := s.fileOpMode.Get()
		name := baseName(s.fileOpDir.Get())
		if mode == "delete" {
			return ui.AlertDialog{
				Open:         s.fileOpOpen.Get,
				OnOpenChange: func(open bool, _ base.Reason) { s.fileOpOpen.Set(open) },
				Title:        "删除「" + name + "」？",
				Description:  "删除后无法恢复；目录会连同其中的全部内容一起删除。",
				Actions: []ui.AlertAction{
					{Text: "取消", TestID: "fileop-cancel", OnPress: func() { s.fileOpOpen.Set(false) }},
					{Text: "删除", Color: s.colors.Danger, TestID: "fileop-submit", OnPress: s.submitFileOp},
				},
				TestID: "fileop-dialog",
			}
		}
		title, description := "新建文件", "创建在 "+s.relativePath(s.fileOpDir.Get())+"；名称不能包含 / 或 \\。"
		switch mode {
		case "new-folder":
			title, description = "新建文件夹", "创建在 "+s.relativePath(s.fileOpDir.Get())+"；名称不能包含 / 或 \\。"
		case "rename":
			title, description = "重命名", "输入新的名称，不能包含 / 或 \\。"
		}
		return ui.Dialog{
			Open:         s.fileOpOpen.Get,
			OnOpenChange: func(open bool, _ base.Reason) { s.fileOpOpen.Set(open) },
			Title:        title,
			Description:  description,
			Children: []core.Node{
				ui.Input{
					Value: s.fileOpName.Get, OnChange: s.fileOpName.Set,
					Placeholder: "名称", TestID: "fileop-input", AutoFocus: true,
					OnSubmit: func(string) { s.submitFileOp() },
					Style:    core.Style{Width: core.Pct(100)},
				},
			},
			Footer: []core.Node{
				ui.Button{Text: "取消", TestID: "fileop-cancel", OnPress: func() { s.fileOpOpen.Set(false) }},
				ui.Button{Text: "确定", Variant: ui.ButtonPrimary, TestID: "fileop-submit", OnPress: s.submitFileOp},
			},
			TestID: "fileop-dialog",
		}
	})
}

// submitFileOp validates the dialog's input and performs the write. Every
// path is resolved inside the project root before the filesystem sees it.
func (s *appState) submitFileOp() {
	mode := s.fileOpMode.GetUntracked()
	root := s.projectRoot()
	if root == "" {
		s.fileOpOpen.Set(false)
		return
	}
	name := strings.TrimSpace(s.fileOpName.GetUntracked())
	switch mode {
	case "new-file", "new-folder", "rename":
		target, err := s.fileOpPath(mode, name)
		if err != nil {
			s.notify(ui.ToastWarning, "名称不可用", err.Error())
			return
		}
		switch mode {
		case "new-file":
			if _, err := os.Lstat(target); err == nil {
				s.notify(ui.ToastWarning, "文件已存在", s.relativePath(target))
				return
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				s.notify(ui.ToastWarning, "新建失败", err.Error())
				return
			}
			if err := os.WriteFile(target, nil, 0o644); err != nil {
				s.notify(ui.ToastWarning, "新建失败", err.Error())
				return
			}
			s.expand(filepath.Dir(target), true)
		case "new-folder":
			if _, err := os.Lstat(target); err == nil {
				s.notify(ui.ToastWarning, "目录已存在", s.relativePath(target))
				return
			}
			if err := os.MkdirAll(target, 0o755); err != nil {
				s.notify(ui.ToastWarning, "新建失败", err.Error())
				return
			}
			s.expand(filepath.Dir(target), true)
		case "rename":
			old := s.fileOpDir.GetUntracked()
			if _, err := os.Lstat(old); err != nil {
				s.notify(ui.ToastWarning, "重命名失败", "原路径不存在")
				return
			}
			if target != old {
				if _, err := os.Lstat(target); err == nil {
					s.notify(ui.ToastWarning, "重命名失败", "同名文件已存在")
					return
				}
				if err := os.Rename(old, target); err != nil {
					s.notify(ui.ToastWarning, "重命名失败", err.Error())
					return
				}
				s.applyFileRename(old, target)
			}
			s.expand(filepath.Dir(target), true)
		}
		s.fileOpOpen.Set(false)
		s.reloadTree()
		s.notify(ui.ToastInfo, "已完成", s.relativePath(target))
	case "delete":
		target := filepath.Clean(s.fileOpDir.GetUntracked())
		if _, err := s.ensureInsideProject(root, target); err != nil {
			s.notify(ui.ToastWarning, "拒绝删除", err.Error())
			return
		}
		if filepath.Clean(target) == filepath.Clean(root) {
			s.notify(ui.ToastWarning, "拒绝删除", "不能删除项目根目录")
			return
		}
		if err := os.RemoveAll(target); err != nil {
			s.notify(ui.ToastWarning, "删除失败", err.Error())
			return
		}
		s.forgetFile(target)
		s.fileOpOpen.Set(false)
		s.reloadTree()
		s.notify(ui.ToastInfo, "已删除", s.relativePath(target))
	default:
		s.fileOpOpen.Set(false)
	}
}

// fileOpPath turns the dialog's name into an absolute target and holds it
// inside the project root. Only the final component may be typed: "/" and
// "\" are refused, as are ".", ".." and control characters.
func (s *appState) fileOpPath(mode, name string) (string, error) {
	root := s.projectRoot()
	if root == "" {
		return "", errors.New("未打开项目")
	}
	if name == "" {
		return "", errors.New("名称不能为空")
	}
	if name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "", errors.New("名称不能包含路径分隔符")
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return "", errors.New("名称不能包含控制字符")
		}
	}
	parent := s.fileOpDir.GetUntracked()
	if mode == "rename" {
		parent = filepath.Dir(parent)
	}
	if parent == "" {
		parent = root
	}
	return s.ensureInsideProject(root, filepath.Join(parent, name))
}

// ensureInsideProject rejects a target outside root. The lexical check runs
// first; for an existing target the resolved path is checked too, so a
// symlinked directory cannot carry a write out of the project.
func (s *appState) ensureInsideProject(root, target string) (string, error) {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", errors.New("路径超出项目目录")
	}
	real, err := resolveExisting(target)
	if err != nil {
		return "", err
	}
	if realRoot, err := filepath.EvalSymlinks(root); err == nil {
		rrel, err := filepath.Rel(realRoot, real)
		if err != nil || rrel == ".." || strings.HasPrefix(rrel, ".."+string(filepath.Separator)) || filepath.IsAbs(rrel) {
			return "", errors.New("路径经符号链接超出项目目录")
		}
	}
	return target, nil
}

// resolveExisting resolves the longest existing prefix of path, so a file
// that is about to be created is still checked through its real parent.
func resolveExisting(path string) (string, error) {
	for {
		if real, err := filepath.EvalSymlinks(path); err == nil {
			return real, nil
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", errors.New("路径不存在")
		}
		path = parent
	}
}

// applyFileRename moves open tabs, drafts and dirty marks to the new path —
// including files under a renamed directory — so a rename does not strand
// the editor on a file that is gone.
func (s *appState) applyFileRename(old, next string) {
	old = filepath.Clean(old)
	next = filepath.Clean(next)
	prefix := old + string(filepath.Separator)
	remap := func(path string) string {
		path = filepath.Clean(path)
		if path == old {
			return next
		}
		if strings.HasPrefix(path, prefix) {
			return filepath.Join(next, strings.TrimPrefix(path, prefix))
		}
		return path
	}

	tabs := s.fileTabs.GetUntracked()
	out := make([]string, 0, len(tabs))
	for _, t := range tabs {
		out = append(out, remap(t))
	}
	s.fileTabs.Set(out)
	if current := s.selectedFile.GetUntracked(); current != "" {
		if moved := remap(current); moved != current {
			s.selectedFile.Set(moved)
			s.selection.Set(moved)
		}
	}

	drafts := make(map[string]string, len(s.fileDrafts))
	for path, text := range s.fileDrafts {
		delete(s.fileDrafts, path)
		drafts[remap(path)] = text
	}
	for path, text := range drafts {
		s.fileDrafts[path] = text
	}

	dirty := s.fileDirty.GetUntracked()
	for path, on := range dirty {
		if !on {
			continue
		}
		if moved := remap(path); moved != path {
			s.setFileDirty(path, false)
			s.setFileDirty(moved, true)
		}
	}

	if pending := s.pendingChange.GetUntracked(); pending != "" {
		if moved := remap(pending); moved != pending {
			s.pendingChange.Set(moved)
		}
	}

	x := s.extras()
	stamps := make(map[string]fileStamp, len(x.stamps))
	for path, stamp := range x.stamps {
		stamps[remap(path)] = stamp
	}
	x.stamps = stamps
}

// forgetFile drops a deleted path — or every open file under a deleted
// directory — from the tabs and the editor without asking about drafts: the
// user confirmed the delete.
func (s *appState) forgetFile(path string) {
	path = filepath.Clean(path)
	prefix := path + string(filepath.Separator)
	gone := func(p string) bool {
		p = filepath.Clean(p)
		return p == path || strings.HasPrefix(p, prefix)
	}
	tabs := s.fileTabs.GetUntracked()
	out := make([]string, 0, len(tabs))
	for _, t := range tabs {
		if !gone(t) {
			out = append(out, t)
		}
	}
	s.fileTabs.Set(out)
	for p := range s.fileDrafts {
		if gone(p) {
			delete(s.fileDrafts, p)
		}
	}
	for p, on := range s.fileDirty.GetUntracked() {
		if on && gone(p) {
			s.setFileDirty(p, false)
		}
	}
	if pending := s.pendingChange.GetUntracked(); pending != "" && gone(pending) {
		s.pendingChange.Set("")
	}
	x := s.extras()
	for p := range x.stamps {
		if gone(p) {
			delete(x.stamps, p)
		}
	}
	if !gone(s.selectedFile.GetUntracked()) {
		return
	}
	s.selectedFile.Set("")
	s.diffPatch.Set("")
	s.filePreview.Set("")
	s.fileImage.Set(core.ImageSource{})
	x.largeNotice.Set("")
	if len(out) > 0 {
		s.openFileNow(out[len(out)-1])
	}
}

// ------------------------------------------------------- unsaved confirm

// confirmUnsaved opens the save/discard/cancel alert for a dirty draft and
// reports whether it took over the action. next runs once the user decided
// to save or discard; cancel leaves everything as it was.
func (s *appState) confirmUnsaved(path string, next func()) bool {
	if path == "" || !s.fileDirty.GetUntracked()[filepath.Clean(path)] {
		return false
	}
	x := s.extras()
	x.unsavedPath = filepath.Clean(path)
	x.unsavedNext = next
	x.unsavedOpen.Set(true)
	return true
}

func (s *appState) unsavedDialog() core.Node {
	return core.Dynamic(func(*core.Scope) core.Node {
		x := s.extras()
		if !x.unsavedOpen.Get() {
			return nil
		}
		return ui.AlertDialog{
			Open: x.unsavedOpen.Get,
			OnOpenChange: func(open bool, _ base.Reason) {
				if !open {
					x.dismissUnsaved()
				}
			},
			Title:       "未保存的修改",
			Description: baseName(x.unsavedPath) + " 有未保存的修改，切换或关闭前要保存吗？",
			Actions: []ui.AlertAction{
				{Text: "取消", TestID: "unsaved-cancel", OnPress: x.dismissUnsaved},
				{Text: "放弃", Color: s.colors.Danger, TestID: "unsaved-discard", OnPress: s.discardUnsaved},
				{Text: "保存", Variant: ui.ButtonPrimary, TestID: "unsaved-save", OnPress: s.saveUnsaved},
			},
			TestID: "unsaved-dialog",
		}
	})
}

// saveUnsaved writes the draft and then runs the pending switch or close. A
// failed write keeps the dialog open, so the draft is never lost.
func (s *appState) saveUnsaved() {
	x := s.extras()
	path, next := x.unsavedPath, x.unsavedNext
	if err := s.writeDraft(path); err != nil {
		s.notify(ui.ToastWarning, "保存失败", err.Error())
		return
	}
	x.dismissUnsaved()
	if next != nil {
		next()
	}
}

// discardUnsaved throws the draft away on purpose, after the user said so.
func (s *appState) discardUnsaved() {
	x := s.extras()
	path, next := x.unsavedPath, x.unsavedNext
	delete(s.fileDrafts, path)
	s.setFileDirty(path, false)
	if s.selectedFile.GetUntracked() == path {
		// The editor shows the disk text again rather than the dropped draft.
		s.loadDiff(path)
	}
	x.dismissUnsaved()
	if next != nil {
		next()
	}
}

// dismissUnsaved closes the alert and forgets the action it held. The draft
// stays in fileDrafts on cancel.
func (x *extraFileState) dismissUnsaved() {
	x.unsavedNext = nil
	x.unsavedPath = ""
	x.unsavedOpen.Set(false)
}

// writeDraft writes one path's unsaved draft back to disk and clears its
// dirty mark, leaving other files untouched. The draft is written over the
// version the file had when it was loaded or last saved; when the file
// changed on disk since then the write is refused and the banner offers a
// reload, so the user's edit never silently overwrites someone else's. A
// file not yet seen (first save) has no stamp and is written as it is.
func (s *appState) writeDraft(path string) error {
	text, ok := s.fileDrafts[path]
	if !ok {
		return nil
	}
	x := s.extras()
	clean := filepath.Clean(path)
	mode := os.FileMode(0o644)
	var current fileStamp
	stated := false
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
		current = fileStamp{size: info.Size(), mod: info.ModTime().UnixNano()}
		stated = true
	}
	// A dirty file's stamp is the version it was loaded from, or the last
	// write's: the watcher leaves it alone while a draft exists. So a
	// mismatch here means the file changed under the draft.
	if stated {
		if stamp, seen := x.stamps[clean]; seen && stamp != current {
			s.pendingChange.Set(clean)
			return fmt.Errorf("文件已被外部修改（%s），请先重新载入再保存", filepath.Base(path))
		}
	}
	if err := os.WriteFile(path, []byte(text), mode); err != nil {
		return err
	}
	delete(s.fileDrafts, path)
	s.setFileDirty(path, false)
	x.noteStamp(path) // the user's write must not read as external
	if s.selectedFile.GetUntracked() == path {
		s.filePreview.Set(text)
		s.loadDiff(path)
	}
	s.notify(ui.ToastInfo, "已保存", s.relativePath(path))
	return nil
}

// ------------------------------------------------------- content search

// onFileQuery stores the query and, in content mode, starts a debounced grep
// so typing does not walk the project once per keystroke.
func (s *appState) onFileQuery(text string) {
	s.fileQuery.Set(text)
	x := s.extras()
	if !x.contentSearch.GetUntracked() {
		return
	}
	x.grepGen++
	gen := x.grepGen
	query := strings.TrimSpace(text)
	if query == "" {
		x.grepResults.Set(nil)
		return
	}
	s.cx.AfterFunc(220*time.Millisecond, func() {
		if x.grepGen == gen {
			s.runGrep(query)
		}
	})
}

// toggleContentSearch flips between filtering names and searching text.
func (s *appState) toggleContentSearch() {
	x := s.extras()
	on := !x.contentSearch.GetUntracked()
	x.contentSearch.Set(on)
	if !on {
		x.grepGen++
		x.grepResults.Set(nil)
		x.grepBusy.Set(false)
		return
	}
	s.runGrep(strings.TrimSpace(s.fileQuery.GetUntracked()))
}

// runGrep searches the project off the UI thread and drops the answer when
// a newer query or another project replaced it.
func (s *appState) runGrep(query string) {
	x := s.extras()
	root := s.projectRoot()
	x.grepGen++
	gen := x.grepGen
	if query == "" || root == "" {
		x.grepResults.Set(nil)
		x.grepBusy.Set(false)
		return
	}
	x.grepBusy.Set(true)
	go func() {
		matches, _ := files.Grep(root, query, files.GrepLimit)
		s.cx.Post(func() {
			if x.grepGen != gen || s.projectRoot() != root {
				return
			}
			x.grepBusy.Set(false)
			x.grepResults.Set(matches)
		})
	}()
}

// fileGrepList renders grep's path:line:text rows; a press opens the file at
// its first match (the editor has no cross-pane scroll target).
func (s *appState) fileGrepList(query string) core.Node {
	x := s.extras()
	matches := x.grepResults.Get()
	busy := x.grepBusy.Get()
	if len(matches) == 0 {
		if busy {
			return ui.Skeleton{Lines: 5, TestID: "files-grep-loading", Style: core.Style{Padding: core.All(6)}}
		}
		return core.Text{Content: "没有匹配「" + query + "」的内容", TestID: "files-grep-empty",
			Style: core.Style{FontSize: 12, Color: s.colors.Muted(), Padding: core.All(8)}}
	}
	root := s.projectRootNow()
	more := len(matches) >= files.GrepLimit
	return core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0)}, Children: []core.Node{
		ui.List{
			Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0)},
			Count: func() int { return len(matches) },
			Key:   func(i int) any { return fmt.Sprintf("%s:%d", matches[i].Path, matches[i].Line) },
			Row: func(cx *core.Scope, r core.ListRow) core.Node {
				i := r.Index()
				if i < 0 || i >= len(matches) {
					return nil
				}
				m := matches[i]
				where := fmt.Sprintf("%s:%d", m.Path, m.Line)
				return core.View{Role: core.RoleButton, Label: where + " " + m.Text, TestID: "files-grep-result",
					OnPress: func(core.PressEvent) { s.openFile(filepath.Join(root, filepath.FromSlash(m.Path))) },
					Style:   core.Style{Gap: 1, Padding: core.Symmetric(4, 6), CornerRadius: 4},
					StyleFunc: func() core.Style {
						return core.Style{Transition: core.Transition{Duration: 120000000, Properties: core.TransitionBackground},
							Hover: &core.Style{Background: ui.Alpha(s.colors.Text(), 0.05)}, Pressed: &core.Style{Background: ui.Alpha(s.colors.Text(), 0.09)}}
					},
					Children: []core.Node{
						core.Text{Content: where, MaxLines: 1,
							Style: core.Style{FontSize: 10.5, FontFamily: core.FontMonospace, Color: s.colors.Muted()}},
						core.Text{Content: m.Text, MaxLines: 1, Style: core.Style{FontSize: 11.5, Color: s.colors.Text()}},
					}}
			},
			EstimatedSize: 40,
			Gap:           1,
			Label:         "内容搜索结果",
			TestID:        "files-grep-list",
		},
		core.Show{When: func() bool { return more }, Then: func(*core.Scope) core.Node {
			return core.Text{Content: fmt.Sprintf("只显示前 %d 条匹配，输入更具体的内容继续缩小", files.GrepLimit),
				TestID: "files-grep-truncated", MaxLines: 2,
				Style: core.Style{FontSize: 10.5, Color: s.colors.Muted(), Padding: core.Edges{Top: 4, Right: 6, Bottom: 6, Left: 6}}}
		}},
	}}
}

// ------------------------------------------------------------ file watch

// startFileWatch starts the two-second stat loop once per window.
func (s *appState) startFileWatch() {
	x := s.extras()
	x.watch.Do(func() { s.watchFiles() })
}

// watchFiles re-arms itself, so tests drive it with the virtual clock the
// session watcher already uses.
func (s *appState) watchFiles() {
	s.cx.AfterFunc(2*time.Second, func() {
		s.checkFilesChanged()
		s.watchFiles()
	})
}

// checkFilesChanged stats only the open tabs. A change to a file with a
// dirty draft is recorded but not announced: the user's own pending edit
// wins, and a later save refreshes the stamp. A change seen while another
// banner already shows keeps its old stamp, so it is announced once the
// first one is handled.
func (s *appState) checkFilesChanged() {
	x := s.extras()
	tabs := s.fileTabs.GetUntracked()
	if len(tabs) == 0 {
		return
	}
	dirty := s.fileDirty.GetUntracked()
	pending := s.pendingChange.GetUntracked()
	for _, path := range tabs {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		path = filepath.Clean(path)
		stamp := fileStamp{size: info.Size(), mod: info.ModTime().UnixNano()}
		prev, seen := x.stamps[path]
		if !seen {
			x.stamps[path] = stamp
			continue
		}
		if prev == stamp {
			continue
		}
		if dirty[path] {
			// The draft is authoritative; a save will refresh the stamp.
			continue
		}
		if pending != "" {
			continue
		}
		x.stamps[path] = stamp
		s.pendingChange.Set(path)
		pending = path
	}
}

// noteStamp records a file's current stat, so the next tick does not report
// the write that just happened as an external change.
func (x *extraFileState) noteStamp(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	x.observeStamp(path, fileStamp{size: info.Size(), mod: info.ModTime().UnixNano()})
}

// observeStamp stores stamp and reports whether it differs from the one
// already known. The first sighting only records.
func (x *extraFileState) observeStamp(path string, stamp fileStamp) bool {
	path = filepath.Clean(path)
	prev, seen := x.stamps[path]
	x.stamps[path] = stamp
	return seen && prev != stamp
}

func (x *extraFileState) forgetStamp(path string) {
	delete(x.stamps, filepath.Clean(path))
}

// pendingChangeBanner is the persistent strip above the file tabs: the file
// changed on disk and holds no draft, so reloading it loses nothing.
func (s *appState) pendingChangeBanner() core.Node {
	return core.Dynamic(func(*core.Scope) core.Node {
		path := s.pendingChange.Get()
		if path == "" {
			return nil
		}
		return core.View{TestID: "file-change-banner",
			Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6, FlexShrink: 0,
				Padding: core.Edges{Top: 4, Right: 6, Bottom: 4, Left: 8}, CornerRadius: 5, Margin: core.Edges{Bottom: 2}},
			StyleFunc: func() core.Style { return core.Style{Background: ui.Alpha(s.colors.Warn(), 0.12)} },
			Children: []core.Node{
				ui.Icon(lucide.FileExclamationPoint, 13, s.colors.Warn),
				core.Text{Content: "文件已变化：" + baseName(path), MaxLines: 1, Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), FontSize: 11, Color: s.colors.Text()}},
				ui.Button{Text: "重新加载", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "file-change-reload",
					OnPress: func() {
						p := s.pendingChange.GetUntracked()
						if p != "" {
							if s.selectedFile.GetUntracked() != p {
								s.openFile(p)
							} else {
								s.loadDiff(p)
							}
						}
						s.pendingChange.Set("")
					}},
				ui.IconButton{Icon: lucide.X, Label: "忽略这次变化", Size: 20, TestID: "file-change-dismiss",
					OnPress: func() {
						// Acknowledge the version on disk, so the next tick
						// does not announce the same change again.
						if p := s.pendingChange.GetUntracked(); p != "" {
							s.extras().noteStamp(p)
						}
						s.pendingChange.Set("")
					}},
			}}
	})
}

// largeFileNotice is the line above a preview that was only partly read.
func (s *appState) largeFileNotice() core.Node {
	return core.Dynamic(func(*core.Scope) core.Node {
		notice := s.extras().largeNotice.Get()
		if notice == "" {
			return nil
		}
		return core.View{TestID: "file-large-notice",
			Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6, FlexShrink: 0,
				Padding: core.Symmetric(3, 8), CornerRadius: 5, Margin: core.Edges{Bottom: 4}},
			StyleFunc: func() core.Style { return core.Style{Background: ui.Alpha(s.colors.Warn(), 0.12)} },
			Children: []core.Node{
				ui.Icon(lucide.TriangleAlert, 12, s.colors.Warn),
				core.Text{Content: notice, MaxLines: 1, Style: core.Style{FontSize: 11, Color: s.colors.Text()}},
			}}
	})
}

// ------------------------------------------------------- terminal rename

// openTermRename asks for a new title for the active shell. Title belongs to
// the app alone; a later shell title will replace it, as it should.
func (s *appState) openTermRename() {
	tab := s.activeTab()
	if tab == nil {
		return
	}
	x := s.extras()
	s.termMu.Lock()
	name := tab.Title
	s.termMu.Unlock()
	x.termRenameName.Set(name)
	x.termRenameOpen.Set(true)
}

func (s *appState) submitTermRename() {
	tab := s.activeTab()
	if tab == nil {
		return
	}
	x := s.extras()
	name := strings.TrimSpace(x.termRenameName.GetUntracked())
	s.termMu.Lock()
	tab.Title = name
	s.termMu.Unlock()
	x.termRenameOpen.Set(false)
	s.termRev.Set(s.termRev.GetUntracked() + 1)
	if name == "" {
		s.notify(ui.ToastInfo, "已恢复终端默认标题", "")
		return
	}
	s.notify(ui.ToastInfo, "已重命名终端", name)
}

func (s *appState) terminalRenameDialog() core.Node {
	return core.Dynamic(func(*core.Scope) core.Node {
		x := s.extras()
		if !x.termRenameOpen.Get() {
			return nil
		}
		return ui.Dialog{
			Open:         x.termRenameOpen.Get,
			OnOpenChange: func(open bool, _ base.Reason) { x.termRenameOpen.Set(open) },
			Title:        "重命名终端标签",
			Description:  "留空则跟随 shell 报告的标题。",
			Children: []core.Node{
				ui.Input{Value: x.termRenameName.Get, OnChange: x.termRenameName.Set,
					Placeholder: "标签名称", TestID: "term-rename-input", AutoFocus: true,
					OnSubmit: func(string) { s.submitTermRename() },
					Style:    core.Style{Width: core.Pct(100)}},
			},
			Footer: []core.Node{
				ui.Button{Text: "取消", TestID: "term-rename-cancel", OnPress: func() { x.termRenameOpen.Set(false) }},
				ui.Button{Text: "保存", Variant: ui.ButtonPrimary, TestID: "term-rename-submit", OnPress: s.submitTermRename},
			},
			TestID: "term-rename-dialog",
		}
	})
}
