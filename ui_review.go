package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/oneo-me/nsgo/base"
	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/icons/lucide"
	"github.com/oneo-me/nsgo/ui"
	"github.com/oneo-me/nsgo/ui/commitgraph"
	uidiff "github.com/oneo-me/nsgo/ui/diff"

	"piwebnative/internal/files"
	"piwebnative/internal/reducer"
	"piwebnative/internal/textdiff"
)

// reviewItem is a flat row of the review tab: a section heading or one
// changed file.
type reviewItem struct {
	Header bool
	Title  string // the section's title
	Rel    string
	Code   string
	Staged bool
}

// reviewView is the dock's Review tab: the changed files with their diff,
// staging and one commit box, plus the commit history one toggle away. It
// replaces a tab that only ever showed the graph.
func (s *appState) reviewView() core.Node {
	return core.View{
		Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0), Direction: core.Column},
		Children: []core.Node{
			s.reviewHeader(),
			core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0)}, Children: []core.Node{
				core.Dynamic(func(*core.Scope) core.Node {
					if s.reviewMode.Get() == "history" {
						return s.reviewHistoryView()
					}
					return s.reviewChanges()
				}),
			}},
		},
	}
}

// reviewHistoryView is the history half of the review tab: the commit graph
// over the selected commit's `git show`, instead of activating a row to copy
// its hash and hide the rest.
func (s *appState) reviewHistoryView() core.Node {
	return core.View{
		Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0)},
		Children: []core.Node{
			core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8,
				Padding: core.Edges{Top: 6, Right: 10, Bottom: 4, Left: 10}}, Children: []core.Node{
				core.Text{ContentFunc: s.gitHeadLabel, TestID: "git-head", MaxLines: 1,
					Style: core.Style{FontSize: 11, FontFamily: core.FontMonospace, Color: s.colors.Muted()}},
				core.View{Style: core.Style{FlexGrow: 1}},
				core.Text{ContentFunc: s.gitCountLabel, MaxLines: 1,
					Style: core.Style{FontSize: fontMeta, Color: s.colors.Muted()}},
				ui.IconButton{Icon: lucide.RefreshCw, Label: "刷新提交历史", TestID: "review-history-refresh", Size: 24,
					OnPress: s.loadGit},
			}},
			commitSelectionWatcher{s: s},
			ui.ResizablePanelGroup{
				Axis:        base.Vertical,
				HandleLabel: "调整提交详情高度",
				HandleStyleFunc: func() core.Style {
					return core.Style{Background: s.colors.Border()}
				},
				Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0)},
				Panels: []base.ResizablePanel{
					{Size: func() core.Points { return core.Points(260) }, MinSize: 120, MaxSize: 520,
						Children: []core.Node{
							commitgraph.View{
								Commits:    s.gitCommits.Get,
								Selection:  s.gitSel,
								OnActivate: s.selectCommit,
								Empty: core.Text{Content: "这个项目还没有提交", TestID: "git-empty",
									Style: core.Style{FontSize: fontLabel, Color: s.colors.Muted()}},
								Label:  "Git 历史",
								TestID: "git-graph",
								Style:  core.Style{FlexGrow: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0)},
							},
						}},
					{MinSize: 100, Children: []core.Node{s.commitDetailPane()}},
				},
			},
		},
	}
}

// commitSelectionWatcher follows the graph's selection: a single click
// selects a row without activating it, and the detail pane should follow
// that click. Activating the row (Enter or double click) is handled too; the
// loader ignores a hash it already shows.
type commitSelectionWatcher struct{ s *appState }

// Render implements core.Node.
func (w commitSelectionWatcher) Render(cx *core.Scope) core.Node {
	s := w.s
	cx.Effect(func() {
		keys := s.gitSel.Keys()
		if len(keys) == 0 {
			return
		}
		if hash, _ := keys[0].(string); hash != "" {
			s.loadCommitDetail(hash)
		}
	})
	return core.View{TestID: "git-commit-watch", Style: core.Style{Width: core.Pt(0), Height: core.Pt(0)}}
}

// selectCommit loads the chosen commit's details off the UI thread.
func (s *appState) selectCommit(c commitgraph.Commit) {
	if c.Hash == "" {
		return
	}
	s.gitSel.Set(c.Hash)
	s.loadCommitDetail(c.Hash)
}

// loadCommitDetail fetches `git show` for hash. The answer is dropped when
// another row replaced it.
func (s *appState) loadCommitDetail(hash string) {
	hash = strings.TrimSpace(hash)
	if hash == "" {
		return
	}
	x := s.extras()
	if x.commitSel.GetUntracked() == hash && (x.commitBusy.GetUntracked() || x.commitDetail.GetUntracked() != "") {
		return
	}
	x.commitSel.Set(hash)
	x.commitDetail.Set("")
	x.commitBusy.Set(true)
	root := s.projectRoot()
	go func() {
		text, err := files.GitShowCommit(root, hash)
		if len(text) > 1<<20 {
			text = text[:1<<20] + "\n…（输出过大，只显示前 1MB）"
		}
		s.cx.Post(func() {
			if x.commitSel.GetUntracked() != hash {
				return
			}
			x.commitBusy.Set(false)
			if err != nil {
				x.commitDetail.Set("读取失败：" + err.Error())
				return
			}
			x.commitDetail.Set(text)
		})
	}()
}

// commitDetailPane shows `git show` in a monospace, selectable scroll: the
// commit message, its stat and its patch.
func (s *appState) commitDetailPane() core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		colors := ui.ColorsOf(cx)
		x := s.extras()
		hash := x.commitSel.Get()
		detail := x.commitDetail.Get()
		busy := x.commitBusy.Get()
		if hash == "" {
			return core.View{TestID: "git-commit-empty", Style: core.Style{FlexGrow: 1, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Padding: core.All(14)},
				Children: []core.Node{core.Text{Content: "点击上方提交查看详情", TestID: "git-commit-hint",
					Style: core.Style{FontSize: fontLabel, Color: colors.Muted()}}}}
		}
		if strings.TrimSpace(detail) == "" {
			content := "没有可显示的内容"
			if busy {
				content = "读取提交…"
			}
			return core.View{TestID: "git-commit-empty", Style: core.Style{FlexGrow: 1, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Padding: core.All(14)},
				Children: []core.Node{core.Text{Content: content, TestID: "git-commit-loading",
					Style: core.Style{FontSize: fontLabel, Color: colors.Muted()}}}}
		}
		short := hash
		if len(short) > 7 {
			short = short[:7]
		}
		return core.View{TestID: "git-commit-detail",
			Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0), Direction: core.Column},
			Children: []core.Node{
				core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6, FlexShrink: 0,
					Padding: core.Edges{Top: 4, Right: 8, Bottom: 4, Left: 10}}, Children: []core.Node{
					core.Text{Content: "提交 " + short, MaxLines: 1,
						Style: core.Style{FontSize: 11, FontFamily: core.FontMonospace, FontWeight: core.FontWeightSemibold, Color: colors.Text()}},
					core.View{Style: core.Style{FlexGrow: 1}},
					ui.IconButton{Icon: lucide.Copy, Label: "复制提交哈希", TestID: "git-commit-copy", Size: 22,
						OnPress: func() {
							s.cx.SetClipboard(hash)
							s.notify(ui.ToastInfo, "已复制提交 "+short, "")
						}},
				}},
				core.Scroll{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0),
					Padding: core.Edges{Top: 2, Right: 8, Bottom: 6, Left: 10}}, Children: []core.Node{
					core.View{Selectable: true, Children: []core.Node{
						core.Text{Content: detail, Style: core.Style{FontSize: 11.5, LineHeight: 17,
							FontFamily: core.FontMonospace, Color: colors.Text()}},
					}},
				}},
			}}
	})
}

// reviewScopeOptions are the 变更 list's two scopes: what this conversation
// wrote, and the whole working tree.
var reviewScopeOptions = []ui.Option[string]{
	{Value: "session", Label: "本会话", TestID: "review-scope-session"},
	{Value: "workspace", Label: "工作区", TestID: "review-scope-workspace"},
}

// reviewHeader switches between changes and history and carries the
// repository state.
func (s *appState) reviewHeader() core.Node {
	return core.View{
		Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6,
			Padding: core.Edges{Top: 6, Right: 8, Bottom: 4, Left: 8}},
		Children: []core.Node{
			ui.Tabs[string]{
				Variant: ui.TabsSegmented, Size: ui.SizeSmall,
				Options: []ui.Option[string]{
					{Value: "changes", Label: "变更", TestID: "review-tab-changes"},
					{Value: "history", Label: "历史", TestID: "review-tab-history"},
				},
				Value: s.reviewMode.Get,
				OnChange: func(v string) {
					s.reviewMode.Set(v)
					if v == "history" {
						s.loadGit()
					}
				},
				Label:  "审阅视图",
				TestID: "review-view",
			},
			core.Show{When: func() bool { return s.reviewMode.Get() == "changes" }, Then: func(*core.Scope) core.Node {
				return ui.Tabs[string]{
					Variant: ui.TabsSegmented, Size: ui.SizeSmall,
					Options: reviewScopeOptions,
					Value:   s.reviewScope.Get,
					OnChange: func(v string) {
						if v == s.reviewScope.GetUntracked() {
							return
						}
						s.reviewScope.Set(v)
						s.reviewSel.Clear()
						s.reviewDiff.Set("")
					},
					Label:  "变更范围",
					TestID: "review-scope",
				}
			}},
			core.Text{ContentFunc: s.gitText.Get, TestID: "review-branch", MaxLines: 1,
				Style: core.Style{FontSize: 10.5, FontFamily: core.FontMonospace, Color: s.colors.Muted()}},
			core.View{Style: core.Style{FlexGrow: 1}},
			core.Show{When: func() bool { return s.reviewMode.Get() == "changes" }, Then: func(*core.Scope) core.Node {
				return ui.IconButton{Icon: lucide.RefreshCw, Label: "刷新变更", TestID: "review-refresh", Size: 24,
					OnPress: func() { s.loadReviewStatus() }}
			}},
			ui.IconButton{Icon: lucide.GitBranch, Label: "工作树", TestID: "git-worktrees", Size: 24,
				OnPress: s.openWorktreeDialog},
		},
	}
}

// sessionReviewFiles is the file list this conversation wrote, in first-touch
// order. It reads the reducer's tool calls rather than the visible turns,
// so a transcript paged to its last twenty turns still knows every file the
// session touched. Paths the tools named are kept as the session wrote them;
// the list resolves them against the project when it draws.
func (s *appState) sessionReviewFiles() []string {
	return sessionReviewFilesFrom(s.redux.Snapshot(), s.projectRoot())
}

// sessionReviewFilesFrom is sessionReviewFiles over a snapshot, so the rule
// can be tested without a live reducer.
func sessionReviewFilesFrom(snap reducer.Snapshot, root string) []string {
	seen := make(map[string]bool, len(snap.Tools))
	out := make([]string, 0, 8)
	for _, tc := range snap.Tools {
		path := writtenPathArg(tc.Name, tc.Args)
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		path = filepath.Clean(path)
		if seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	return out
}

// writtenPathArg is the file a write-like tool call named, empty for a call
// that writes nothing (read, bash, grep) or one whose arguments do not carry
// a path (a bare patch body).
func writtenPathArg(name string, args map[string]any) string {
	lower := strings.ToLower(name)
	if !strings.Contains(lower, "edit") && !strings.Contains(lower, "write") && !strings.Contains(lower, "apply_patch") {
		return ""
	}
	path, _ := args["path"].(string)
	if path == "" {
		path, _ = args["file_path"].(string)
	}
	return strings.TrimSpace(path)
}

// relativeReviewPath is the review list's name for a written file: relative
// to the project when it lives inside it, absolute otherwise.
func (s *appState) relativeReviewPath(path string) string {
	root := s.projectRoot()
	if root == "" {
		return path
	}
	if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return path
}

// reviewAbsPath resolves a list row back to the file on disk: a relative
// row joins the project, an absolute one is already the path.
func (s *appState) reviewAbsPath(rel string) string {
	if filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(s.projectRoot(), rel)
}

// gitCodeMap turns the working-tree status into the letter each row shows,
// keyed by the review list's relative path.
func gitCodeMap(status files.GitStatus) map[string]string {
	out := make(map[string]string, len(status.Entries))
	for _, e := range status.Entries {
		key := reviewStatKey(e.Path)
		if key == "" {
			continue
		}
		code := strings.TrimSpace(e.Status)
		if code == "??" {
			code = "U"
		}
		if code != "" {
			out[key] = code
		}
	}
	return out
}

// reviewRowsForScope is the changes list of the current scope: this
// session's written files, or the working tree's git status.
func (s *appState) reviewRowsForScope() []reviewItem {
	if s.reviewScope.Get() == "workspace" {
		return reviewRows(s.gitStatus.Get())
	}
	files := s.sessionReviewFiles()
	if len(files) == 0 {
		return nil
	}
	codes := gitCodeMap(s.gitStatus.Get())
	rows := make([]reviewItem, 0, len(files)+1)
	rows = append(rows, reviewItem{Header: true, Title: fmt.Sprintf("本次会话 · %d 个文件", len(files))})
	for _, path := range files {
		rel := s.relativeReviewPath(path)
		rows = append(rows, reviewItem{Rel: rel, Code: codes[rel]})
	}
	return rows
}

// reviewRows flattens the status into section headings and files: the
// working copy first, then the index.
func reviewRows(status files.GitStatus) []reviewItem {
	var unstaged, staged []files.GitEntry
	for _, e := range status.Entries {
		code := e.Status
		for len(code) < 2 {
			code += " "
		}
		x, y := code[0], code[1]
		if x == '?' && y == '?' {
			unstaged = append(unstaged, e)
			continue
		}
		if y != ' ' {
			unstaged = append(unstaged, e)
		}
		if x != ' ' {
			staged = append(staged, e)
		}
	}
	var rows []reviewItem
	if len(unstaged) > 0 {
		rows = append(rows, reviewItem{Header: true, Title: fmt.Sprintf("未暂存 · %d", len(unstaged))})
		for _, e := range unstaged {
			rows = append(rows, reviewItem{Rel: e.Path, Code: strings.TrimSpace(e.Status)})
		}
	}
	if len(staged) > 0 {
		rows = append(rows, reviewItem{Header: true, Title: fmt.Sprintf("已暂存 · %d", len(staged))})
		for _, e := range staged {
			rows = append(rows, reviewItem{Rel: e.Path, Code: strings.TrimSpace(e.Status), Staged: true})
		}
	}
	return rows
}

// reviewStatKey turns a `git status` path into the key the list rows and the
// stats map share: a rename keeps its destination, a directory loses the
// trailing slash.
func reviewStatKey(path string) string {
	if i := strings.LastIndex(path, " -> "); i >= 0 {
		path = path[i+4:]
	}
	return strings.TrimSuffix(strings.TrimSpace(path), "/")
}

// countPatchStats counts the added and removed lines of a unified diff. Only
// hunk bodies count: the `+++`/`---` file headers sit before the first `@@`,
// which also keeps a content line that begins with "++" from being mistaken
// for a header.
func countPatchStats(patch string) (add, del int) {
	inHunk := false
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "@@") {
			inHunk = true
			continue
		}
		if strings.HasPrefix(line, "diff ") {
			inHunk = false
			continue
		}
		if !inHunk {
			continue
		}
		switch {
		case strings.HasPrefix(line, "+"):
			add++
		case strings.HasPrefix(line, "-"):
			del++
		}
	}
	return add, del
}

// countLines is the number of text lines, counting a body without a trailing
// newline as one more line and an empty body as none.
func countLines(body string) int {
	if body == "" {
		return 0
	}
	n := strings.Count(body, "\n")
	if !strings.HasSuffix(body, "\n") {
		n++
	}
	return n
}

// reviewFileStats asks git for one path's patch and counts it. A file with
// neither a working copy nor a staged patch but a body on disk is untracked:
// everything in it is an addition.
func reviewFileStats(root, rel string) reviewStat {
	patch, err := files.GitDiff(root, rel)
	if err != nil || strings.TrimSpace(patch) == "" {
		patch = ""
		if staged, serr := files.GitDiffStaged(root, rel); serr == nil {
			patch = staged
		}
	}
	if strings.TrimSpace(patch) != "" {
		add, del := countPatchStats(patch)
		return reviewStat{Add: add, Del: del, Ready: true}
	}
	body, err := files.ReadText(filepath.Join(root, rel), 512<<10)
	if err != nil {
		return reviewStat{Ready: true}
	}
	return reviewStat{Add: countLines(body), Untracked: true, Ready: true}
}

// prefetchReviewStats fills the list's +N −M counts, four git diffs at a time.
// It runs from a render effect and never blocks: the work happens in
// goroutines, each answer is posted back to the UI thread, and an answer is
// dropped when another project replaced the one it was asked for.
func (s *appState) prefetchReviewStats(root string, rels []string) {
	if root == "" {
		return
	}
	if root != s.reviewStatsRoot {
		s.reviewStatsRoot = root
		clear(s.reviewStatsPending)
		s.reviewStats.Set(map[string]reviewStat{})
	}
	if len(rels) == 0 {
		return
	}
	if s.reviewStatsPending == nil {
		s.reviewStatsPending = map[string]bool{}
	}
	ready := s.reviewStats.GetUntracked()
	var todo []string
	for _, rel := range rels {
		if rel == "" || s.reviewStatsPending[rel] {
			continue
		}
		if st, ok := ready[rel]; ok && st.Ready {
			continue
		}
		s.reviewStatsPending[rel] = true
		todo = append(todo, rel)
	}
	if len(todo) == 0 {
		return
	}
	work := make(chan string, len(todo))
	for _, rel := range todo {
		work <- rel
	}
	close(work)
	workers := 4
	if len(todo) < workers {
		workers = len(todo)
	}
	for i := 0; i < workers; i++ {
		go func() {
			for rel := range work {
				st := reviewFileStats(root, rel)
				s.cx.Post(func() {
					delete(s.reviewStatsPending, rel)
					if s.reviewStatsRoot != root {
						return
					}
					next := make(map[string]reviewStat, len(s.reviewStats.GetUntracked())+1)
					for k, v := range s.reviewStats.GetUntracked() {
						next[k] = v
					}
					next[rel] = st
					s.reviewStats.Set(next)
				})
			}
		}()
	}
}

// reviewStatBadge draws the counts beside a path: a new file shows only its
// additions, a changed one its additions and removals.
func (s *appState) reviewStatBadge(cx *core.Scope, rel string, st reviewStat) core.Node {
	colors := ui.ColorsOf(cx)
	testID := "review-stat-" + strings.ReplaceAll(rel, "/", "-")
	if st.Untracked {
		return core.Text{Content: fmt.Sprintf("+%d", st.Add), MaxLines: 1, TestID: testID,
			Style: core.Style{FontSize: 10, FontFamily: core.FontMonospace, Color: colors.Up()}}
	}
	return core.View{TestID: testID, Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 4},
		Children: []core.Node{
			core.Text{Content: fmt.Sprintf("+%d", st.Add), MaxLines: 1,
				Style: core.Style{FontSize: 10, FontFamily: core.FontMonospace, Color: colors.Up()}},
			core.Text{Content: fmt.Sprintf("−%d", st.Del), MaxLines: 1,
				Style: core.Style{FontSize: 10, FontFamily: core.FontMonospace, Color: colors.Danger()}},
		}}
}

// reviewAutoSelect picks the first changed file when nothing is selected, so
// the diff pane shows a patch as soon as the status arrives instead of asking
// the user to click. It stays quiet once there is a selection, so a click —
// or an empty list — wins. The same effect kicks off the line-count prefetch
// the rows need, which hands its work to goroutines.
type reviewAutoSelect struct{ s *appState }

// Render implements core.Node.
func (w reviewAutoSelect) Render(cx *core.Scope) core.Node {
	s := w.s
	cx.Effect(func() {
		status := s.gitStatus.Get()
		var rels []string
		if s.reviewScope.Get() == "session" {
			for _, row := range s.reviewRowsForScope() {
				if !row.Header && row.Rel != "" {
					rels = append(rels, row.Rel)
				}
			}
		} else {
			for _, e := range status.Entries {
				if rel := reviewStatKey(e.Path); rel != "" {
					rels = append(rels, rel)
				}
			}
		}
		s.prefetchReviewStats(s.projectRoot(), rels)
		// A cleared selection with a diff still on screen is the user pressing
		// Esc; re-picking the first row would make that Esc do nothing.
		if len(s.reviewSel.Keys()) > 0 || s.reviewDiff.GetUntracked() != "" {
			return
		}
		for _, row := range s.reviewRowsForScope() {
			if row.Header {
				continue
			}
			if row.Rel != "" {
				s.selectReviewFile(row.Rel)
			}
			return
		}
	})
	return core.View{TestID: "review-auto-select-watch", Style: core.Style{Width: core.Pt(0), Height: core.Pt(0)}}
}

// reviewChanges is the changes list over the selected file's diff, with the
// commit box under the list.
func (s *appState) reviewChanges() core.Node {
	rows := s.reviewRowsForScope()
	return core.View{
		Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0)},
		Children: []core.Node{reviewAutoSelect{s: s}, ui.ResizablePanelGroup{
			Axis:        base.Vertical,
			HandleLabel: "调整变更列表高度",
			HandleStyleFunc: func() core.Style {
				return core.Style{Background: s.colors.Border()}
			},
			Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0)},
			Panels: []base.ResizablePanel{
				{Size: func() core.Points { return core.Points(220) }, MinSize: 120, MaxSize: 460,
					Children: []core.Node{s.reviewList(rows), s.reviewCommitBox(rows)}},
				{MinSize: 120, Children: []core.Node{
					core.View{Style: core.Style{FlexGrow: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0)}, Children: []core.Node{s.reviewDiffPane()}},
				}},
			},
		}},
	}
}

// reviewList is the virtualized change list.
func (s *appState) reviewList(rows []reviewItem) core.Node {
	return ui.List{
		Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0)},
		Count: func() int { return len(rows) },
		Key:   func(i int) any { return fmt.Sprintf("%d:%s", i, rows[i].Rel) },
		Row: func(cx *core.Scope, r core.ListRow) core.Node {
			return core.Dynamic(func(cx *core.Scope) core.Node {
				i := r.Index()
				if i < 0 || i >= len(rows) {
					return nil
				}
				return s.reviewRow(cx, rows[i])
			})
		},
		Selection: s.reviewSel,
		OnActivate: func(i int) {
			if i >= 0 && i < len(rows) && !rows[i].Header {
				s.selectReviewFile(rows[i].Rel)
			}
		},
		RowLabel: func(i int) string {
			if i >= 0 && i < len(rows) {
				return rows[i].Title + rows[i].Rel
			}
			return ""
		},
		EstimatedSize: 34,
		Gap:           1,
		Label:         "变更文件",
		Empty: s.reviewEmptyState(),
		TestID: "review-list",
	}
}

// reviewEmptyState says why the list is empty, which depends on the scope: a
// session that wrote nothing is normal, a dirty tree is not.
func (s *appState) reviewEmptyState() core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		colors := ui.ColorsOf(cx)
		text := "工作区干净，没有待审阅的改动"
		tail := ""
		if s.reviewScope.Get() == "session" {
			text = "本次会话没有改动文件"
			tail = "AI 写到文件后会自动出现在这里"
		}
		return core.View{TestID: "review-empty", Style: core.Style{Padding: core.All(14), Gap: 4},
			Children: []core.Node{
				core.Text{Content: text, MaxLines: 2, Style: core.Style{FontSize: fontLabel, Color: colors.Muted()}},
				core.Show{When: func() bool { return tail != "" }, Then: func(*core.Scope) core.Node {
					return core.Text{Content: tail, MaxLines: 2,
						Style: core.Style{FontSize: fontMeta, Color: colors.Faint()}}
				}},
			}}
	})
}

// reviewRow draws a heading or a file with its git letter.
func (s *appState) reviewRow(cx *core.Scope, item reviewItem) core.Node {
	colors := ui.ColorsOf(cx)
	if item.Header {
		return core.Text{Content: item.Title, TestID: "review-section", MaxLines: 1,
			Style: core.Style{FontSize: fontMeta, FontWeight: core.FontWeightSemibold, Color: colors.Faint(),
				Padding: core.Edges{Top: 8, Right: 6, Bottom: 2, Left: 6}}}
	}
	code := item.Code
	color := colors.Warn()
	switch {
	case strings.Contains(code, "A"), strings.Contains(code, "?"):
		color = colors.Up()
	case strings.Contains(code, "D"):
		color = colors.Danger()
	case strings.Contains(code, "R"):
		color = colors.Accent()
	}
	rel := reviewStatKey(item.Rel)
	children := []core.Node{
		core.Text{Content: code, Style: core.Style{FontSize: 10, FontFamily: core.FontMonospace, FontWeight: core.FontWeightSemibold, Color: color}},
		core.Text{Content: item.Rel, MaxLines: 1, Style: core.Style{MinWidth: core.Pt(0), FontSize: fontLabel, Color: colors.Text()}},
	}
	// The counts arrive from the background prefetch; a row without them yet
	// keeps its letter and path only, so nothing waits on git.
	if st, ok := s.reviewStats.Get()[rel]; ok && st.Ready {
		children = append(children, core.View{Style: core.Style{FlexGrow: 1}}, s.reviewStatBadge(cx, rel, st))
	}
	return core.View{Role: core.RoleButton, Label: item.Rel,
		OnPress: func(core.PressEvent) { s.selectReviewFile(item.Rel) },
		Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter,
			Gap: 6, Padding: core.Symmetric(4, 6), CornerRadius: radiusInline},
		StyleFunc: func() core.Style {
			return core.Style{Transition: core.Transition{Duration: 120000000, Properties: core.TransitionBackground},
				Hover: &core.Style{Background: ui.Alpha(colors.Text(), 0.05)}, Pressed: &core.Style{Background: ui.Alpha(colors.Text(), 0.09)}}
		}, Children: children}
}

// reviewCommitBox is the commit box under the list. A session-scoped list has
// no staged half to speak of, so the box belongs to the working tree only.
func (s *appState) reviewCommitBox(rows []reviewItem) core.Node {
	return core.Dynamic(func(*core.Scope) core.Node {
		if s.reviewScope.Get() != "workspace" {
			return nil
		}
		return s.commitBox(rows)
	})
}

// commitBox stages the selected file, unstages it, or commits the index.
func (s *appState) commitBox(rows []reviewItem) core.Node {
	staged := 0
	for _, r := range rows {
		if r.Staged {
			staged++
		}
	}
	return core.View{Style: core.Style{Gap: 4, Padding: core.All(6), BorderWidth: 1}, StyleFunc: func() core.Style {
		return core.Style{BorderColor: s.colors.Border()}
	}, Children: []core.Node{
		core.View{Style: core.Style{Direction: core.Row, Gap: 4}, Children: []core.Node{
			ui.Button{Text: "暂存选中", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "review-stage",
				OnPress: s.stageSelected},
			ui.Button{Text: "取消暂存", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "review-unstage",
				OnPress: s.unstageSelected},
			ui.Button{Text: "全部暂存", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "review-stage-all",
				OnPress: s.stageAll},
		}},
		ui.Input{Value: s.reviewMessage.Get, OnChange: s.reviewMessage.Set, Multiline: true,
			Placeholder: "提交说明", TestID: "review-message",
			Style: core.Style{Width: core.Pct(100), MinHeight: core.Pt(48), FontSize: fontLabel}},
		core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6}, Children: []core.Node{
			core.Text{Content: fmt.Sprintf("%d 个文件已暂存", staged), MaxLines: 1,
				Style: core.Style{FontSize: fontMeta, Color: s.colors.Muted()}},
			core.View{Style: core.Style{FlexGrow: 1}},
			ui.Button{Text: "提交", Variant: ui.ButtonPrimary, Size: ui.SizeSmall, TestID: "review-commit",
				Disabled: func() bool {
					return s.reviewBusy.Get() || staged == 0 || strings.TrimSpace(s.reviewMessage.Get()) == ""
				},
				OnPress: s.commitReview},
		}},
	}}
}

// reviewDiffPane renders the selected file's patch under a bar that names the
// file and offers to open it in the file panel or reload its diff.
func (s *appState) reviewDiffPane() core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		colors := ui.ColorsOf(cx)
		patch := s.reviewDiff.Get()
		if strings.TrimSpace(patch) == "" {
			return core.View{Style: core.Style{FlexGrow: 1, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Padding: core.All(16)},
				Children: []core.Node{core.Text{Content: "选择左侧文件查看改动", TestID: "review-diff-empty",
					Style: core.Style{FontSize: fontLabel, TextAlign: core.TextAlignCenter, Color: colors.Muted()}}}}
		}
		rel := s.selectedReviewPath()
		bar := []core.Node{
			core.Text{Content: rel, TestID: "review-diff-path", MaxLines: 1,
				Style: core.Style{FlexGrow: 1, MinWidth: core.Pt(0), FontSize: 11, FontFamily: core.FontMonospace, Color: colors.Muted()}},
		}
		if st, ok := s.reviewStats.Get()[reviewStatKey(rel)]; ok && st.Ready {
			bar = append(bar, s.reviewStatBadge(cx, reviewStatKey(rel), st))
		}
		bar = append(bar,
			ui.Button{Text: "在文件面板打开", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "review-open-file",
				OnPress: func() {
					if rel == "" {
						return
					}
					// openFile puts the file in a tab of its own and shows it;
					// switching to the tree afterwards would hide what the
					// press asked for.
					s.openFile(s.reviewAbsPath(rel))
				}},
			ui.Button{Text: "刷新", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "review-diff-refresh",
				OnPress: func() {
					if rel == "" {
						return
					}
					s.selectReviewFile(rel)
				}},
		)
		return core.View{TestID: "review-diff-pane",
			Style: core.Style{Direction: core.Column, FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0)},
			Children: []core.Node{
				core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6, FlexShrink: 0,
					Padding: core.Edges{Top: 4, Right: 8, Bottom: 4, Left: 10}, Borders: core.Edges{Bottom: 1}},
					StyleFunc: func() core.Style { return core.Style{BorderColor: colors.Border()} },
					Children:  bar},
				core.Scroll{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0)}, Children: []core.Node{
					uidiff.View{Patch: func() string { return patch }, Unified: true, ContextLines: 3, SoftWrap: true,
						Selectable: true, FontSize: 11.5, TestID: "review-diff",
						Style: core.Style{FlexGrow: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0)}},
				}},
			}}
	})
}

// loadReviewStatus re-reads git status and marks for the review list. Asking
// for a refresh also drops the cached line counts, so an edited file gets new
// numbers instead of the ones the first prefetch saw.
func (s *appState) loadReviewStatus() {
	root := s.projectRoot()
	s.reviewStatsRoot = root
	clear(s.reviewStatsPending)
	s.reviewStats.Set(map[string]reviewStat{})
	s.loadGitState(root)
}

// selectReviewFile shows one path's patch: the working copy first, then the
// index, and a new file as an all-added patch.
func (s *appState) selectReviewFile(rel string) {
	s.reviewSel.Set(rel)
	s.reviewDiff.Set("")
	root := s.projectRoot()
	target := s.reviewAbsPath(rel)
	go func() {
		patch, _ := files.GitDiff(root, rel)
		if strings.TrimSpace(patch) == "" {
			patch, _ = files.GitDiffStaged(root, rel)
		}
		if strings.TrimSpace(patch) == "" {
			if body, err := files.ReadText(target, 512<<10); err == nil {
				patch = textdiff.Unified(rel, "", body, 3)
			}
		}
		s.cx.Post(func() {
			if s.selectedReviewPath() == rel {
				s.reviewDiff.Set(patch)
			}
		})
	}()
}

// selectedReviewPath is the highlighted change, empty for none.
func (s *appState) selectedReviewPath() string {
	keys := s.reviewSel.Keys()
	if len(keys) == 0 {
		return ""
	}
	rel, _ := keys[0].(string)
	return rel
}

// stageSelected adds the highlighted file to the index.
func (s *appState) stageSelected() {
	s.reviewGitAction("已暂存", func(root, rel string) error { return files.GitStage(root, rel) })
}

// unstageSelected removes the highlighted file from the index.
func (s *appState) unstageSelected() {
	s.reviewGitAction("已取消暂存", func(root, rel string) error { return files.GitUnstage(root, rel) })
}

// stageAll adds every change.
func (s *appState) stageAll() {
	s.reviewGitAction("已暂存全部", func(root, _ string) error { return files.GitStage(root, ".") })
}

func (s *appState) reviewGitAction(done string, action func(root, rel string) error) {
	rel := s.selectedReviewPath()
	root := s.projectRoot()
	if root == "" {
		return
	}
	s.reviewBusy.Set(true)
	go func() {
		err := action(root, rel)
		s.cx.Post(func() {
			s.reviewBusy.Set(false)
			if err != nil {
				s.notify(ui.ToastWarning, "Git 操作失败", err.Error())
				return
			}
			s.notify(ui.ToastInfo, done, rel)
			s.loadGitState(root)
			if rel != "" {
				s.selectReviewFile(rel)
			}
		})
	}()
}

// commitReview commits the index and clears the box.
func (s *appState) commitReview() {
	root := s.projectRoot()
	msg := strings.TrimSpace(s.reviewMessage.Get())
	if root == "" || msg == "" {
		return
	}
	s.reviewBusy.Set(true)
	go func() {
		summary, err := files.GitCommit(root, msg)
		s.cx.Post(func() {
			s.reviewBusy.Set(false)
			if err != nil {
				s.notify(ui.ToastWarning, "提交失败", firstLine(summary+" "+err.Error(), 120))
				return
			}
			s.reviewMessage.Set("")
			s.reviewDiff.Set("")
			s.notify(ui.ToastSuccess, "已提交", firstLine(summary, 80))
			s.loadGitState(root)
			s.loadGit()
			s.refresh()
		})
	}()
}

// openSubagent opens a child session in the transcript; the dock keeps its
// list, so going back is one tab press.
func (s *appState) openSubagent(id string) {
	s.subOpen.Set(false)
	s.openSession(id)
	s.readOnly.Set(true)
}

// subagentsPanel is the dock's Subagents tab: the child sessions of the
// running agent, opened like a session from the sidebar.
func (s *appState) subagentsPanel() core.Node {
	return core.Dynamic(func(cx *core.Scope) core.Node {
		rows := s.subItems.Get()
		colors := ui.ColorsOf(cx)
		header := core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6,
			Padding: core.Edges{Top: 6, Right: 8, Bottom: 4, Left: 10}}, Children: []core.Node{
			core.Text{Content: "子代理会话", MaxLines: 1,
				Style: core.Style{FontSize: fontMeta, FontWeight: core.FontWeightSemibold, Color: colors.Muted()}},
			core.Text{Content: fmt.Sprintf("%d", len(rows)), MaxLines: 1,
				Style: core.Style{FontSize: fontMeta, Color: colors.Faint()}},
			core.View{Style: core.Style{FlexGrow: 1}},
			ui.IconButton{Icon: lucide.RefreshCw, Label: "刷新子代理", TestID: "subagents-refresh", Size: 24,
				OnPress: s.loadSubagents},
		}}
		if len(rows) == 0 {
			return core.View{Style: core.Style{FlexGrow: 1}, Children: []core.Node{header,
				core.View{Style: core.Style{FlexGrow: 1, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Padding: core.All(16)},
					Children: []core.Node{core.Text{Content: "没有子代理会话", TestID: "subagents-empty",
						Style: core.Style{FontSize: fontLabel, Color: s.colors.Muted()}}}}}}
		}
		nodes := make([]core.Node, 0, len(rows))
		for _, r := range rows {
			row := r
			meta := row.Project
			if row.Age != "" {
				meta += " · " + row.Age
			}
			nodes = append(nodes, core.View{Role: core.RoleButton, Label: row.Title, TestID: "subagent-item",
				OnPress: func(core.PressEvent) { s.openSubagent(row.ID) },
				Style:   core.Style{Gap: 2, Padding: core.Symmetric(6, 8), CornerRadius: radiusControl},
				StyleFunc: func() core.Style {
					return core.Style{Hover: &core.Style{Background: ui.Alpha(colors.Text(), 0.05)},
						Pressed: &core.Style{Background: ui.Alpha(colors.Text(), 0.09)}}
				},
				Children: []core.Node{
					core.Text{Content: row.Title, MaxLines: 1, Style: core.Style{FontSize: fontLabel, Color: colors.Text()}},
					core.Text{Content: meta, MaxLines: 1, Style: core.Style{FontSize: fontMeta, Color: colors.Muted()}},
				}})
		}
		return core.View{Style: core.Style{FlexGrow: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0)}, Children: []core.Node{header,
			core.Scroll{Style: core.Style{FlexGrow: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0), Padding: core.All(6)},
				Children: []core.Node{core.View{Style: core.Style{Gap: 2}, Children: nodes}}}}}
	})
}
