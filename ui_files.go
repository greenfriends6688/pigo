package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/oneo-me/nsgo/base"
	"github.com/oneo-me/nsgo/base/datatable"
	"github.com/oneo-me/nsgo/base/editor"
	"github.com/oneo-me/nsgo/base/textview"
	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/icons/lucide"
	"github.com/oneo-me/nsgo/ui"
	uidiff "github.com/oneo-me/nsgo/ui/diff"

	"pigo/internal/files"
)

// filesEntry is the row model of the native file tree.
type filesEntry = files.Entry

// dirCache keeps one directory listing per path. ui.Tree reads Children
// during render, so a child list must be available synchronously; the first
// read starts the load and the tree re-renders when treeRev moves.
type dirCache struct {
	mu    sync.Mutex
	dirs  map[string][]filesEntry
	asked map[string]bool
	// index maps a path to its entry, so a row's icon and directory test
	// cost a lookup instead of a scan of its folder.
	index map[string]filesEntry
}

func newDirCache() *dirCache {
	return &dirCache{dirs: make(map[string][]filesEntry), asked: make(map[string]bool), index: make(map[string]filesEntry)}
}

// ensureDir loads dir once and reports whether a load was started. A failed
// read clears its mark so a later refresh retries instead of caching an empty
// directory forever.
func (s *appState) ensureDir(dir string) bool {
	s.dirs.mu.Lock()
	if s.dirs.asked[dir] {
		s.dirs.mu.Unlock()
		return false
	}
	s.dirs.asked[dir] = true
	hidden := s.showHidden.GetUntracked()
	s.dirs.mu.Unlock()
	go func() {
		entries, err := files.ListHidden(dir, 2000, hidden)
		s.cx.Post(func() {
			s.dirs.mu.Lock()
			if err != nil {
				delete(s.dirs.asked, dir)
				s.status.Set("列目录失败：" + err.Error())
			} else {
				s.dirs.dirs[dir] = entries
				for _, e := range entries {
					s.dirs.index[e.Path] = e
				}
			}
			s.dirs.mu.Unlock()
			s.treeRev.Set(s.treeRev.GetUntracked() + 1)
		})
	}()
	return true
}

func (s *appState) dirEntries(dir string) []filesEntry {
	s.dirs.mu.Lock()
	defer s.dirs.mu.Unlock()
	return s.dirs.dirs[dir]
}

// dirLoading reports a first read of dir that has not come back yet.
func (s *appState) dirLoading(dir string) bool {
	s.dirs.mu.Lock()
	defer s.dirs.mu.Unlock()
	_, loaded := s.dirs.dirs[dir]
	return s.dirs.asked[dir] && !loaded
}

func (s *appState) expand(key any, on bool) {
	path, _ := key.(string)
	s.expanded.Update(func(m map[string]bool) map[string]bool {
		out := make(map[string]bool, len(m)+1)
		for k, v := range m {
			out[k] = v
		}
		if on {
			out[path] = true
		} else {
			delete(out, path)
		}
		return out
	})
}

func (s *appState) isExpanded(key any) bool {
	path, _ := key.(string)
	return s.expanded.Get()[path]
}

// projectRoot is the directory the file tree, terminals and new sessions use.
// It is empty until the user opens a project: a tree rooted at the home
// directory would show every file on the machine.
func (s *appState) projectRoot() string {
	return s.fileDir.GetUntracked()
}

// projectRootNow is the tracked read for rendering: the dock repaints when
// the user opens another project.
func (s *appState) projectRootNow() string {
	return s.fileDir.Get()
}

// roots are the project's top-level entries.
func (s *appState) roots() []any {
	root := s.projectRoot()
	if root == "" {
		return nil
	}
	s.ensureDir(root)
	entries := s.dirEntries(root)
	out := make([]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Path)
	}
	return out
}

func (s *appState) children(key any) []any {
	path, _ := key.(string)
	if path == "" || !filepath.IsAbs(path) || !s.isDir(path) {
		return nil
	}
	s.ensureDir(path)
	entries := s.dirEntries(path)
	out := make([]any, 0, len(entries))
	for _, e := range entries {
		if e.IsDir {
			out = append(out, e.Path)
		}
	}
	for _, e := range entries {
		if !e.IsDir {
			out = append(out, e.Path)
		}
	}
	return out
}

// treeRow draws one tree row: a folder or file glyph, the base name and the
// file's git state. The row itself takes the press so one click both selects
// and previews; ui.Tree's own activation is a double press.
func (s *appState) treeRow(cx *core.Scope, item ui.TreeItem) core.Node {
	path, _ := item.Key().(string)
	entry := s.entryAt(path)
	icon := fileIcon(path, entry.IsDir, s.isExpanded(path))
	colors := ui.ColorsOf(cx)
	font := core.Points(s.treeFont.Get())
	style := core.Style{FontSize: font, Color: colors.Text()}
	if s.selectedFile.Get() == path {
		style.FontWeight = core.FontWeightSemibold
		style.Color = colors.Accent()
	}
	if strings.HasPrefix(entry.Name, ".") {
		style.Color = colors.Muted()
	}
	children := []core.Node{
		ui.Icon(icon, 13, colors.Muted),
		core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}, Children: []core.Node{
			core.Text{Content: baseName(path), MaxLines: 1, Style: style},
		}},
	}
	if code, dirDirty := s.gitMarkFor(path, entry.IsDir); code != "" || dirDirty {
		children = append(children, s.gitBadge(cx, code, dirDirty))
	}
	row := core.View{
		Role: core.RoleButton, Label: baseName(path),
		OnPress: func(core.PressEvent) { s.activate(path) },
		Style:   core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6, MinWidth: core.Pt(0), Padding: core.Symmetric(4, 2), CornerRadius: 4},
		StyleFunc: func() core.Style {
			return core.Style{Transition: core.Transition{Duration: 120000000, Properties: core.TransitionBackground},
				Hover: &core.Style{Background: ui.Alpha(colors.Text(), 0.05)}, Pressed: &core.Style{Background: ui.Alpha(colors.Text(), 0.09)}}
		},
		Children: children,
	}
	// A file creates beside itself, a folder inside itself.
	newDir := filepath.Dir(path)
	if entry.IsDir {
		newDir = path
	}
	items := []core.Node{
		ui.MenuItem{Text: "打开", TestID: "file-menu-open", OnSelect: func() { s.activate(path) }},
		ui.MenuItem{Text: "在 Finder 中显示", TestID: "file-menu-finder", OnSelect: func() { s.revealInFinder(path) }},
		ui.MenuSeparator{},
		ui.MenuItem{Text: "新建文件…", TestID: "file-menu-new-file", OnSelect: func() {
			s.openFileOp("new-file", newDir, "")
		}},
		ui.MenuItem{Text: "新建文件夹…", TestID: "file-menu-new-folder", OnSelect: func() {
			s.openFileOp("new-folder", newDir, "")
		}},
		ui.MenuItem{Text: "重命名…", TestID: "file-menu-rename", OnSelect: func() {
			s.openFileOp("rename", path, entry.Name)
		}},
		ui.MenuItem{Text: "删除…", TestID: "file-menu-delete", OnSelect: func() {
			s.openFileOp("delete", path, entry.Name)
		}},
		ui.MenuSeparator{},
		ui.MenuItem{Text: "复制路径", TestID: "file-menu-copy", OnSelect: func() {
			s.cx.SetClipboard(path)
			s.notify(ui.ToastInfo, "已复制路径", path)
		}},
		ui.MenuItem{Text: "复制相对路径", TestID: "file-menu-copy-rel", OnSelect: func() {
			s.cx.SetClipboard(s.relativePath(path))
			s.notify(ui.ToastInfo, "已复制相对路径", s.relativePath(path))
		}},
	}
	if !entry.IsDir {
		items = append(items,
			ui.MenuSeparator{},
			ui.MenuItem{Text: "用系统应用打开", TestID: "file-menu-external", OnSelect: func() { s.openExternal(path, mimeOf(path)) }},
			ui.MenuItem{Text: "插入到输入框", TestID: "file-menu-mention", OnSelect: func() {
				s.insertMention(s.relativePath(path))
			}},
		)
	}
	return ui.ContextMenu{Trigger: row, TestID: "file-menu", Children: items}
}

// gitBadge shows a file's status letter or a directory's "contains changes"
// dot.
func (s *appState) gitBadge(cx *core.Scope, code string, dirDirty bool) core.Node {
	colors := ui.ColorsOf(cx)
	label, color := code, colors.Muted()
	if dirDirty {
		label, color = "•", colors.Warn()
	}
	switch code {
	case "M":
		color = colors.Warn()
	case "A", "U":
		color = colors.Up()
	case "D":
		color = colors.Danger()
	case "R":
		color = colors.Accent()
	}
	return core.Text{Content: label, Style: core.Style{FontSize: 10, FontFamily: core.FontMonospace, FontWeight: core.FontWeightSemibold, Color: color}}
}

// gitMarkFor returns the file's status code, or for a directory whether any
// change lies under it.
func (s *appState) gitMarkFor(path string, dir bool) (string, bool) {
	marks := s.gitMarks.Get()
	if len(marks) == 0 {
		return "", false
	}
	if !dir {
		return marks[path], false
	}
	prefix := path
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	for marked := range marks {
		if strings.HasPrefix(marked, prefix) {
			return "", true
		}
	}
	return "", false
}

func (s *appState) entryAt(path string) filesEntry {
	s.dirs.mu.Lock()
	e, ok := s.dirs.index[path]
	s.dirs.mu.Unlock()
	if ok {
		return e
	}
	return filesEntry{Name: baseName(path), Path: path, IsDir: isDir(path)}
}

// activate opens a folder or selects a file for the preview beside the tree.
func (s *appState) activate(key any) {
	path, _ := key.(string)
	if path == "" {
		return
	}
	if s.isDir(path) {
		s.expand(path, !s.isExpanded(path))
		return
	}
	s.openFile(path)
}

// openFile adds a tab for path and shows it. A dirty draft in the file on
// screen asks first, so switching never drops half-finished edits.
func (s *appState) openFile(path string) {
	if path == "" {
		return
	}
	if current := s.selectedFile.GetUntracked(); current != "" && filepath.Clean(current) != filepath.Clean(path) {
		if s.confirmUnsaved(current, func() { s.openFileNow(path) }) {
			return
		}
	}
	s.openFileNow(path)
}

// openFileNow is openFile without the unsaved check, for callers that just
// answered it.
func (s *appState) openFileNow(path string) {
	tabs := append([]string(nil), s.fileTabs.Get()...)
	found := false
	for _, t := range tabs {
		if t == path {
			found = true
			break
		}
	}
	if !found {
		tabs = append(tabs, path)
		s.fileTabs.Set(tabs)
	}
	s.selectedFile.Set(path)
	s.selection.Set(path)
	// The file becomes a tab of the panel and shows itself: the tree keeps its
	// tab, so reading a file never hides the way back to it.
	s.dock.Set("file:" + path)
	s.rightOpen.Set(true)
	s.savePrefsAsync(s.projectRoot(), s.showHidden.GetUntracked())
	// A picture opens at fit width: the zoom belongs to what is being read,
	// so another file starts fresh. A reload of the same file (loadDiff,
	// the file watcher) leaves the level alone.
	s.setPreviewZoom(previewZoomDefault)
	if code, _ := s.gitMarkFor(path, false); code != "" && code != "U" {
		s.fileMode.Set("diff")
	} else if isMarkdownPath(path) {
		s.fileMode.Set("preview")
	} else {
		s.fileMode.Set("source")
	}
	s.startFileWatch()
	s.loadDiff(path)
}

// editedFile is the text the editor shows: the unsaved draft when one
// exists, else the last text read from disk.
func (s *appState) editedFile(path string) string {
	if text, ok := s.fileDrafts[path]; ok {
		return text
	}
	return s.filePreview.Get()
}

// onFileEdit keeps a change in memory and marks the tab dirty; ⌘S writes it.
func (s *appState) onFileEdit(path, text string) {
	if path == "" || text == s.filePreview.GetUntracked() {
		delete(s.fileDrafts, path)
		s.setFileDirty(path, false)
		return
	}
	s.fileDrafts[path] = text
	s.setFileDirty(path, true)
}

func (s *appState) setFileDirty(path string, dirty bool) {
	s.fileDirty.Update(func(m map[string]bool) map[string]bool {
		out := make(map[string]bool, len(m)+1)
		for k, v := range m {
			out[k] = v
		}
		if dirty {
			out[path] = true
		} else {
			delete(out, path)
		}
		return out
	})
}

// saveFile writes the active tab's draft back to disk and clears its dirty
// mark. A failed write keeps the draft, so nothing is lost.
func (s *appState) saveFile() {
	path := s.selectedFile.GetUntracked()
	if path == "" {
		return
	}
	if _, ok := s.fileDrafts[path]; !ok {
		return
	}
	if err := s.writeDraft(path); err != nil {
		s.notify(ui.ToastWarning, "保存失败", err.Error())
		return
	}
	s.reloadTree()
}

// revealInFinder selects the file in the system file manager.
func (s *appState) revealInFinder(path string) {
	if runtime.GOOS == "darwin" {
		if err := exec.Command("open", "-R", path).Start(); err == nil {
			return
		}
	}
	s.openExternal(filepath.Dir(path), "inode/directory")
}

// closeFileTab closes a tab, asking first when it holds an unsaved draft.
func (s *appState) closeFileTab(path string) {
	if s.fileDirty.GetUntracked()[filepath.Clean(path)] {
		if s.confirmUnsaved(path, func() { s.closeFileTabNow(path) }) {
			return
		}
	}
	s.closeFileTabNow(path)
}

// closeFileTabNow drops the tab and picks the last remaining file, without
// an unsaved check.
func (s *appState) closeFileTabNow(path string) {
	tabs := s.fileTabs.GetUntracked()
	out := make([]string, 0, len(tabs))
	for _, t := range tabs {
		if t != path {
			out = append(out, t)
		}
	}
	s.fileTabs.Set(out)
	s.extras().forgetStamp(path)
	if filepath.Clean(s.pendingChange.GetUntracked()) == filepath.Clean(path) {
		s.pendingChange.Set("")
	}
	if s.selectedFile.GetUntracked() != path {
		return
	}
	s.selectedFile.Set("")
	s.diffPatch.Set("")
	s.filePreview.Set("")
	s.fileImage.Set(core.ImageSource{})
	s.extras().largeNotice.Set("")
	if len(out) > 0 {
		s.openFileNow(out[len(out)-1])
		return
	}
	// No file tab left: the strip's first tool tab takes the panel back.
	if keys := s.dockTabKeys(); len(keys) > 0 {
		s.openDock(keys[0])
		return
	}
	s.dock.Set("")
}

func (s *appState) isDir(path string) bool {
	s.dirs.mu.Lock()
	e, ok := s.dirs.index[path]
	s.dirs.mu.Unlock()
	if ok {
		return e.IsDir
	}
	return isDir(path)
}

// docExts are the office formats the dock sends to the system viewer, after
// converting Word documents to HTML when textutil is available.
var docExts = map[string]string{
	".pdf": "pdf", ".docx": "docx", ".doc": "docx", ".rtf": "docx",
	".odt": "docx", ".xlsx": "sheet", ".xls": "sheet", ".pptx": "slides", ".ppt": "slides",
}

// imageExts are the formats the dock shows as a picture instead of text.
var imageExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".webp": true, ".bmp": true, ".ico": true, ".heic": true,
}

// loadDiff reads the file's text and, when git knows it, its patch, so the
// dock can show either side. An image goes to the viewer.
func (s *appState) loadDiff(path string) {
	x := s.extras()
	x.noteStamp(path) // a reload is not an external change
	if info, err := os.Stat(path); err == nil && info.Size() > 512<<10 {
		x.largeNotice.Set(fmt.Sprintf("文件较大，只显示前 512KB（共 %d KB）", (info.Size()+1023)/1024))
	} else {
		x.largeNotice.Set("")
	}
	ext := strings.ToLower(filepath.Ext(path))
	if kind, ok := docExts[ext]; ok {
		s.diffPatch.Set("")
		s.filePreview.Set("")
		s.fileImage.Set(core.ImageSource{})
		s.fileDocHTML.Set("")
		s.fileDoc.Set(kind)
		if kind == "docx" {
			go s.convertDocument(path)
		}
		return
	}
	s.fileDoc.Set("")
	s.fileDocHTML.Set("")
	if imageExts[ext] {
		go func() {
			data, err := os.ReadFile(path)
			s.cx.Post(func() {
				if s.selectedFile.GetUntracked() != path {
					return
				}
				s.diffPatch.Set("")
				s.filePreview.Set("")
				if err != nil {
					s.fileImage.Set(core.ImageSource{})
					s.filePreview.Set("读取失败：" + err.Error())
					return
				}
				s.fileImage.Set(core.ImageSource{Data: data})
			})
		}()
		return
	}
	go func() {
		patch, _ := files.GitDiff(s.projectRoot(), path)
		text := ""
		if body, err := files.ReadText(path, 512<<10); err == nil {
			text = body
		} else if err == files.ErrBinary {
			text = "（二进制文件，原生版不预览）"
		} else {
			text = "读取失败：" + err.Error()
		}
		s.cx.Post(func() {
			if s.selectedFile.GetUntracked() != path {
				return
			}
			s.diffPatch.Set(patch)
			s.fileImage.Set(core.ImageSource{})
			s.filePreview.Set(text)
			s.fileTable.Set(parseTableRows(path, text))
			if patch == "" && s.fileMode.GetUntracked() == "diff" {
				s.fileMode.Set("source")
			}
		})
	}()
}

// convertDocument turns a Word document into HTML so the dock can show its
// text, the way pi-web's preview route does. Without textutil the pane falls
// back to opening the file in the system app.
func (s *appState) convertDocument(path string) {
	if runtime.GOOS != "darwin" {
		return
	}
	out, err := exec.Command("textutil", "-convert", "html", "-stdout", path).Output()
	if err != nil {
		return
	}
	const cap = 2 << 20
	if len(out) > cap {
		out = out[:cap]
	}
	s.cx.Post(func() {
		if s.selectedFile.GetUntracked() != path {
			return
		}
		s.fileDocHTML.Set(string(out))
	})
}

// openExternal hands the selected file to the system viewer.
func (s *appState) openExternal(path, mime string) {
	if path == "" {
		return
	}
	s.cx.OpenFile(core.SharedFile{Path: path, Name: baseName(path), MIME: mime}, func(opened bool, err error) {
		if err != nil || !opened {
			s.notify(ui.ToastWarning, "系统应用没有打开文件", path)
		}
	})
}

// filesView is the dock's 文件 tab: the directory alone, full width. It used
// to split into tree and preview, which made the panel's narrow width do two
// jobs and turned every file press into a preview swap; a file now opens a tab
// of its own, and the tree keeps the panel.
func (s *appState) filesView() core.Node {
	return core.Dynamic(func(*core.Scope) core.Node {
		if s.projectRootNow() == "" {
			return s.noProjectView()
		}
		return core.View{
			Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0), Width: core.Pct(100), Height: core.Pct(100)},
			Children: []core.Node{
				s.filesHeader(),
				s.fileTreePane(),
			},
		}
	})
}

// fileTabView is one open file's tab: the change banner over the editor, the
// diff or the picture. It renders the same pane the fullscreen overlay does.
func (s *appState) fileTabView() core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		path, _ := strings.CutPrefix(s.dock.Get(), "file:")
		if path == "" || s.selectedFile.Get() == "" {
			return core.View{TestID: "file-tab-empty", Style: core.Style{FlexGrow: 1, AlignItems: core.AlignCenter,
				JustifyContent: core.JustifyCenter, Gap: 8, Padding: core.All(20)},
				Children: []core.Node{
					ui.Icon(lucide.FileText, 24, s.colors.Muted),
					core.Text{Content: "从文件页签里选一个文件", Style: core.Style{FontSize: 12, Color: s.colors.Muted()}},
				}}
		}
		return core.View{TestID: "files-preview-pane", Style: core.Style{Width: core.Pct(100), Height: core.Pct(100),
			MinWidth: core.Pt(0), MinHeight: core.Pt(0), Direction: core.Column},
			Children: []core.Node{
				s.fileTabHeader(path),
				s.pendingChangeBanner(),
				core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0), Padding: core.All(6)},
					Children: []core.Node{s.filePreviewPane()}},
			}}
	})
}

// fileTabHeader names the file and carries the actions that act on it: save,
// fullscreen, open outside, copy the path, insert as a mention.
func (s *appState) fileTabHeader(path string) core.Node {
	dirty := s.fileDirty.Get()[path]
	label := baseName(path)
	return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 4,
		Padding: core.Edges{Top: 4, Right: 6, Bottom: 4, Left: 10}}, Children: []core.Node{
		core.Text{Content: label, TestID: "file-tab-title", MaxLines: 1,
			Style: core.Style{FontSize: 12, FontWeight: core.FontWeightSemibold, Color: s.colors.Text(), FlexShrink: 1, MinWidth: core.Pt(0)}},
		core.Show{When: func() bool { return dirty }, Then: func(*core.Scope) core.Node {
			return core.View{Style: core.Style{Width: core.Pt(6), Height: core.Pt(6), CornerRadius: 3, Background: s.colors.Accent()}}
		}},
		core.View{Style: core.Style{FlexGrow: 1}},
		core.Show{When: func() bool { return dirty }, Then: func(*core.Scope) core.Node {
			return ui.IconButton{Icon: lucide.Save, Label: "保存文件", TestID: "file-save", Size: 24, OnPress: s.saveFile}
		}},
		ui.IconButton{Icon: lucide.Maximize2, Label: "全屏预览", TestID: "file-fullscreen-open", Size: 24,
			OnPress: func() { s.previewFull.Set(true) }},
		ui.IconButton{Icon: lucide.ExternalLink, Label: "用系统应用打开", TestID: "files-open-external", Size: 24,
			OnPress: func() { s.openExternal(path, mimeOf(path)) }},
		ui.IconButton{Icon: lucide.Copy, Label: "复制文件路径", TestID: "file-copy-path", Size: 24, OnPress: func() {
			s.cx.SetClipboard(path)
			s.notify(ui.ToastInfo, "已复制路径", path)
		}},
		ui.IconButton{Icon: lucide.AtSign, Label: "插入到输入框", TestID: "file-mention", Size: 24, OnPress: func() {
			s.insertMention(s.relativePath(path))
		}},
	}}
}

// fileTreePane is the directory column: a find field over either the tree
// or the flat search results. Both lists sit in a bounded parent, so their
// own scroll keeps working.
func (s *appState) fileTreePane() core.Node {
	return core.View{
		TestID: "files-scroll",
		Style:  core.Style{Height: core.Pct(100), MinWidth: core.Pt(0), MinHeight: core.Pt(0), Direction: core.Column},
		Children: []core.Node{
			core.View{Style: core.Style{Padding: core.Symmetric(6, 6)}, Children: []core.Node{
				core.Dynamic(func(*core.Scope) core.Node {
					content := s.extras().contentSearch.Get()
					placeholder := "搜索文件"
					if content {
						placeholder = "在内容中搜索"
					}
					return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 4}, Children: []core.Node{
						core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}, Children: []core.Node{
							ui.Input{
								Value: s.fileQuery.Get, OnChange: s.onFileQuery,
								Placeholder: placeholder, TestID: "files-search",
								Style: core.Style{Width: core.Pct(100), Height: core.Pt(26), FontSize: 12},
							},
						}},
						ui.IconButton{Icon: lucide.SearchCode, Label: "在内容中搜索", TestID: "files-search-content",
							Size: 26, Selected: s.extras().contentSearch.Get, OnPress: s.toggleContentSearch},
					}}
				}),
			}},
			core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0), Padding: core.Symmetric(2, 4)},
				Children: []core.Node{core.Dynamic(func(*core.Scope) core.Node {
					query := strings.TrimSpace(s.fileQuery.Get())
					if query != "" {
						if s.extras().contentSearch.Get() {
							return s.fileGrepList(query)
						}
						return s.fileSearchList(query)
					}
					_ = s.treeRev.Get() // reload the visible rows after a directory loads
					root := s.projectRootNow()
					s.ensureDir(root)
					if s.dirLoading(root) {
						return ui.Skeleton{Lines: 6, TestID: "files-loading", Style: core.Style{Padding: core.All(6)}}
					}
					return ui.Tree{
						Roots:         s.roots,
						Children:      s.children,
						Expanded:      s.isExpanded,
						OnExpand:      s.expand,
						Row:           s.treeRow,
						Selection:     s.selection,
						OnActivate:    s.activate,
						ToggleLabel:   "展开",
						Label:         "文件",
						EstimatedSize: core.Points(s.treeFont.Get() + 14),
						Empty:         core.Text{Content: "（空目录）", Style: core.Style{FontSize: 12, Color: s.colors.Muted()}},
						TestID:        "files-tree",
						Style:         core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0), Height: core.Pct(100)},
					}
				})}},
		},
	}
}

// fileSearchList shows the project's files matching the find field. It is a
// flat, virtualized list, as ZCode's file search is, so a deep tree still
// answers at once.
func (s *appState) fileSearchList(query string) core.Node {
	needle := strings.ToLower(query)
	var matches []string
	for _, rel := range s.mentions.Get() {
		if strings.Contains(strings.ToLower(rel), needle) {
			matches = append(matches, rel)
			if len(matches) >= 300 {
				break
			}
		}
	}
	if len(matches) == 0 {
		return core.Text{Content: "没有匹配的文件", TestID: "files-search-empty",
			Style: core.Style{FontSize: 12, Color: s.colors.Muted(), Padding: core.All(8)}}
	}
	root := s.projectRootNow()
	return ui.List{
		Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0)},
		Count: func() int { return len(matches) },
		Key:   func(i int) any { return matches[i] },
		Row: func(cx *core.Scope, r core.ListRow) core.Node {
			i := r.Index()
			if i < 0 || i >= len(matches) {
				return nil
			}
			return core.View{Role: core.RoleButton, Label: matches[i], TestID: "files-search-result",
				OnPress: func(core.PressEvent) { s.openFile(filepath.Join(root, matches[i])) },
				Style:   core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6, Padding: core.Symmetric(4, 6), CornerRadius: 4},
				StyleFunc: func() core.Style {
					return core.Style{Transition: core.Transition{Duration: 120000000, Properties: core.TransitionBackground},
						Hover: &core.Style{Background: ui.Alpha(s.colors.Text(), 0.05)}, Pressed: &core.Style{Background: ui.Alpha(s.colors.Text(), 0.09)}}
				},
				Children: []core.Node{
					ui.Icon(lucide.FileText, 12, s.colors.Muted),
					core.Text{Content: matches[i], MaxLines: 1, Style: core.Style{FontSize: 12, Color: s.colors.Text()}},
				}}
		},
		EstimatedSize: 30,
		Gap:           1,
		Label:         "搜索结果",
		TestID:        "files-search-list",
	}
}

// filesHeader names the project and carries the tree's actions.
func (s *appState) filesHeader() core.Node {
	root := s.projectRootNow()
	return core.View{
		Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 4, Padding: core.Edges{Top: 6, Right: 8, Bottom: 2, Left: 10}},
		Children: []core.Node{
			ui.Icon(lucide.FolderOpen, 13, s.colors.Muted),
			core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)}, Children: []core.Node{
				core.Text{Content: projectName(root), TestID: "files-project", MaxLines: 1,
					Style: core.Style{FontSize: 12, FontWeight: core.FontWeightSemibold, Color: s.colors.Text()}},
				core.Text{ContentFunc: s.gitText.Get, TestID: "git-status", MaxLines: 1,
					Style: core.Style{FontSize: 10.5, FontFamily: core.FontMonospace, Color: s.colors.Muted()}},
			}},
			ui.IconButton{Icon: lucide.Eye, Label: "显示隐藏文件", TestID: "files-hidden", Size: 24,
				Selected: s.showHidden.Get, OnPress: s.toggleHidden},
			ui.IconButton{Icon: lucide.ZoomOut, Label: "缩小文件树", TestID: "files-zoom-out", Size: 24,
				OnPress: func() { s.zoomTree(-1) }},
			ui.IconButton{Icon: lucide.ZoomIn, Label: "放大文件树", TestID: "files-zoom-in", Size: 24,
				OnPress: func() { s.zoomTree(1) }},
			ui.IconButton{Icon: lucide.RefreshCw, Label: "刷新文件树", TestID: "files-refresh", Size: 24, OnPress: s.reloadTree},
			ui.IconButton{Icon: lucide.FolderOpen, Label: "打开项目", TestID: "files-open-project", Size: 24, OnPress: s.openProjectPicker},
		},
	}
}

// noProjectView asks for a project instead of walking the home directory.
func (s *appState) noProjectView() core.Node {
	return core.View{
		Style: core.Style{FlexGrow: 1, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Padding: core.All(24), Gap: 10},
		Children: []core.Node{
			ui.Icon(lucide.FolderOpen, 30, s.colors.Muted),
			core.Text{Content: "尚未选择项目", TestID: "files-no-project",
				Style: core.Style{FontSize: 13, FontWeight: core.FontWeightSemibold, Color: s.colors.Text()}},
			core.Text{Content: "文件树、终端和新会话都跟随项目目录", MaxLines: 2,
				Style: core.Style{FontSize: 12, TextAlign: core.TextAlignCenter, Color: s.colors.Muted()}},
			ui.Button{Text: "打开项目…", Variant: ui.ButtonSecondary, TestID: "files-choose-project", OnPress: s.openProjectPicker},
		},
	}
}

// fileTabsBar lists the open files, each with a close button.
func (s *appState) fileTabsBar() core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		tabs := s.fileTabs.Get()
		if len(tabs) == 0 {
			return nil
		}
		colors := ui.ColorsOf(cx)
		dirty := s.fileDirty.Get()
		nodes := make([]core.Node, 0, len(tabs))
		for i, path := range tabs {
			p := path
			active := s.selectedFile.Get() == p
			label := baseName(p)
			if dirty[p] {
				label = "• " + label
			}
			textStyle := core.Style{FontSize: 11.5, Color: colors.Muted()}
			tabStyle := core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 2, FlexShrink: 0, CornerRadius: 5, Padding: core.Edges{Left: 7, Right: 2}}
			if active {
				textStyle.Color = colors.Text()
				textStyle.FontWeight = core.FontWeightSemibold
				tabStyle.Background = ui.Alpha(colors.Text(), 0.08)
			}
			nodes = append(nodes, core.View{TestID: "file-tab-" + itoa(i), Style: tabStyle, Children: []core.Node{
				base.Button{Label: label, OnPress: func(core.PressEvent) { s.openFile(p) },
					Style:    core.Style{Padding: core.Symmetric(3, 0), MaxWidth: core.Pt(150)},
					Children: []core.Node{core.Text{Content: label, MaxLines: 1, Style: textStyle}}},
				ui.IconButton{Icon: lucide.X, Label: "关闭 " + label, Size: 18, OnPress: func() { s.closeFileTab(p) }},
			}})
		}
		return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 2, FlexShrink: 0, Padding: core.Edges{Top: 4, Right: 6, Left: 6}},
			Children: []core.Node{core.Scroll{Horizontal: true, HideIndicator: true, Style: core.Style{FlexGrow: 1, MinWidth: core.Pt(0)}, Children: []core.Node{
				core.View{Style: core.Style{Direction: core.Row, Gap: 2}, Children: nodes},
			}}}}
	})
}

// The picture viewer's zoom: percent of the pane's width, one level per
// window. 100 is the pane's full width — the size every picture had before
// the viewer could zoom — and 200 is the quick "read the pixels" step a press
// on the picture toggles.
const (
	previewZoomMin     = 50
	previewZoomMax     = 400
	previewZoomStep    = 25
	previewZoomDefault = 100
	previewZoomPressed = 200
)

// previewZooms keys the picture zoom by scope, so a second window zooms on
// its own and the level dies with the window instead of leaking into prefs.
// It mirrors debugTicks in ui_debug.go: appState gains no field for it.
var previewZooms sync.Map // *core.Scope -> *core.Signal[int]

func (s *appState) previewZoom() *core.Signal[int] {
	if v, ok := previewZooms.Load(s.cx); ok {
		return v.(*core.Signal[int])
	}
	sig := core.NewSignal(s.cx, previewZoomDefault)
	actual, _ := previewZooms.LoadOrStore(s.cx, sig)
	return actual.(*core.Signal[int])
}

// setPreviewZoom clamps to the bounds the buttons respect, so a caller can
// step past them without checking first.
func (s *appState) setPreviewZoom(zoom int) {
	s.previewZoom().Set(min(max(zoom, previewZoomMin), previewZoomMax))
}

// stepPreviewZoom moves one step, stopping at the bounds.
func (s *appState) stepPreviewZoom(delta int) {
	s.setPreviewZoom(s.previewZoom().GetUntracked() + delta)
}

// togglePreviewZoom flips between the fit width and the quick enlargement: a
// press on the picture reads as "show me that detail", a second press puts it
// back. Any other level returns to 100 rather than guessing 200.
func (s *appState) togglePreviewZoom() {
	if s.previewZoom().GetUntracked() == previewZoomDefault {
		s.setPreviewZoom(previewZoomPressed)
		return
	}
	s.setPreviewZoom(previewZoomDefault)
}

// fileImagePane is the picture half of the preview: a small zoom row over the
// picture, which scrolls vertically. Zooming keeps the level per window and
// across file switches — the level is the reader's, not the file's.
func (s *appState) fileImagePane(img core.ImageSource, colors ui.Colors) core.Node {
	zoom := s.previewZoom()
	level := zoom.Get() // the level the row and the picture are drawn at
	return core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0),
		Direction: core.Column, Gap: 4}, Children: []core.Node{
		core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6, Padding: core.All(4), FlexShrink: 0},
			Children: []core.Node{
				ui.IconButton{Icon: lucide.Minus, Label: "缩小图片", Size: 22, TestID: "image-zoom-out",
					Disabled: func() bool { return zoom.Get() <= previewZoomMin },
					OnPress:  func() { s.stepPreviewZoom(-previewZoomStep) }},
				core.Text{Content: itoa(level) + "%", TestID: "image-zoom-value", MaxLines: 1,
					Style: core.Style{FontSize: fontMeta, FontFamily: core.FontMonospace, TextAlign: core.TextAlignCenter,
						MinWidth: core.Pt(38), Color: colors.Muted()}},
				ui.IconButton{Icon: lucide.Plus, Label: "放大图片", Size: 22, TestID: "image-zoom-in",
					Disabled: func() bool { return zoom.Get() >= previewZoomMax },
					OnPress:  func() { s.stepPreviewZoom(previewZoomStep) }},
				ui.Button{Text: "适应", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "image-zoom-reset",
					OnPress: func() { s.setPreviewZoom(previewZoomDefault) }},
				core.View{Style: core.Style{FlexGrow: 1}}, // the row's right side stays empty
			}},
		// A vertical scroll only: past the pane's width the picture is
		// clipped on the right rather than given a second axis, which the
		// scroll does not carry beside this one.
		core.Scroll{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0)},
			Children: []core.Node{
				core.Image{Source: img, ResizeMode: core.Contain, Label: s.selectedFile.Get(), TestID: "image-preview",
					OnPress: func(core.PressEvent) { s.togglePreviewZoom() },
					Style: core.Style{Width: core.Pct(float32(level)), MinHeight: core.Pt(160),
						CornerRadius: radiusCard}},
			}},
	}}
}

// filePreviewPane shows the active tab: a picture, a source/diff switch over
// the editor, or the empty prompt.
func (s *appState) filePreviewPane() core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		colors := ui.ColorsOf(cx)
		_ = s.filePreview.Get() // the editor re-mounts when the disk text arrives
		_ = s.fileDirty.Get()
		// A picture opens in the zoomable viewer; the fullscreen overlay
		// renders this same pane, so it zooms too.
		if img := s.fileImage.Get(); len(img.Data) > 0 {
			return s.fileImagePane(img, colors)
		}
		switch s.fileDoc.Get() {
		case "pdf", "docx", "sheet", "slides":
			kind := s.fileDoc.Get()
			labels := map[string]string{"pdf": "PDF 文档", "docx": "文档", "sheet": "表格", "slides": "演示文稿"}
			children := []core.Node{
				ui.Icon(lucide.FileText, 30, colors.Muted),
				core.Text{Content: labels[kind], TestID: "files-doc",
					Style: core.Style{FontSize: 13, FontWeight: core.FontWeightSemibold, Color: colors.Text()}},
				core.Text{Content: "NSGo 没有内嵌的 Office/PDF 渲染，可交给系统应用打开。",
					Style: core.Style{FontSize: 12, TextAlign: core.TextAlignCenter, Color: colors.Muted()}},
				ui.Button{Text: "用系统应用打开", Variant: ui.ButtonSecondary, TestID: "file-open-external",
					OnPress: func() { s.openExternal(s.selectedFile.Get(), mimeOf(s.selectedFile.Get())) }},
			}
			if html := s.fileDocHTML.Get(); html != "" {
				return core.Scroll{Style: core.Style{FlexGrow: 1, MinHeight: core.Pt(0)}, Children: []core.Node{
					textview.TextView{HTML: html, Selectable: true, OpenLinks: true},
				}}
			}
			return core.View{Style: core.Style{FlexGrow: 1, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Gap: 8, Padding: core.All(20)}, Children: children}
		}
		if s.selectedFile.Get() == "" {
			return core.Text{Content: "选择左侧文件即可预览", TestID: "files-preview",
				Style: core.Style{FontSize: 12, Color: colors.Muted()}}
		}
		if rows := s.fileTable.Get(); len(rows) > 1 {
			return core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0), Direction: core.Column}, Children: []core.Node{
				s.largeFileNotice(),
				datatable.DataTable{
					Columns: func() []datatable.Column {
						cols := make([]datatable.Column, len(rows[0]))
						for i, name := range rows[0] {
							if name == "" {
								name = "列 " + itoa(i+1)
							}
							cols[i] = datatable.Column{Key: itoa(i), Title: name, Width: 140}
						}
						return cols
					},
					RowCount: func() int { return len(rows) - 1 },
					// RowHeader puts the spreadsheet's row numbers at the left,
					// selecting the row they lead; the numbers read 1..N over the
					// data rows, header excluded.
					RowHeader: true,
					Text: func(row, column int) string {
						if row+1 >= len(rows) || column >= len(rows[row+1]) {
							return ""
						}
						return rows[row+1][column]
					},
					RowHeight: 28,
					Empty:     core.Text{Content: "（空表）"},
					Label:     filepath.Base(s.selectedFile.Get()),
					TestID:    "files-table",
					Style:     core.Style{FlexGrow: 1, MinHeight: core.Pt(0)},
				},
			}}
		}
		if s.fileMode.Get() == "preview" && isMarkdownPath(s.selectedFile.Get()) {
			path := s.selectedFile.Get()
			return core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0), Direction: core.Column, Gap: 4}, Children: []core.Node{
				s.fileModeSwitch(),
				core.Scroll{Style: core.Style{FlexGrow: 1, MinHeight: core.Pt(0)}, Children: []core.Node{
					core.View{Style: core.Style{Padding: core.All(12), MaxWidth: core.Pt(760), AlignSelf: core.AlignCenter, Width: core.Pct(100)},
						Children: []core.Node{markdownView(s.editedFile(path), false)(cx)}},
				}},
			}}
		}
		showDiff := s.diffPatch.Get() != "" && s.fileMode.Get() == "diff"
		var body core.Node
		if showDiff {
			body = core.Scroll{Style: core.Style{FlexGrow: 1, MinHeight: core.Pt(0)}, Children: []core.Node{
				uidiff.View{
					Patch:        s.diffPatch.Get,
					Unified:      s.fileUnified.Get(),
					ContextLines: 3,
					SoftWrap:     s.fileWrap.Get(),
					Selectable:   true,
					FontSize:     11.5,
					Style:        core.Style{FlexGrow: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0)},
				},
			}}
		} else {
			// base/editor is the editor, not just a viewer: edits stay in
			// memory (fileDrafts) until ⌘S writes them, so switching tabs or
			// files never loses a half-finished change.
			path := s.selectedFile.Get()
			body = editor.Editor{
				Value:      func() string { return s.editedFile(path) },
				OnChange:   func(text string) { s.onFileEdit(path, text) },
				Language:   filepath.Base(path),
				ReadOnly:   false,
				NoFolding:  true,
				SoftWrap:   s.fileWrap.Get(),
				FontSize:   13,
				LineHeight: 21,
				Styles: editor.Styles{
					Gutter:           core.Style{Background: colors.Inset()},
					LineNumber:       core.Style{FontSize: 11, Color: colors.Faint()},
					ActiveLineNumber: core.Style{FontSize: 11, Color: colors.Text()},
					Content:          core.Style{FontSize: 13, LineHeight: 21, FontFamily: core.FontMonospace, Color: colors.Text()},
					Caret:            core.Style{Width: core.Pt(1), Background: colors.Accent()},
				},
				Style:  core.Style{FlexGrow: 1, MinHeight: core.Pt(0)},
				TestID: "files-preview",
			}
		}
		children := []core.Node{s.largeFileNotice()}
		if s.diffPatch.Get() != "" || isMarkdownPath(s.selectedFile.Get()) {
			children = append(children, s.fileModeSwitch())
		}
		children = append(children, body)
		return core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0), Gap: 4}, Children: children}
	})
}

// fileModeSwitch flips between the file as it is and the patch git shows.
func (s *appState) fileModeSwitch() core.Node {
	return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6, FlexShrink: 0}, Children: []core.Node{
		ui.Tabs[string]{
			Variant: ui.TabsSegmented,
			Size:    ui.SizeSmall,
			Options: func() []ui.Option[string] {
				options := []ui.Option[string]{{Value: "source", Label: "源码"}}
				if isMarkdownPath(s.selectedFile.Get()) {
					options = append(options, ui.Option[string]{Value: "preview", Label: "预览"})
				}
				options = append(options, ui.Option[string]{Value: "diff", Label: "差异"})
				return options
			}(),
			Value:    s.fileMode.Get,
			OnChange: func(v string) { s.fileMode.Set(v) },
			Label:    "文件视图",
			TestID:   "file-mode",
		},
		ui.IconButton{Icon: lucide.TextWrap, Label: "自动换行", Size: 22, Selected: s.fileWrap.Get, TestID: "file-wrap",
			OnPress: func() { s.fileWrap.Set(!s.fileWrap.GetUntracked()) }},
		core.Show{When: func() bool { return s.fileMode.Get() == "diff" }, Then: func(*core.Scope) core.Node {
			return ui.IconButton{Icon: lucide.Columns2, Label: "分栏差异", Size: 22, Selected: func() bool { return !s.fileUnified.Get() }, TestID: "file-diff-split",
				OnPress: func() { s.fileUnified.Set(!s.fileUnified.GetUntracked()) }}
		}},
	}}
}

// isMarkdownPath reports a file pi-web shows as a rendered preview.
func isMarkdownPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown", ".mdx":
		return true
	}
	return false
}

// zoomTree changes the tree's font size, within a readable range.
func (s *appState) zoomTree(delta int) {
	next := s.treeFont.GetUntracked() + delta
	next = min(max(next, 10), 18)
	s.treeFont.Set(next)
	s.savePrefsAsync(s.projectRoot(), s.showHidden.GetUntracked())
}

// fileIcon picks a glyph by kind, so the tree reads at a glance.
func fileIcon(path string, dir, expanded bool) func() *core.SVG {
	if dir {
		if expanded {
			return lucide.FolderOpen
		}
		return lucide.Folder
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".ico", ".heic", ".svg":
		return lucide.FileImage
	case ".go", ".ts", ".tsx", ".js", ".jsx", ".py", ".rs", ".sh", ".c", ".h", ".cpp", ".java", ".kt", ".rb", ".php", ".swift":
		return lucide.FileCode
	case ".json", ".yaml", ".yml", ".toml", ".xml", ".ini":
		return lucide.FileType
	case ".csv", ".xlsx", ".xls":
		return lucide.FileSpreadsheet
	case ".zip", ".tar", ".gz", ".tgz", ".rar", ".7z", ".dmg", ".pkg":
		return lucide.FileArchive
	}
	return lucide.FileText
}

// parseTableRows reads a CSV/TSV file into rows for base/datatable. The view
// stays bounded: a million-line export must not freeze the window.
func parseTableRows(path, text string) [][]string {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".csv" && ext != ".tsv" && ext != ".tab" {
		return nil
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}
	reader := csv.NewReader(strings.NewReader(text))
	if ext != ".csv" {
		reader.Comma = '\t'
	}
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	var rows [][]string
	for len(rows) < 2000 {
		record, err := reader.Read()
		if err != nil {
			break
		}
		if len(record) > 80 {
			record = record[:80]
		}
		rows = append(rows, record)
	}
	return rows
}

// relativePath is path as the composer's @-mention writes it.
func (s *appState) relativePath(path string) string {
	if root := s.projectRoot(); root != "" {
		if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return path
}

// setProject points the whole window at dir: the tree, terminals, new
// sessions and the mention index. dir must be an existing directory.
func (s *appState) setProject(dir string) {
	if !s.adoptProjectDir(dir) {
		return
	}
	s.selected.Set("")
	s.redux.Reset()
	s.refresh()
	s.status.Set("项目：" + s.projectRoot())
	s.notify(ui.ToastInfo, "已切换项目", s.projectRoot())
	if !s.ensureTrust() {
		return
	}
	s.connect("")
}

// adoptProjectDir points the file tree, terminal and mentions at dir without
// touching the live session. Opening a session calls it from connect(); the
// user's "open project" calls setProject, which also starts a new session.
func (s *appState) adoptProjectDir(dir string) bool {
	dir = filepath.Clean(dir)
	if dir == "" || dir == "." || !isDir(dir) {
		return false
	}
	s.setCwd(dir)
	s.fileDir.Set(dir)
	s.expanded.Set(map[string]bool{dir: true})
	s.selectedFile.Set("")
	s.fileTabs.Set(nil)
	s.diffPatch.Set("")
	s.filePreview.Set("")
	s.fileImage.Set(core.ImageSource{})
	s.gitMarks.Set(map[string]string{})
	s.pendingChange.Set("")
	x := s.extras()
	x.largeNotice.Set("")
	x.grepGen++
	x.grepResults.Set(nil)
	x.grepBusy.Set(false)
	x.stamps = map[string]fileStamp{}
	x.commitSel.Set("")
	x.commitDetail.Set("")
	x.commitBusy.Set(false)
	s.gitSel.Clear()
	s.reloadTree()
	s.loadMentions()
	if x.contentSearch.GetUntracked() {
		if query := strings.TrimSpace(s.fileQuery.GetUntracked()); query != "" {
			s.runGrep(query)
		}
	}
	s.savePrefsAsync(dir, s.showHidden.GetUntracked())
	return true
}

// reloadTree re-reads the project's directories and git state, keeping the
// folders the user has open.
func (s *appState) reloadTree() {
	root := s.projectRoot()
	if root == "" {
		return
	}
	s.dirs.mu.Lock()
	s.dirs.dirs = make(map[string][]filesEntry)
	s.dirs.asked = make(map[string]bool)
	s.dirs.index = make(map[string]filesEntry)
	s.dirs.mu.Unlock()
	s.diffPatch.Set("")
	s.filePreview.Set("")
	s.fileImage.Set(core.ImageSource{})
	s.treeRev.Set(s.treeRev.GetUntracked() + 1)
	s.loadGitState(root)
	if path := s.selectedFile.GetUntracked(); path != "" {
		s.loadDiff(path)
	}
}

func (s *appState) loadGitState(root string) {
	go func() {
		status, _ := files.GitState(root)
		marks := make(map[string]string, len(status.Entries))
		for _, e := range status.Entries {
			path := strings.TrimSpace(e.Path)
			if i := strings.LastIndex(path, " -> "); i >= 0 {
				path = path[i+4:]
			}
			path = strings.TrimSuffix(path, "/")
			if path == "" {
				continue
			}
			code := strings.TrimSpace(e.Status)
			if code == "??" {
				code = "U"
			}
			if code != "" {
				marks[filepath.Join(root, path)] = code[:1]
			}
		}
		text := "不是 Git 仓库"
		if status.Branch != "" {
			text = status.Branch
			if status.IsWorktree {
				text += "（worktree）"
			}
			if status.Clean {
				text += " · 干净"
			} else {
				text += " · " + itoa(len(status.Entries)) + " 处改动"
			}
		}
		s.cx.Post(func() {
			if s.projectRoot() != root {
				return
			}
			s.gitMarks.Set(marks)
			s.gitText.Set(text)
			s.gitStatus.Set(status)
			s.treeRev.Set(s.treeRev.GetUntracked() + 1)
		})
	}()
}

// toggleHidden shows or hides dotfiles, drops the cached listings and keeps
// the choice for the next launch.
func (s *appState) toggleHidden() {
	on := !s.showHidden.GetUntracked()
	s.showHidden.Set(on)
	s.savePrefsAsync(s.projectRoot(), on)
	s.dirs.mu.Lock()
	s.dirs.dirs = make(map[string][]filesEntry)
	s.dirs.asked = make(map[string]bool)
	s.dirs.index = make(map[string]filesEntry)
	s.dirs.mu.Unlock()
	s.treeRev.Set(s.treeRev.GetUntracked() + 1)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// fullPreviewOverlay shows the open file over the whole window: the dock is
// 420 points wide, too narrow to read or edit anything longer than a few
// columns. Escape or the close button returns to it.
func (s *appState) fullPreviewOverlay() core.Node {
	return core.Show{When: func() bool { return s.previewFull.Get() && s.selectedFile.Get() != "" }, Then: func(cx *core.Scope) core.Node {
		cx.OnKeyWith("escape", core.KeyOptions{When: core.KeyAlways}, func() { s.previewFull.Set(false) })
		return core.View{TestID: "file-fullscreen",
			Style: core.Style{Position: core.Absolute, Top: core.Pt(0), Right: core.Pt(0), Bottom: core.Pt(0), Left: core.Pt(0),
				ZIndex: 30, Direction: core.Column, Background: s.colors.Background()},
			Children: []core.Node{
				core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8,
					Padding: core.Symmetric(6, 10), BorderWidth: 1}, StyleFunc: func() core.Style {
					return core.Style{BorderColor: s.colors.Border()}
				}, Children: []core.Node{
					ui.Icon(lucide.FileText, 14, s.colors.Muted),
					core.Text{Content: filepath.Base(s.selectedFile.Get()), MaxLines: 1,
						Style: core.Style{FontSize: 12.5, FontWeight: core.FontWeightSemibold, Color: s.colors.Text()}},
					core.Text{ContentFunc: func() string { return s.relativePath(s.selectedFile.Get()) }, MaxLines: 1,
						Style: core.Style{FontSize: 10.5, Color: s.colors.Faint()}},
					core.View{Style: core.Style{FlexGrow: 1}},
					ui.IconButton{Icon: lucide.X, Label: "关闭全屏预览", TestID: "file-fullscreen-close", Size: 24,
						OnPress: func() { s.previewFull.Set(false) }},
				}},
				s.fileTabsBar(),
				core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0), Padding: core.All(10)},
					Children: []core.Node{s.filePreviewPane()}},
			}}
	}}
}
