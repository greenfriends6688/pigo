package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/ui"
	"github.com/oneo-me/nsgo/ui/chat"
	"github.com/oneo-me/nsgo/ui/commitgraph"
	"github.com/oneo-me/nsgo/ui/filepicker"

	"pigo/internal/agent"
	"pigo/internal/config"
	"pigo/internal/files"
	"pigo/internal/models"
	"pigo/internal/nodeinstall"
	"pigo/internal/reducer"
	"pigo/internal/resources"
	"pigo/internal/sessions"
	"pigo/internal/skills"
	"pigo/internal/term"
	"pigo/internal/textdiff"
	"pigo/internal/toolfields"
	"pigo/internal/worktree"
)

// sessionRow is one row of the native session sidebar.
type sessionRow struct {
	ID, Title, Subtitle, Group string
	Project, Age               string
	// Snippet is the fragment of a full-text search hit in this session's
	// transcript; empty unless the row matched by content rather than name.
	Snippet  string
	Pinned   bool
	Archived bool
	// Unread is the row's own mark: set from the menu, cleared on opening.
	Unread bool
	// Preview and LastTurnTools are the newest turn, for the row's tooltip.
	Preview       string
	LastTurnTools int
}

// projectStat is what a project's menu reports: how many sessions its sweep
// would archive, how many sit in the archive, and how many its delete would
// remove.
type projectStat struct{ old, archived, total int }

// sessionItem is a flat row of the virtualized sidebar: a group header or a
// session. The ordinal keeps the TestID stable across group changes.
type sessionItem struct {
	Group      string
	GroupLabel string
	Count      int
	Header     bool
	Ordinal    int
	Row        sessionRow
}

// branchItem is one node of the conversation tree.
type branchItem struct {
	ID, Label string
	Depth     int
	Kind      string
}

// mcpItem is one MCP server row of the settings page.
type mcpItem struct {
	Name, Scope, Transport, Target string
	Enabled                        bool
}

// modelItem is one model pi offers.
type modelItem struct {
	ID, Name, Provider string
	Reasoning          bool
}

// imageAttachment is a picture pasted or dropped into the composer, sent to
// pi as an image block.
type imageAttachment struct {
	Name, MIME string
	Data       []byte
}

// commandItem is one slash command pi reports.
type commandItem struct {
	Name, Description, Source string
}

// toolRow is one tool-call card.
type toolRow struct {
	ID, Action, Detail, Status, Partial string
	IsError                             bool
	// Name is the raw tool name: edit/write cards render their Args as a
	// diff, and unknown tools fall back to a parameter view.
	Name string
	Args map[string]any
	// Diff is the unified patch edit/write calls carry, Add/Del its line
	// counts for the collapsed summary row.
	Diff     string
	Add, Del int
	// Nested are the codemode sub-calls a parent card lists in its body.
	// History replay carries none: pi's transcript does not keep them.
	Nested []nestedToolRow
}

// nestedToolRow is one codemode sub-call inside a parent card's body: the
// status mark and a one-line Action/Detail summary, never its output.
type nestedToolRow struct {
	ID, Action, Detail, Status string
	IsError                    bool
}

// turnBlock is one piece of a turn in the order it happened: reasoning, a
// run of tool calls, or a message the assistant wrote. Steps and Tools stay
// the flat views tests and actions use; Blocks keeps the chronology.
type turnBlock struct {
	Kind  string // "think", "tools" or "say"
	Text  string
	Tools []toolRow
}

// turnRow is one request and its response, the data chat.Turn draws.
// Steps holds the assistant's intermediate prose, Final the last answer.
type turnRow struct {
	ID      string
	EntryID string
	Prompt  string
	Steps   []string
	Thinks  []string
	Final   string
	Running bool
	Failed  bool
	Tools   []toolRow
	Blocks  []turnBlock
	// Error is a hard failure (401, quota, prompt_error) the retry banner
	// never covers; an empty string means the turn itself did not fail.
	Error string
	// StartedAt is when the prompt was sent, Elapsed how long the turn ran
	// (clock on the client when pi reports no timestamps), Model the model
	// that answered, Usage its token counts.
	StartedAt time.Time
	Elapsed   time.Duration
	Model     string
	Usage     turnUsage
}

// turnUsage is one turn's token counts, for the footer's meta line.
type turnUsage struct{ In, Out, Cache int }

// reviewStat is one changed file's line counts for the review list, which
// PI NEXT draws as +N −M beside the path. Untracked marks a file that has no
// patch at all (a new file), where the whole body counts as additions; Ready
// says the background prefetch finished, so 0/0 is a real answer and not a
// pending row.
type reviewStat struct {
	Add       int
	Del       int
	Untracked bool
	Ready     bool
}

// appState owns every signal and every long-lived resource of the window.
// Background goroutines (RPC stdout, session scan, git) only ever mutate
// signals through cx.Post — PLAN §9.5.
type appState struct {
	cx     *core.Scope
	colors ui.Colors

	agentDir, cwd, nodePath, bundle string
	nodeBundled                     bool
	store                           *sessions.Store
	cfg                             *config.Store
	pool                            *agent.Pool
	redux                           *reducer.Store
	terms                           *term.Manager

	rows         *core.Signal[[]sessionRow]
	sessionItems *core.Signal[[]sessionItem]
	mentions     *core.Signal[[]string]
	selected     *core.Signal[string]
	draft        *core.Signal[string]
	running      *core.Signal[bool]
	connected    *core.Signal[bool]
	// reconnecting is the window's own notice that the automatic restart is
	// in flight, so the capsule says 重连中 instead of flashing 未连接.
	reconnecting  *core.Signal[bool]
	status        *core.Signal[string]
	turns         *core.Signal[[]turnRow]
	banner        *core.Signal[string]
	queues        *core.Signal[string]
	steeringItems *core.Signal[[]string]
	followUpItems *core.Signal[[]string]
	queuePanel    *core.Signal[bool]
	autoLoading   bool
	turnHeights   map[string]core.Points
	query         *core.Signal[string]
	dock          *core.Signal[string]
	// dockOpenKinds are the tool tabs the right panel has open, in the
	// order dockKinds renders them; file tabs follow them from fileTabs.
	dockOpenKinds   *core.Signal[[]string]
	category        *core.Signal[string]
	settingsOpen    *core.Signal[bool]
	nodeStatus      *core.Signal[string]
	nodeReady       *core.Signal[bool]
	settingsQuery   *core.Signal[string]
	settingsInfo    *core.Signal[map[string]string]
	autoCompaction  *core.Signal[bool]
	autoRetry       *core.Signal[bool]
	noMCP           *core.Signal[bool]
	searchRows      *core.Signal[[]sessionRow]
	sessionsLoading *core.Signal[bool]
	// searchSnippets caches the last full-text scan (session path -> snippet)
	// and searchGen discards replays of a scan the query already superseded.
	searchSnippets *core.Signal[map[string]string]
	searchGen      int
	searchHitsFor  string // the needle searchSnippets was computed for
	sidebarOpen    *core.Signal[bool]
	rightOpen      *core.Signal[bool]
	paletteOpen    *core.Signal[bool]
	// paletteIndex is the highlighted option of the composer's inline
	// list; paletteDismissed hides it until the draft changes again.
	paletteIndex     *core.Signal[int]
	paletteDismissed *core.Signal[bool]
	extOpen          *core.Signal[bool]
	extCurrent       *core.Signal[reducer.ExtensionRequest]
	extInput         *core.Signal[string]
	fileDir          *core.Signal[string]
	filePreview      *core.Signal[string]
	fileImage        *core.Signal[core.ImageSource]
	fileDoc          *core.Signal[string]
	fileDocHTML      *core.Signal[string]
	diffPatch        *core.Signal[string]
	selectedFile     *core.Signal[string]
	fileTabs         *core.Signal[[]string]
	fileMode         *core.Signal[string]
	fileQuery        *core.Signal[string]
	fileSplit        *core.Signal[core.Points]
	previewFull      *core.Signal[bool]
	fileWrap         *core.Signal[bool]
	fileUnified      *core.Signal[bool]
	chatWidth        *core.Signal[core.Points]
	sendOnEnter      *core.Signal[bool]
	showHidden       *core.Signal[bool]
	treeFont         *core.Signal[int]
	appearance       *core.Signal[string]
	// The dock terminal's look, independent of the window's theme.
	termFont      *core.Signal[int]
	termTheme     *core.Signal[string]
	attachments   *core.Signal[[]imageAttachment]
	gitMarks      *core.Signal[map[string]string]
	expanded      *core.Signal[map[string]bool]
	treeRev       *core.Signal[int]
	slashCommands *core.Signal[[]commandItem]
	models        *core.Signal[[]modelItem]
	modelName     *core.Signal[string]
	thinking      *core.Signal[string]
	archiveView   *core.Signal[bool]
	deleteOpen    *core.Signal[bool]
	deleteTarget  *core.Signal[string]
	subOpen       *core.Signal[bool]
	subItems      *core.Signal[[]sessionRow]
	bashOutput    *core.Signal[string]
	bashCommand   *core.Signal[string]
	copied        *core.Signal[string]
	gitText       *core.Signal[string]
	gitHead       *core.Signal[string]
	gitLoading    *core.Signal[bool]
	gitCommits    *core.Signal[[]commitgraph.Commit]
	gitStatus     *core.Signal[files.GitStatus]
	reviewMode    *core.Signal[string]
	// reviewScope is whose changes the 变更 list shows: this conversation's
	// own writes ("session", the default) or the whole working tree
	// ("workspace"). Every session in a project shared the same git status,
	// which is what made all of them look like the same diff.
	reviewScope   *core.Signal[string]
	reviewSel     *ui.Selection
	reviewDiff    *core.Signal[string]
	reviewMessage *core.Signal[string]
	reviewBusy    *core.Signal[bool]
	// reviewStats are the +N −M counts of the changed files, prefetched off
	// the UI thread and keyed by path relative to reviewStatsRoot. The root
	// is kept so switching projects throws the old counts away, and
	// reviewStatsPending holds the paths whose counts are in flight so a
	// status refresh does not ask git the same question twice. Both are only
	// touched on the UI thread.
	reviewStats        *core.Signal[map[string]reviewStat]
	reviewStatsRoot    string
	reviewStatsPending map[string]bool

	termTabIDs *core.Signal[[]string]
	activeTerm *core.Signal[string]
	termRev    *core.Signal[int]
	wasRunning bool
	readOnly   *core.Signal[bool]
	// sideTabs are the side chats open in the right dock; sideRev repaints
	// the panel because their fields change under pointers.
	sideTabs *core.Signal[[]*sideTab]
	sideRev  *core.Signal[int]
	// Sidebar view and the status panel behind the capsule.
	sidebarView *core.Signal[string]
	statusPanel *core.Signal[bool]
	// In-transcript find (⌘F).
	findOpen  *core.Signal[bool]
	findQuery *core.Signal[string]
	findIndex *core.Signal[int]
	// Composer history and mode.
	promptHistory   *core.Signal[[]string]
	historyAt       *core.Signal[int]
	historyDraft    string
	composerFocused *core.Signal[bool]
	permissionMode  *core.Signal[string]
	toolMode        *core.Signal[string]
	dragActive      *core.Signal[bool]
	// File operations and external changes.
	pendingChange   *core.Signal[string]
	fileOpOpen      *core.Signal[bool]
	fileOpMode      *core.Signal[string]
	fileOpName      *core.Signal[string]
	fileOpDir       *core.Signal[string]
	pluginOpen      *core.Signal[bool]
	pluginSource    *core.Signal[string]
	pluginRemove    *core.Signal[bool]
	skillSearchOpen *core.Signal[bool]
	skillQuery      *core.Signal[string]
	skillHits       *core.Signal[[]skills.Hit]
	skillBusy       *core.Signal[bool]

	settingsRev  *core.Signal[int]
	providers    *core.Signal[[]models.Provider]
	providerOpen *core.Signal[bool]
	// The models page's master-detail: the provider the right pane shows, the
	// API key being typed, and the provider catalog dialog's search.
	providerSel      *core.Signal[string]
	providerKeyDraft *core.Signal[string]
	catalogOpen      *core.Signal[bool]
	catalogQuery     *core.Signal[string]
	providerEditID   *core.Signal[string]
	providerName     *core.Signal[string]
	providerBase     *core.Signal[string]
	providerAPI      *core.Signal[string]
	providerKey      *core.Signal[string]
	providerModels   *core.Signal[string]
	// The model form inside the Models settings section.
	modelOpen               *core.Signal[bool]
	modelFormOpen           *core.Signal[bool]
	modelProviderID         *core.Signal[string]
	modelEditID             *core.Signal[string]
	modelOriginalID         *core.Signal[string]
	modelFormName           *core.Signal[string]
	modelFormBase           *core.Signal[string]
	modelFormAPI            *core.Signal[string]
	modelFormContext        *core.Signal[string]
	modelFormMax            *core.Signal[string]
	modelFormReasoning      *core.Signal[bool]
	modelFormImage          *core.Signal[bool]
	modelFormCostInput      *core.Signal[string]
	modelFormCostOutput     *core.Signal[string]
	modelFormCostCacheRead  *core.Signal[string]
	modelFormCostCacheWrite *core.Signal[string]
	// enabledPatterns is settings.json's enabledModels whitelist; empty
	// means every model is available.
	enabledPatterns *core.Signal[[]string]
	enabledBusy     *core.Signal[bool]
	providerPreset  *core.Signal[string]
	catalog         *core.Signal[[]models.CatalogProvider]
	branchOpen      *core.Signal[bool]
	branchItems     *core.Signal[[]branchItem]
	notifyDone      *core.Signal[bool]
	// notifySound and notifyInApp are the other two notification channels;
	// notifyDone stays the desktop (Notification Center) one.
	notifySound *core.Signal[bool]
	notifyInApp *core.Signal[bool]
	// debugOpen shows the read-only diagnostics panel.
	debugOpen    *core.Signal[bool]
	modelDefault *core.Signal[string]
	mcpItems     *core.Signal[[]mcpItem]
	mcpOpen      *core.Signal[bool]
	mcpName      *core.Signal[string]
	mcpTransport *core.Signal[string]
	mcpCommand   *core.Signal[string]
	mcpArgs      *core.Signal[string]
	mcpURL       *core.Signal[string]
	mcpHeaders   *core.Signal[string]
	mcpProject   *core.Signal[bool]
	skillsList   *core.Signal[[]resources.Skill]
	skillOpen    *core.Signal[bool]
	skillName    *core.Signal[string]
	skillBody    *core.Signal[string]
	// skillPath is the SKILL.md the dialog is editing; empty for none.
	skillPath  *core.Signal[string]
	agentsList *core.Signal[[]resources.Agent]
	agentOpen  *core.Signal[bool]
	agentPath  *core.Signal[string]
	// agentBuiltin marks the dialog as showing one of pi's own profiles, which
	// is read-only.
	agentBuiltin   *core.Signal[bool]
	agentTitle     *core.Signal[string]
	agentBody      *core.Signal[string]
	trustOpen      *core.Signal[bool]
	trustDirs      *core.Signal[[]string]
	worktrees      *core.Signal[[]worktree.Worktree]
	worktreeOpen   *core.Signal[bool]
	worktreeBranch *core.Signal[string]
	worktreePath   *core.Signal[string]
	worktreeForce  *core.Signal[bool]
	projectOpen    *core.Signal[bool]
	renameOpen     *core.Signal[bool]
	renameName     *core.Signal[string]
	renameTarget   *core.Signal[string]
	// renameGroup is the project's group key when the rename dialog is naming
	// a project instead of a session.
	renameGroup       *core.Signal[string]
	deleteProject     *core.Signal[string]
	deleteProjectOpen *core.Signal[bool]
	ctxMenu           *core.Signal[string]
	collapsed         *core.Signal[map[string]bool]
	groupsInit        bool
	// widgetSignature is what the desktop widget last showed, so an unchanged
	// library does not reload WidgetKit's timeline again.
	widgetSignature string
	// projectStats are the per-group counts the project menu shows, filled
	// once per publish so a repaint never walks the whole library.
	projectStats map[string]projectStat
	// The user's project arrangement: pinned groups, their order, and any
	// rename. They live in prefs.json and are read once at startup.
	projectPin     map[string]bool
	projectOrder   []string
	projectAlias   map[string]string
	refreshPending bool
	paletteHandled bool
	expandThinking *core.Signal[bool]
	hideThinking   *core.Signal[bool]
	sideListH      *core.Signal[core.Points]
	turnLimit      *core.Signal[int]
	turnTotal      *core.Signal[int]
	ctxUsed        *core.Signal[int]
	ctxWindow      *core.Signal[int]
	statsLine      *core.Signal[string]
	widgetText     *core.Signal[string]
	statsIn        *core.Signal[float64]
	statsOut       *core.Signal[float64]
	statsCache     *core.Signal[float64]
	fileTable      *core.Signal[[][]string]

	conversation *chat.ConversationState
	selection    *ui.Selection
	sessionSel   *ui.Selection
	gitSel       *ui.Selection
	picker       *filepicker.State
	toaster      *ui.Toaster
	dirs         *dirCache
	extDone      map[string]bool // extension UI request ids already answered

	termMu   sync.Mutex
	termTabs map[string]*term.Tab
	// termStarting holds a shell that is still proving it runs, so a second
	// press does not start another one.
	termStarting bool
	// transcriptLoading holds a skeleton over the transcript while a session
	// it is opening has not sent its messages yet: the first paint after a
	// click used to be an empty chat, which read as "the click did nothing".
	transcriptLoading *core.Signal[bool]
	// dictation is the speech session behind the composer's microphone, and
	// dictating lights the button while it listens.
	dictation *dictation
	dictating *core.Signal[bool]
	// forkPending is the session a row's "分叉" waits for: the fork runs once
	// that session is connected, since pi forks the open session.
	forkPending string
	// scanBusy keeps one session-library rescan in flight at a time.
	scanBusy  atomic.Bool
	termOrder []string // creation order of the terminal tabs
	turnRefs  map[string]*core.Ref

	// Terminal search: the find bar's text, whether it shows, and a pulse
	// that repaints the match count while the emulator searches its
	// scrollback in the frames after the keystroke.
	termSearch     *core.Signal[string]
	termSearchOpen *core.Signal[bool]
	termSearchRev  *core.Signal[int]
	termSearching  bool

	// drafts keeps the composer text per session, so switching sessions
	// neither leaks one session's half-typed message into another nor
	// loses it (it used to be one global signal).
	drafts map[string]string
	// attachmentsBySession keeps each session's pictures with it across a
	// switch, as drafts are kept.
	attachmentsBySession map[string][]imageAttachment
	// runStart is when the current run began, for the working label and
	// the status capsule; zero while idle.
	runStart time.Time
	// runSessionID is the session the current run belongs to; switching
	// while it runs must not attribute its duration to the new session.
	runSessionID string
	// lastElapsedBySession keeps the finished turn's length per session;
	// a single window-wide value painted the previous session's duration
	// onto whichever session was opened next.
	lastElapsedBySession map[string]time.Duration
	// lastDisconnect is what the child said just before it went away. The
	// card shows it instead of pointing at a log file: a pi that dies on a
	// broken extension writes the reason to stderr and nowhere else.
	lastDisconnect string
	// autoRetrying is the automatic restart after an unexpected child exit;
	// quitting stops it so the pool's teardown is not answered by a respawn.
	autoRetrying bool
	quitting     bool
	runSeconds   *core.Signal[int]
	fileDrafts   map[string]string // edited file contents not yet saved
	fileDirty    *core.Signal[map[string]bool]

	// pendingTrust is the directory the trust gate is showing;
	// pendingTrustAction the session to open once it is trusted.
	pendingTrust       string
	pendingTrustAction string

	cwdMu   sync.RWMutex
	mu      sync.Mutex
	handle  *agent.SessionHandle
	connKey string
}

func newState(cx *core.Scope) *appState {
	agentDir := agent.ResolveAgentDir()
	// An app launched from Finder has "/" as its working directory, which
	// would put the file tree at the filesystem root and make pi open a
	// session there. Fall back to the home directory.
	home, _ := os.UserHomeDir()
	cwd, err := os.Getwd()
	if err != nil || cwd == "" || cwd == "/" {
		cwd = home
	}
	nativeDir := filepath.Join(agentDir, "pigo")
	_ = os.MkdirAll(nativeDir, 0o700)
	prefs := loadPrefs(agentDir)
	project := resolveProjectDir(cwd, home, prefs.Project)
	if project != "" {
		cwd = project
	}

	node := agent.ResolveNode()
	bundled := bundledNode()
	if bundled != "" {
		node = bundled // B build: a stripped node shipped inside the .app
	}
	sidebarOpen := true
	if prefs.SidebarOpen != nil {
		sidebarOpen = *prefs.SidebarOpen
	}
	dockOpen := false
	if prefs.DockOpen != nil {
		dockOpen = *prefs.DockOpen
	}
	dockTab := prefs.DockTab
	if dockTab != "files" && dockTab != "review" && dockTab != "terminal" && dockTab != "subagents" {
		dockTab = "files"
	}
	sidebarView := prefs.SidebarView
	if sidebarView != "projects" && sidebarView != "recent" {
		sidebarView = "projects"
	}
	s := &appState{
		cx:                   cx,
		colors:               ui.ColorsOf(cx),
		agentDir:             agentDir,
		projectPin:           prefs.projectPins(),
		projectOrder:         append([]string(nil), prefs.ProjectOrder...),
		projectAlias:         prefs.projectAliases(),
		cwd:                  cwd,
		nodePath:             node,
		nodeBundled:          bundled != "",
		bundle:               agent.ResolveBundle(resourceDir()),
		store:                sessions.NewStore(agentDir),
		cfg:                  config.NewStore(agentDir),
		pool:                 agent.NewPool(4, 10*time.Minute),
		redux:                reducer.New(),
		terms:                term.NewManager(),
		termTabs:             make(map[string]*term.Tab),
		termOrder:            []string{},
		drafts:               make(map[string]string),
		attachmentsBySession: make(map[string][]imageAttachment),
		lastElapsedBySession: make(map[string]time.Duration),
		turnHeights:          make(map[string]core.Points),
		fileDrafts:           make(map[string]string),
		conversation:         chat.NewConversationState(cx),
		selection:            ui.NewSelection(cx, false),
		sessionSel:           ui.NewSelection(cx, false),
		gitSel:               ui.NewSelection(cx, false),
		reviewSel:            ui.NewSelection(cx, false),
		toaster:              ui.NewToaster(cx),
		dirs:                 newDirCache(),
		extDone:              make(map[string]bool),
	}
	s.picker = filepicker.NewState(cx, filepicker.List, false, 220)
	s.picker.Load = s.loadPickerFolder

	s.rows = core.NewSignalFunc(cx, []sessionRow{}, nil)
	s.sessionItems = core.NewSignalFunc(cx, []sessionItem{}, nil)
	s.mentions = core.NewSignalFunc(cx, []string{}, nil)
	s.searchRows = core.NewSignalFunc(cx, []sessionRow{}, nil)
	s.searchSnippets = core.NewSignalFunc(cx, map[string]string{}, nil)
	s.sessionsLoading = core.NewSignal(cx, true)
	s.turns = core.NewSignalFunc(cx, []turnRow{}, nil)
	s.termTabIDs = core.NewSignalFunc(cx, []string{}, nil)
	s.gitCommits = core.NewSignalFunc(cx, []commitgraph.Commit{}, nil)
	s.extCurrent = core.NewSignalFunc(cx, reducer.ExtensionRequest{}, nil)
	s.selected = core.NewSignal(cx, "")
	s.draft = core.NewSignal(cx, "")
	s.running = core.NewSignal(cx, false)
	s.connected = core.NewSignal(cx, false)
	s.reconnecting = core.NewSignal(cx, false)
	s.dictating = core.NewSignal(cx, false)
	s.transcriptLoading = core.NewSignal(cx, false)
	s.status = core.NewSignal(cx, "正在读取会话…")
	s.banner = core.NewSignal(cx, "")
	s.queues = core.NewSignal(cx, "")
	s.steeringItems = core.NewSignalFunc(cx, []string{}, nil)
	s.followUpItems = core.NewSignalFunc(cx, []string{}, nil)
	s.queuePanel = core.NewSignal(cx, false)
	s.query = core.NewSignal(cx, "")
	dockKindsOpen := []string{}
	if prefs.DockTabs != nil {
		dockKindsOpen = append(dockKindsOpen, prefs.DockTabs...)
	}
	if len(dockKindsOpen) == 0 {
		dockKindsOpen = []string{"files", "review", "terminal", "subagents"}
	}
	s.dock = core.NewSignal(cx, dockTab)
	s.dockOpenKinds = core.NewSignalFunc(cx, dockKindsOpen, nil)
	s.category = core.NewSignal(cx, "general")
	s.settingsOpen = core.NewSignal(cx, false)
	s.debugOpen = core.NewSignal(cx, false)
	s.settingsQuery = core.NewSignal(cx, "")
	s.settingsInfo = core.NewSignalFunc(cx, map[string]string{}, nil)
	s.nodeStatus = core.NewSignal(cx, "正在检测 Node…")
	s.nodeReady = core.NewSignal(cx, false)
	s.copied = core.NewSignal(cx, "")
	s.selectedFile = core.NewSignal(cx, "")
	s.diffPatch = core.NewSignal(cx, "")
	expanded := map[string]bool{}
	if project != "" {
		expanded[project] = true
	}
	s.expanded = core.NewSignalFunc(cx, expanded, nil)
	s.treeRev = core.NewSignal(cx, 0)
	settings, _ := s.cfg.Settings()
	s.autoCompaction = core.NewSignal(cx, boolSetting(settings, "autoCompaction", true))
	s.autoRetry = core.NewSignal(cx, boolSetting(settings, "autoRetry", true))
	s.noMCP = core.NewSignal(cx, boolSetting(settings, "noMCP", false))
	s.sidebarOpen = core.NewSignal(cx, sidebarOpen)
	// The chat is the page; files, Git and the terminal open on demand from
	// the composer or the title bar instead of squeezing the transcript.
	s.rightOpen = core.NewSignal(cx, dockOpen)
	s.paletteOpen = core.NewSignal(cx, false)
	s.extOpen = core.NewSignal(cx, false)
	s.extInput = core.NewSignal(cx, "")
	s.fileDir = core.NewSignal(cx, project)
	s.filePreview = core.NewSignal(cx, "")
	s.fileImage = core.NewSignalFunc(cx, core.ImageSource{}, nil)
	s.fileDoc = core.NewSignal(cx, "")
	s.fileDocHTML = core.NewSignal(cx, "")
	s.fileTabs = core.NewSignalFunc(cx, []string{}, nil)
	s.fileMode = core.NewSignal(cx, "source")
	s.fileQuery = core.NewSignal(cx, "")
	s.fileSplit = core.NewSignal(cx, core.Points(160))
	s.previewFull = core.NewSignal(cx, false)
	s.fileWrap = core.NewSignal(cx, true)
	s.fileUnified = core.NewSignal(cx, true)
	chatWidth := prefs.ChatWidth
	if chatWidth < 560 || chatWidth > 900 {
		chatWidth = 720
	}
	s.chatWidth = core.NewSignal(cx, core.Points(chatWidth))
	sendOnEnter := true
	if prefs.SendOnEnter != nil {
		sendOnEnter = *prefs.SendOnEnter
	}
	s.sendOnEnter = core.NewSignal(cx, sendOnEnter)
	s.gitStatus = core.NewSignalFunc(cx, files.GitStatus{}, nil)
	s.reviewMode = core.NewSignal(cx, "changes")
	s.reviewScope = core.NewSignal(cx, "session")
	s.reviewDiff = core.NewSignal(cx, "")
	s.reviewMessage = core.NewSignal(cx, "")
	s.reviewBusy = core.NewSignal(cx, false)
	s.reviewStats = core.NewSignalFunc(cx, map[string]reviewStat{}, nil)
	s.reviewStatsPending = map[string]bool{}
	s.showHidden = core.NewSignal(cx, prefs.ShowHidden)
	treeFont := prefs.TreeFont
	if treeFont < 10 || treeFont > 20 {
		treeFont = 12
	}
	s.treeFont = core.NewSignal(cx, treeFont)
	appearance := prefs.Appearance
	if appearance != "light" && appearance != "dark" {
		appearance = "system"
	}
	s.appearance = core.NewSignal(cx, appearance)
	// An out-of-range size or an unknown theme (an older prefs file, a hand
	// edit) reads as the default rather than a broken terminal.
	termFont := prefs.TermFont
	if termFont < minTermFont || termFont > maxTermFont {
		termFont = defaultTermFont
	}
	s.termFont = core.NewSignal(cx, termFont)
	s.termTheme = core.NewSignal(cx, normalizeTermTheme(prefs.TermTheme))
	// Apply after the window mounts: changing the subtree appearance during
	// construction makes the platform restyle layers that have no window yet.
	cx.AfterFunc(10*time.Millisecond, func() { s.applyAppearance(appearance) })
	s.attachments = core.NewSignalFunc(cx, []imageAttachment{}, nil)
	s.gitMarks = core.NewSignalFunc(cx, map[string]string{}, nil)
	s.slashCommands = core.NewSignalFunc(cx, []commandItem{}, nil)
	s.models = core.NewSignalFunc(cx, []modelItem{}, nil)
	s.modelName = core.NewSignal(cx, "")
	s.thinking = core.NewSignal(cx, "")
	s.archiveView = core.NewSignal(cx, false)
	s.deleteOpen = core.NewSignal(cx, false)
	s.deleteTarget = core.NewSignal(cx, "")
	s.subOpen = core.NewSignal(cx, false)
	s.subItems = core.NewSignalFunc(cx, []sessionRow{}, nil)
	s.bashOutput = core.NewSignal(cx, "")
	s.bashCommand = core.NewSignal(cx, "")
	s.gitText = core.NewSignal(cx, "")
	s.gitHead = core.NewSignal(cx, "")
	s.gitLoading = core.NewSignal(cx, false)
	s.activeTerm = core.NewSignal(cx, "")
	s.termRev = core.NewSignal(cx, 0)
	s.termSearch = core.NewSignal(cx, "")
	s.termSearchOpen = core.NewSignal(cx, false)
	s.termSearchRev = core.NewSignal(cx, 0)
	s.settingsRev = core.NewSignal(cx, 0)
	s.providers = core.NewSignalFunc(cx, []models.Provider{}, nil)
	s.providerOpen = core.NewSignal(cx, false)
	s.providerSel = core.NewSignal(cx, "")
	s.providerKeyDraft = core.NewSignal(cx, "")
	s.catalogOpen = core.NewSignal(cx, false)
	s.catalogQuery = core.NewSignal(cx, "")
	s.providerEditID = core.NewSignal(cx, "")
	s.providerName = core.NewSignal(cx, "")
	s.providerBase = core.NewSignal(cx, "")
	s.providerAPI = core.NewSignal(cx, "")
	s.providerKey = core.NewSignal(cx, "")
	s.providerModels = core.NewSignal(cx, "")
	s.modelOpen = core.NewSignal(cx, false)
	s.modelFormOpen = core.NewSignal(cx, false)
	s.modelProviderID = core.NewSignal(cx, "")
	s.modelEditID = core.NewSignal(cx, "")
	s.modelOriginalID = core.NewSignal(cx, "")
	s.modelFormName = core.NewSignal(cx, "")
	s.modelFormBase = core.NewSignal(cx, "")
	s.modelFormAPI = core.NewSignal(cx, "")
	s.modelFormContext = core.NewSignal(cx, "")
	s.modelFormMax = core.NewSignal(cx, "")
	s.modelFormReasoning = core.NewSignal(cx, false)
	s.modelFormImage = core.NewSignal(cx, false)
	s.modelFormCostInput = core.NewSignal(cx, "")
	s.modelFormCostOutput = core.NewSignal(cx, "")
	s.modelFormCostCacheRead = core.NewSignal(cx, "")
	s.modelFormCostCacheWrite = core.NewSignal(cx, "")
	s.enabledPatterns = core.NewSignalFunc(cx, []string{}, nil)
	s.enabledBusy = core.NewSignal(cx, false)
	s.providerPreset = core.NewSignal(cx, "")
	s.catalog = core.NewSignalFunc(cx, []models.CatalogProvider{}, nil)
	s.branchOpen = core.NewSignal(cx, false)
	s.branchItems = core.NewSignalFunc(cx, []branchItem{}, nil)
	desktopNotify := boolSetting(settings, "notifyOnDone", true)
	if prefs.NotifyDesktop != nil {
		desktopNotify = *prefs.NotifyDesktop
	}
	notifyInApp := true
	if prefs.NotifyInApp != nil {
		notifyInApp = *prefs.NotifyInApp
	}
	s.notifyDone = core.NewSignal(cx, desktopNotify)
	s.notifySound = core.NewSignal(cx, prefs.NotifySound)
	s.notifyInApp = core.NewSignal(cx, notifyInApp)
	s.modelDefault = core.NewSignal(cx, "")
	s.mcpItems = core.NewSignalFunc(cx, []mcpItem{}, nil)
	s.mcpOpen = core.NewSignal(cx, false)
	s.mcpName = core.NewSignal(cx, "")
	s.mcpTransport = core.NewSignal(cx, "stdio")
	s.mcpCommand = core.NewSignal(cx, "")
	s.mcpArgs = core.NewSignal(cx, "")
	s.mcpURL = core.NewSignal(cx, "")
	s.mcpHeaders = core.NewSignal(cx, "")
	s.mcpProject = core.NewSignal(cx, false)
	s.skillsList = core.NewSignalFunc(cx, []resources.Skill{}, nil)
	s.skillOpen = core.NewSignal(cx, false)
	s.skillName = core.NewSignal(cx, "")
	s.skillBody = core.NewSignal(cx, "")
	s.skillPath = core.NewSignal(cx, "")
	s.agentsList = core.NewSignalFunc(cx, []resources.Agent{}, nil)
	s.agentOpen = core.NewSignal(cx, false)
	s.agentPath = core.NewSignal(cx, "")
	s.agentBuiltin = core.NewSignal(cx, false)
	s.agentTitle = core.NewSignal(cx, "")
	s.agentBody = core.NewSignal(cx, "")
	s.trustOpen = core.NewSignal(cx, false)
	s.trustDirs = core.NewSignalFunc(cx, []string{}, nil)
	s.worktrees = core.NewSignalFunc(cx, []worktree.Worktree{}, nil)
	s.worktreeOpen = core.NewSignal(cx, false)
	s.worktreeBranch = core.NewSignal(cx, "")
	s.worktreePath = core.NewSignal(cx, "")
	s.worktreeForce = core.NewSignal(cx, false)
	s.projectOpen = core.NewSignal(cx, false)
	s.renameOpen = core.NewSignal(cx, false)
	s.renameName = core.NewSignal(cx, "")
	s.renameTarget = core.NewSignal(cx, "")
	s.renameGroup = core.NewSignal(cx, "")
	s.deleteProject = core.NewSignal(cx, "")
	s.deleteProjectOpen = core.NewSignal(cx, false)
	s.ctxMenu = core.NewSignal(cx, "")
	s.collapsed = core.NewSignalFunc(cx, map[string]bool{}, nil)
	s.expandThinking = core.NewSignal(cx, !prefs.CollapseSteps)
	s.hideThinking = core.NewSignal(cx, prefs.HideThinking)
	s.sideListH = core.NewSignal(cx, core.Points(0))
	s.turnLimit = core.NewSignal(cx, 20)
	s.turnTotal = core.NewSignal(cx, 0)
	s.ctxUsed = core.NewSignal(cx, 0)
	s.ctxWindow = core.NewSignal(cx, 0)
	s.statsLine = core.NewSignal(cx, "")
	s.widgetText = core.NewSignal(cx, "")
	s.statsIn = core.NewSignal(cx, float64(0))
	s.statsOut = core.NewSignal(cx, float64(0))
	s.statsCache = core.NewSignal(cx, float64(0))
	s.fileTable = core.NewSignalFunc(cx, [][]string{}, nil)
	s.runSeconds = core.NewSignal(cx, 0)
	s.fileDirty = core.NewSignalFunc(cx, map[string]bool{}, nil)
	s.readOnly = core.NewSignal(cx, false)
	s.sideTabs = core.NewSignalFunc(cx, []*sideTab{}, nil)
	s.sideRev = core.NewSignal(cx, 0)
	s.sidebarView = core.NewSignal(cx, sidebarView)
	s.statusPanel = core.NewSignal(cx, false)
	s.findOpen = core.NewSignal(cx, false)
	s.findQuery = core.NewSignal(cx, "")
	s.findIndex = core.NewSignal(cx, 0)
	s.promptHistory = core.NewSignalFunc(cx, []string{}, nil)
	s.historyAt = core.NewSignal(cx, -1)
	s.composerFocused = core.NewSignal(cx, false)
	s.permissionMode = core.NewSignal(cx, "default")
	s.toolMode = core.NewSignal(cx, "default")
	s.dragActive = core.NewSignal(cx, false)
	s.pendingChange = core.NewSignal(cx, "")
	s.fileOpOpen = core.NewSignal(cx, false)
	s.fileOpMode = core.NewSignal(cx, "")
	s.fileOpName = core.NewSignal(cx, "")
	s.fileOpDir = core.NewSignal(cx, "")
	s.pluginOpen = core.NewSignal(cx, false)
	s.pluginSource = core.NewSignal(cx, "")
	s.pluginRemove = core.NewSignal(cx, false)
	s.skillSearchOpen = core.NewSignal(cx, false)
	s.skillQuery = core.NewSignal(cx, "")
	s.skillHits = core.NewSignalFunc(cx, []skills.Hit{}, nil)
	s.skillBusy = core.NewSignal(cx, false)
	s.paletteIndex = core.NewSignal(cx, 0)
	s.paletteDismissed = core.NewSignal(cx, false)
	s.installKeys()

	cx.Cleanup(func() {
		s.quitting = true
		s.autoRetrying = false
		_ = s.pool.Close()
		_ = s.terms.CloseAll()
		_ = s.store.SaveCache()
	})

	if project != "" {
		s.reloadTree()
		s.savePrefsAsync(project, prefs.ShowHidden)
	}
	s.startSessionScan()
	s.startWatcher()
	s.loadMentions()
	go s.refreshSettings()
	if os.Getenv("PIGO_NO_AGENT") == "1" {
		s.nodeStatus.Set("测试模式：不启动子进程")
		// No child will ever start, so "no runtime yet" would pin the capsule
		// to the download pill forever; the offline pill is the truth here.
		s.nodeReady.Set(true)
		return s
	}
	s.installDesktop()
	s.bootstrapNode()
	return s
}

const emptyChatHint = "选择项目，输入消息开始。@ 引用文件，/ 打开命令，⌘K 打开命令面板。"

// resourceDir finds the bundled resources directory when running from an
// .app bundle; the empty string makes agent.ResolveBundle fall back to the
// pi installation on PATH.
func resourceDir() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), "..", "Resources")
	}
	return ""
}

// bundledNode returns the node binary of a B build (PLAN §11), if present and
// runnable. A binary whose signature the packaging step broke is killed at exec
// on Apple silicon, and every RPC child then died on start while the window
// showed nothing but "连接中断"; so the file must answer `--version` before it
// is trusted, and the system node is the fallback.
func bundledNode() string {
	dirs := []string{filepath.Join("resources", "bin", "node")}
	if r := resourceDir(); r != "" {
		dirs = append(dirs, filepath.Join(r, "bin", "node"))
	}
	for _, p := range dirs {
		if st, err := os.Stat(p); err != nil || st.IsDir() || st.Mode()&0o111 == 0 {
			continue
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		if nodeRuns(abs) {
			return abs
		}
	}
	return ""
}

// nodeRuns executes a candidate node once; macOS reports a broken signature as
// an immediate SIGKILL, which this turns into a clean fallback.
func nodeRuns(path string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, path, "--version").Run() == nil
}

func (s *appState) startSessionScan() {
	go func() {
		_ = s.store.LoadCache()
		_ = s.store.Refresh()
		s.cx.Post(func() {
			s.publishSessions()
			if s.rightOpen.GetUntracked() && s.dock.GetUntracked() == "subagents" {
				s.loadSubagents()
			}
		})
	}()
}

// startWatcher polls the session tree every two seconds. A native fsnotify
// watcher is unnecessary here: stat of the project directories is cheap and
// the virtual clock in headless tests drives this via app.Advance.
func (s *appState) startWatcher() {
	s.cx.AfterFunc(2*time.Second, func() {
		// One rescan at a time: a library of hundreds of sessions can take
		// longer than the interval, and overlapping rescans kept every core
		// busy on a window nobody had touched.
		if !s.scanBusy.CompareAndSwap(false, true) {
			s.startWatcher()
			return
		}
		go func() {
			defer s.scanBusy.Store(false)
			changed, err := s.store.RefreshIfChanged()
			if err == nil && changed {
				s.cx.Post(func() {
					s.publishSessions()
					_ = s.store.SaveCache()
				})
			}
		}()
		s.startWatcher()
	})
}

func (s *appState) publishSessions() {
	s.sessionsLoading.Set(false)
	all := s.store.List()
	archived := false
	if s.archiveView.GetUntracked() {
		all = s.store.Archived()
		archived = true
	}
	rows := make([]sessionRow, 0, len(all))
	for _, info := range all {
		title := info.Name
		if title == "" {
			title = info.FirstMessage
		}
		if title == "" {
			title = info.ID
		}
		if len([]rune(title)) > 60 {
			title = string([]rune(title)[:60]) + "…"
		}
		group := info.ProjectKey
		if group == "" {
			group = "其它项目"
		}
		label := projectTitleOf(info)
		if info.Side {
			group, label = "侧聊", "侧聊"
		}
		if archived {
			group, label = "归档", "归档"
		}
		if alias := s.projectAlias[group]; alias != "" {
			label = alias
		}
		rows = append(rows, sessionRow{
			ID: info.Path, Title: title, Subtitle: info.Cwd,
			Project: label, Age: relativeAge(info.Modified),
			Group: group, Pinned: info.Pinned, Archived: info.Archived, Unread: info.Unread,
			Preview: info.Preview, LastTurnTools: info.LastTurnTools,
		})
	}
	if !s.groupsInit && len(rows) > 0 {
		s.groupsInit = true
		// One project open, the rest folded: a library of hundreds of
		// sessions stays a list of projects, not a wall of folder names.
		collapsed := make(map[string]bool, len(rows))
		for _, r := range rows {
			collapsed[r.Group] = true
		}
		open := ""
		if sel := s.selected.GetUntracked(); sel != "" {
			for _, r := range rows {
				if r.ID == sel {
					open = r.Group
					break
				}
			}
		}
		if open == "" {
			open = rows[0].Group
		}
		collapsed[open] = false
		s.collapsed.Set(collapsed)
	}
	s.countProjectStats(rows)
	rows = s.orderByProject(rows)
	if !slices.Equal(rows, s.rows.GetUntracked()) {
		s.rows.Set(rows)
		s.updateWidgets()
		s.applySearch(s.query.GetUntracked())
		return
	}
	// Unchanged rows and an empty query need no second pass: the live
	// session's own file grows every couple of seconds, and rebuilding the
	// whole list for that was pure work.
	if strings.TrimSpace(s.query.GetUntracked()) != "" {
		s.applySearch(s.query.GetUntracked())
	}
}

// applySearch is the sidebar's search entry point: it filters rows by q and,
// for a non-empty q, schedules one debounced full-text scan of the visible
// transcripts. Callers are frequent (group folding, every session refresh), so
// the scan is debounced by 220 ms and replayed scans are dropped by generation.
func (s *appState) applySearch(q string) {
	s.query.Set(q)
	needle := strings.ToLower(strings.TrimSpace(q))
	s.searchGen++
	gen := s.searchGen
	if needle == "" {
		s.searchHitsFor = ""
		if len(s.searchSnippets.GetUntracked()) > 0 {
			s.searchSnippets.Set(map[string]string{})
		}
		s.rebuildSearchRows()
		return
	}
	// Snippets from another query would caption a row with a fragment of the
	// previous search, so they are dropped until this query's scan lands.
	if needle != s.searchHitsFor {
		s.searchHitsFor = needle
		if len(s.searchSnippets.GetUntracked()) > 0 {
			s.searchSnippets.Set(map[string]string{})
		}
	}
	s.rebuildSearchRows()
	s.cx.AfterFunc(220*time.Millisecond, func() {
		if gen != s.searchGen {
			return // a later keystroke already owns the search
		}
		go func() {
			hits := s.store.SearchContent(needle, 30)
			s.cx.Post(func() {
				if gen != s.searchGen {
					return
				}
				s.searchSnippets.Set(hits)
				// Rebuild only: scheduling from here would scan again forever.
				s.rebuildSearchRows()
			})
		}()
	})
}

// rebuildSearchRows renders s.searchRows and s.sessionItems from the current
// query and snippets. It never schedules a scan, so both applySearch (once)
// and the async result (once) can call it.
func (s *appState) rebuildSearchRows() {
	rows := s.rows.GetUntracked()
	collapsed := s.collapsed.GetUntracked()
	needle := strings.ToLower(strings.TrimSpace(s.query.GetUntracked()))
	if needle == "" {
		items := itemsFromRows(rows, collapsed)
		if !slices.Equal(rows, s.searchRows.GetUntracked()) {
			s.searchRows.Set(rows)
		}
		if !slices.Equal(items, s.sessionItems.GetUntracked()) {
			s.sessionItems.Set(items)
		}
		return
	}
	snippets := s.searchSnippets.GetUntracked()
	seen := make(map[string]bool, len(rows))
	out := make([]sessionRow, 0, len(rows))
	for _, r := range rows {
		if !strings.Contains(strings.ToLower(r.Title), needle) &&
			!strings.Contains(strings.ToLower(r.Subtitle), needle) {
			continue
		}
		r.Snippet = snippets[r.ID]
		seen[r.ID] = true
		out = append(out, r)
	}
	// A transcript hit puts its session in the list even when the title and
	// the directory say nothing about the query.
	for _, r := range rows {
		if seen[r.ID] {
			continue
		}
		if snippet := snippets[r.ID]; snippet != "" {
			r.Snippet = snippet
			out = append(out, r)
		}
	}
	if !slices.Equal(out, s.searchRows.GetUntracked()) {
		s.searchRows.Set(out)
	}
	// A search shows what it found even inside a folded project.
	items := itemsFromRows(out, nil)
	if !slices.Equal(items, s.sessionItems.GetUntracked()) {
		s.sessionItems.Set(items)
	}
}

// itemsFromRows lays sessions under one header per project, keeping the
// store's order of groups. ui.List virtualizes the result, so the sidebar is
// no longer capped at 200 rows per project.
func itemsFromRows(rows []sessionRow, collapsed map[string]bool) []sessionItem {
	var order []string
	byGroup := make(map[string][]sessionRow)
	for _, r := range rows {
		if _, ok := byGroup[r.Group]; !ok {
			order = append(order, r.Group)
		}
		byGroup[r.Group] = append(byGroup[r.Group], r)
	}
	out := make([]sessionItem, 0, len(rows)+len(order))
	n := 0
	for _, group := range order {
		items := byGroup[group]
		sort.SliceStable(items, func(i, j int) bool { return items[i].Pinned && !items[j].Pinned })
		label := items[0].Project
		if label == "" {
			label = group
		}
		out = append(out, sessionItem{Group: group, GroupLabel: label, Count: len(items), Header: true})
		if collapsed[group] {
			continue
		}
		for _, r := range items {
			out = append(out, sessionItem{Group: group, GroupLabel: label, Ordinal: n, Row: r})
			n++
		}
	}
	return out
}

// countProjectStats fills the menu counts for one publish: sessions older
// than a week per group, archived per group, and totals.
func (s *appState) countProjectStats(rows []sessionRow) {
	cutoff := time.Now().AddDate(0, 0, -7)
	stats := make(map[string]projectStat, 8)
	// The sweep archives by the file's mtime, so ask the store for it: the
	// rows carry only the relative age.
	old := map[string]int{}
	for _, info := range s.store.List() {
		if info.Modified.Before(cutoff) {
			old[info.ProjectKey]++
		}
	}
	for _, info := range s.store.Archived() {
		st := stats[info.ProjectKey]
		st.archived++
		stats[info.ProjectKey] = st
	}
	for _, r := range rows {
		st := stats[r.Group]
		st.total++
		st.old = old[r.Group]
		stats[r.Group] = st
	}
	s.projectStats = stats
}

// orderByProject puts the groups in the order the user arranged them:
// pinned projects first, then the saved order, then whatever is left in the
// order the store returned. Sessions keep their own order inside a group.
func (s *appState) orderByProject(rows []sessionRow) []sessionRow {
	if len(s.projectPin) == 0 && len(s.projectOrder) == 0 {
		return rows
	}
	rank := make(map[string]int, len(s.projectOrder))
	for i, group := range s.projectOrder {
		rank[group] = i
	}
	first := make(map[string]int, 8)
	for i, r := range rows {
		if _, ok := first[r.Group]; !ok {
			first[r.Group] = i
		}
	}
	out := append([]sessionRow(nil), rows...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Group, out[j].Group
		if a == b {
			return false
		}
		pa, pb := s.projectPin[a], s.projectPin[b]
		if pa != pb {
			return pa
		}
		ra, oka := rank[a]
		rb, okb := rank[b]
		switch {
		case oka && okb:
			return ra < rb
		case oka:
			return true
		case okb:
			return false
		}
		return first[a] < first[b]
	})
	return out
}

// projectTitleOf names a project the sidebar header shows: the folder's base
// name instead of the full path every session row used to repeat.
func projectTitleOf(info sessions.Info) string {
	dir := info.Cwd
	if dir == "" {
		dir = info.ProjectKey
	}
	if base := baseName(dir); base != "" && base != "." && base != "/" {
		return base
	}
	return "其它项目"
}

// relativeAge is how long ago a session changed, short enough for a row.
func relativeAge(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return fmt.Sprintf("%d 分钟前", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d 小时前", int(d.Hours()))
	case d < 48*time.Hour:
		return "昨天"
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%d 天前", int(d.Hours()/24))
	}
	return t.Format("2006-01-02")
}

// currentCwd is the project directory sessions, terminals and the file tree
// use. It moves when a session records another one or the user picks another
// project, so every goroutine reads it through the lock.
func (s *appState) currentCwd() string {
	s.cwdMu.RLock()
	defer s.cwdMu.RUnlock()
	return s.cwd
}

func (s *appState) setCwd(dir string) {
	if dir == "" {
		return
	}
	s.cwdMu.Lock()
	s.cwd = dir
	s.cwdMu.Unlock()
}

// sessionKey identifies a process in the pool. A new session uses the cwd as
// its key until the RPC reports a concrete session file.
func (s *appState) sessionKey(path string) string {
	if path == "" {
		return "new:" + s.currentCwd()
	}
	return path
}

func (s *appState) current() *agent.SessionHandle {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.handle
}

// sessionCwd is the directory a session was recorded in. pi refuses to open
// a session whose stored cwd is gone, so a missing one falls back to the
// app's own directory and the caller tells the user.
func (s *appState) sessionCwd(sessionPath string) (string, string) {
	if sessionPath == "" {
		return s.currentCwd(), ""
	}
	// The index already holds the cwd; reading the whole transcript here put
	// a file parse on every click in the sidebar.
	info, ok := s.store.Info(sessionPath)
	if !ok || info.Cwd == "" {
		var err error
		info, err = sessions.Read(sessionPath)
		if err != nil || info.Cwd == "" {
			return s.currentCwd(), ""
		}
	}
	if st, err := os.Stat(info.Cwd); err == nil && st.IsDir() {
		return info.Cwd, ""
	}
	return s.currentCwd(), info.Cwd
}

// connect starts (or restarts) the pi RPC child process. Reconnection is just
// another Acquire with --session, followed by get_state + get_messages to
// rebuild the whole reducer state (PLAN §2.6).
func (s *appState) connect(sessionPath string) {
	s.connectWith(sessionPath, "", "")
}

// connectWith is connect for a session that needs its own process: a side
// chat starts with the parent's read-mostly tools and a pool key apart from
// the parent's, so both can stay warm.
func (s *appState) connectWith(sessionPath, keySuffix, tools string, noTools ...bool) {
	if os.Getenv("PIGO_NO_AGENT") == "1" {
		s.cx.Post(func() {
			s.connected.Set(false)
			s.status.Set("测试模式：未启动 pi 子进程")
		})
		return
	}
	cwd, missing := s.sessionCwd(sessionPath)
	s.setCwd(cwd)
	openPath := sessionPath
	if missing != "" {
		// pi refuses a session whose recorded directory is gone, so open a
		// repaired copy that keeps the history but points at the fallback.
		if repaired, err := sessions.RepairCwd(sessionPath, cwd, filepath.Join(s.agentDir, "pigo", "repaired")); err == nil {
			openPath = repaired
		}
	}
	s.cx.Post(func() {
		s.connected.Set(false)
		if missing != "" {
			s.status.Set("原目录已不存在（" + missing + "），已复制会话并改在 " + cwd + " 打开")
		} else {
			s.status.Set("正在启动 pi 子进程…")
		}
	})
	opts := agent.Options{
		NodePath:    s.nodePath,
		RpcBundle:   s.bundle,
		Cwd:         cwd,
		AgentDir:    s.agentDir,
		SessionPath: openPath,
		LogPath:     filepath.Join(s.agentDir, "pigo", "agent.log"),
		NoMCP:       s.noMCP.GetUntracked(),
		ExtraArgs:   nil,
		Tools:       tools,
		NoTools:     len(noTools) > 0 && noTools[0],
	}
	if s.bundle == "" || s.nodePath == "" {
		s.cx.Post(func() { s.status.Set("未找到 node 或 rpc-entry.js，请在设置中检查") })
		return
	}
	key := s.sessionKey(openPath) + keySuffix
	// The session is already open: a second press on its row (or a switch
	// away and back) must not re-spawn, re-read or repaint the transcript.
	s.mu.Lock()
	sameOpen := s.handle != nil && s.connKey == key
	s.mu.Unlock()
	if sameOpen && s.connected.GetUntracked() {
		s.cx.Post(func() {
			s.selected.Set(sessionPath)
			s.followCwd(cwd)
			s.transcriptLoading.Set(false)
		})
		return
	}
	if keySuffix != "" {
		s.cx.Post(func() { s.status.Set("正在启动侧聊…") })
	}
	go func() {
		// Frames are bound to the handle that produced them: a warm idle
		// session in the pool must not write into another session's
		// transcript, and its idle eviction must not mark this one
		// disconnected.
		var ready atomic.Pointer[agent.SessionHandle]
		// Frames can arrive between spawn and Acquire returning. Buffer them
		// here instead of dropping the first tool/message of a cold start.
		var pendingMu sync.Mutex
		var pendingFrames []agent.Frame
		onEvent := func(f agent.Frame) {
			if h := ready.Load(); h != nil {
				s.handleFrame(h, f)
				return
			}
			pendingMu.Lock()
			pendingFrames = append(pendingFrames, f)
			pendingMu.Unlock()
		}
		onDisconnect := func(err error) {
			if h := ready.Load(); h != nil {
				s.handleDisconnect(h, err)
			}
		}
		h, err := s.pool.Acquire(key, opts, onEvent, onDisconnect)
		if err != nil {
			s.cx.Post(func() {
				if s.selected.GetUntracked() != sessionPath {
					return
				}
				s.connected.Set(false)
				s.reconnecting.Set(false)
				s.transcriptLoading.Set(false)
				s.status.Set("启动失败：" + err.Error())
			})
			return
		}
		ready.Store(h)
		pendingMu.Lock()
		buffered := pendingFrames
		pendingFrames = nil
		pendingMu.Unlock()
		s.mu.Lock()
		old, oldKey := s.handle, s.connKey
		s.handle, s.connKey = h, key
		s.mu.Unlock()
		// The previous session stays warm in the pool for a quick switch back
		// instead of being killed and respawned.
		if old != nil && oldKey != "" && oldKey != key {
			s.pool.Release(oldKey)
		}
		s.cx.Post(func() {
			// Two connects race at launch (the blank session and whichever
			// row the user pressed); only the one the window still follows
			// may write the window's state.
			if !s.isCurrent(h) {
				return
			}
			s.connected.Set(true)
			s.reconnecting.Set(false)
			s.transcriptLoading.Set(false)
			s.lastDisconnect = ""
			s.status.Set("已连接")
			s.selected.Set(sessionPath)
			s.followCwd(cwd)
			// A fork asked from the row's menu waits for this session to be
			// live: pi forks the session it has open, not a path.
			if s.forkPending == sessionPath || s.forkPending == key {
				s.forkPending = ""
				s.forkSession()
			}
		})
		for _, f := range buffered {
			s.handleFrame(h, f)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		resp, err := h.Client.Call(ctx, agent.Command{"type": agent.CmdGetState})
		if err != nil {
			s.cx.Post(func() { s.status.Set("get_state 失败：" + err.Error()) })
			return
		}
		if !s.isCurrent(h) {
			return
		}
		if path := sessionFileFromState(resp.Data); path != "" && path != key {
			s.pool.Rekey(key, path)
			s.mu.Lock()
			if s.connKey == key {
				s.connKey = path
			}
			s.mu.Unlock()
			if s.rightOpen.GetUntracked() && s.dock.GetUntracked() == "subagents" {
				s.loadSubagents()
			}
			s.cx.Post(func() {
				if openPath == sessionPath {
					s.selected.Set(path)
				} else {
					// The repaired copy stands for the row the user opened.
					s.selected.Set(sessionPath)
				}
			})
		}
		s.applyStateInfo(resp.Data)
		// The transcript first: it is what the click was for, and the settings
		// round trips and the catalog can follow behind it. They used to run
		// ahead of get_messages, which is why the row went blue and the chat
		// stayed empty for a while.
		messages, err := h.Client.Call(ctx, agent.Command{"type": agent.CmdGetMessages})
		if err == nil && messages != nil && len(messages.Data) > 0 {
			var payload struct {
				Messages []json.RawMessage `json:"messages"`
			}
			if json.Unmarshal(messages.Data, &payload) == nil {
				msgs := messagesFromRPC(payload.Messages)
				s.cx.Post(func() {
					// A cold start connects a blank session; when the user
					// opens a row before it finishes, that blank reply used to
					// land last and empty the transcript it had just loaded.
					if !s.isCurrent(h) {
						return
					}
					s.redux.SetState(msgs)
					s.transcriptLoading.Set(false)
					s.refresh()
				})
			}
		} else {
			s.cx.Post(func() {
				if s.isCurrent(h) {
					s.transcriptLoading.Set(false)
				}
			})
		}
		go s.syncSessionSettings(h)
		go s.fetchCatalog(h)
		s.fetchStats(h)
	}()
}

// sessionFileFromState reads the session file the RPC reports, the path the
// selected session really lives at after new_session or fork moved it.
func sessionFileFromState(data json.RawMessage) string {
	if len(data) == 0 {
		return ""
	}
	var state struct {
		SessionFile string `json:"sessionFile"`
	}
	if json.Unmarshal(data, &state) != nil {
		return ""
	}
	return state.SessionFile
}

// followCwd points the file dock and the next terminal at the session's own
// directory, so a session opened from the sidebar shows its project rather
// than wherever the app was launched.
func (s *appState) followCwd(cwd string) {
	if cwd == "" || cwd == s.projectRoot() {
		return
	}
	// Only the project context follows the session. setProject would call
	// connect("") and replace the session we just opened with a blank one.
	s.adoptProjectDir(cwd)
}

// isCurrent reports whether h is the session the window shows.
func (s *appState) isCurrent(h *agent.SessionHandle) bool {
	if h == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.handle == h
}

// detachSession stops the window from following the live process without
// killing it: the pool keeps it warm until its idle timeout.
func (s *appState) detachSession() {
	s.mu.Lock()
	oldKey := s.connKey
	s.handle, s.connKey = nil, ""
	s.mu.Unlock()
	if oldKey != "" {
		s.pool.Release(oldKey)
	}
	s.connected.Set(false)
}

// handleDisconnect only reacts when the handle that ended is the current
// session; an idle pool eviction or a session the user left says nothing
// about the open one. A child that died on its own is restarted, so a crash
// inside pi costs a second instead of the whole window staying offline until
// somebody presses 重连.
func (s *appState) handleDisconnect(h *agent.SessionHandle, err error) {
	if !s.isCurrent(h) {
		return
	}
	// ErrClientClosed is this window closing the client on purpose
	// (reconnect, delete, quit): nothing to repair.
	deliberate := errors.Is(err, agent.ErrClientClosed) || s.quitting
	s.cx.Post(func() {
		if !s.isCurrent(h) {
			return
		}
		s.connected.Set(false)
		s.running.Set(false)
		if deliberate {
			return
		}
		detail := "正在自动重连…"
		if err != nil && !strings.Contains(err.Error(), "EOF") {
			detail = err.Error() + " · 正在自动重连…"
			s.lastDisconnect = err.Error()
		}
		s.status.Set("会话已断开 · " + detail)
		s.notify(ui.ToastWarning, "连接中断", detail)
		s.scheduleAutoReconnect()
	})
}

// stopAutoReconnect ends the restart loop: a manual action, a session switch
// or a live connection has made it moot.
func (s *appState) stopAutoReconnect() {
	s.autoRetrying = false
	s.reconnecting.Set(false)
}

// scheduleAutoReconnect restarts the session after an unexpected exit, three
// tries 2 s apart. connect() is asynchronous, so each try waits for its
// verdict before deciding to try again; a manual 重连 or a session switch
// cancels the loop.
func (s *appState) scheduleAutoReconnect() {
	if s.autoRetrying || s.quitting {
		return
	}
	s.autoRetrying = true
	s.reconnecting.Set(true)
	path := s.selected.GetUntracked()
	attempt := 0
	var step func()
	step = func() {
		if !s.autoRetrying {
			return
		}
		attempt++
		if s.quitting || s.selected.GetUntracked() != path {
			s.stopAutoReconnect()
			return
		}
		if attempt > 1 {
			s.status.Set(fmt.Sprintf("会话已断开 · 自动重连（%d/3）…", attempt))
		}
		s.mu.Lock()
		if s.handle != nil {
			s.handle, s.connKey = nil, ""
		}
		s.mu.Unlock()
		s.connect(path)
		s.cx.AfterFunc(2500*time.Millisecond, func() {
			if !s.autoRetrying {
				return
			}
			if s.selected.GetUntracked() != path {
				s.stopAutoReconnect()
				return
			}
			if s.connected.GetUntracked() {
				s.stopAutoReconnect()
				s.notify(ui.ToastSuccess, "已重新连接", "")
				return
			}
			if attempt >= 3 {
				s.stopAutoReconnect()
				s.status.Set("会话已断开 · 自动重连失败，点「重连」恢复")
				s.notify(ui.ToastError, "自动重连失败", "点状态栏的「重连」恢复")
				return
			}
			step()
		})
	}
	step()
}

// handleFrame applies a frame only while its handle is the current session.
func (s *appState) handleFrame(h *agent.SessionHandle, f agent.Frame) {
	if !s.isCurrent(h) {
		return
	}
	if f.Type == "response" {
		s.cx.Post(func() {
			if s.isCurrent(h) {
				s.onResponse(f)
			}
		})
		return
	}
	if len(f.Raw) == 0 {
		return
	}
	b, err := json.Marshal(f.Raw)
	if err != nil {
		return
	}
	// The read loop already posted this to a dispatcher goroutine; cx.Post
	// hands it to the graph goroutine so no signal is read during render.
	s.cx.Post(func() {
		if !s.isCurrent(h) {
			return
		}
		if s.redux.Apply(b) {
			s.refresh()
			if f.Type == "message_end" {
				s.fetchStats(s.current())
			}
		}
		s.syncExtensions()
	})
}

// syncSessionSettings pushes the switches the window owns into a freshly
// connected session.
func (s *appState) syncSessionSettings(h *agent.SessionHandle) {
	if h == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = h.Client.Call(ctx, agent.Command{"type": agent.CmdSetAutoCompaction, "enabled": s.autoCompaction.GetUntracked()})
		_, _ = h.Client.Call(ctx, agent.Command{"type": agent.CmdSetAutoRetry, "enabled": s.autoRetry.GetUntracked()})
	}()
}

func (s *appState) onResponse(f agent.Frame) {
	if !f.Success && f.Error != "" {
		s.status.Set("命令 " + f.Command + " 失败：" + f.Error)
		return
	}
	switch f.Command {
	case agent.CmdGetState:
		s.status.Set("已连接 · " + s.phaseLabel())
	case agent.CmdPrompt:
		var d struct {
			Disposition string `json:"disposition"`
		}
		_ = json.Unmarshal(f.Data, &d)
		if d.Disposition != "" && d.Disposition != "started" {
			s.status.Set("prompt：" + d.Disposition)
		}
	case agent.CmdExportHTML:
		var d struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(f.Data, &d)
		if d.Path != "" {
			s.status.Set("已导出：" + d.Path)
			s.notify(ui.ToastSuccess, "已导出会话 HTML", d.Path)
		}
	}
}

func (s *appState) phaseLabel() string {
	snap := s.redux.Snapshot()
	if snap.Retry != nil {
		return "自动重试中"
	}
	if snap.Compaction != "" {
		return "压缩上下文"
	}
	return snap.Phase
}

// bootstrapNode picks the runtime the RPC child needs (PLAN §11 B′): a
// bundled node when the build carries one, else a system Node ≥ 22, else the
// official tarball downloaded on first launch into the agent directory.

func (s *appState) bootstrapNode() {
	set := func(status string, ready bool) {
		s.cx.Post(func() {
			s.nodeStatus.Set(status)
			s.nodeReady.Set(ready)
		})
	}
	if s.nodeBundled && s.nodePath != "" {
		set("内置 Node · "+filepath.Base(filepath.Dir(s.nodePath)), true)
		s.connect("")
		return
	}
	if path, version, ok := nodeinstall.SystemNodeAtLeast22(); ok {
		s.nodePath = path
		set("Node "+version, true)
		go s.refreshSettings()
		s.connect("")
		return
	}
	set("未检测到 Node ≥ 22，正在下载…", false)
	go func() {
		dir := filepath.Join(s.agentDir, "pigo", "node")
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		path, err := nodeinstall.Ensure(ctx, dir, s.onNodeProgress)
		if err != nil {
			set("Node 下载失败："+err.Error(), false)
			s.cx.Post(func() { s.status.Set("无法启动子进程：请安装 Node ≥ 22 后点「重连」") })
			return
		}
		s.nodePath = path
		set("已下载 Node", true)
		go s.refreshSettings()
		s.connect("")
	}()
}

func (s *appState) onNodeProgress(p nodeinstall.Progress) {
	s.cx.Post(func() {
		detail := strings.TrimSpace(p.Stage)
		if p.Detail != "" {
			detail += " · " + p.Detail
		}
		if p.Percent > 0 {
			detail += fmt.Sprintf(" %d%%", p.Percent)
		}
		s.nodeStatus.Set(strings.TrimPrefix(detail, " · "))
	})
}

// openDock shows the right panel on one of its tabs, starting the tab's
// resource only when it is first shown.
func (s *appState) openDock(tab string) {
	if tab == "" {
		return
	}
	s.dock.Set(tab)
	s.rightOpen.Set(true)
	// A tool tab stays in the strip once opened; a file tab is in fileTabs
	// already, so the strip derives it.
	switch tab {
	case "files", "review", "terminal", "subagents":
		open := s.dockOpenKinds.GetUntracked()
		if !slices.Contains(open, tab) {
			s.dockOpenKinds.Set(append(append([]string(nil), open...), tab))
		}
	}
	switch tab {
	case "files":
		s.dirs.mu.Lock()
		ready := s.dirs.asked[s.projectRoot()]
		s.dirs.mu.Unlock()
		if !ready {
			s.reloadTree()
		}
	case "review":
		s.loadGit()
		s.loadGitState(s.projectRoot())
	case "terminal":
		s.ensureTerminal()
		s.cx.AfterFunc(80*time.Millisecond, func() {
			if tab := s.activeTab(); tab != nil {
				tab.Focus()
			}
		})
	case "subagents":
		s.loadSubagents()
	default:
		if path, ok := strings.CutPrefix(tab, "file:"); ok {
			s.selectedFile.Set(path)
			s.loadDiff(path)
		}
	}
}

// refresh coalesces streaming deltas to one transcript rebuild per frame:
// every message_update used to rebuild every visible turn, which made long
// answers crawl. Outside a run it is immediate, so optimistic messages show
// at once.
func (s *appState) refresh() {
	if !s.running.GetUntracked() {
		s.refreshNow()
		return
	}
	if s.refreshPending {
		return
	}
	s.refreshPending = true
	s.cx.AfterFunc(32*time.Millisecond, func() {
		s.refreshPending = false
		s.refreshNow()
	})
}

func (s *appState) refreshNow() {
	snap := s.redux.Snapshot()
	all := buildTurns(snap)
	if n := len(all); n > 0 {
		last := &all[n-1]
		last.Model = s.modelName.GetUntracked()
		if snap.Running && !s.runStart.IsZero() {
			last.StartedAt = s.runStart
			last.Elapsed = time.Since(s.runStart)
		} else if !snap.Running {
			if done := s.lastElapsedBySession[s.selected.GetUntracked()]; done > 0 {
				last.Elapsed = done
			}
		}
	}
	// The context window's fill is the newest request's prompt: that one
	// message's new input plus its cache reads. A turn's sum counts every
	// request it made, which read far past the window.
	for i := len(snap.Messages) - 1; i >= 0; i-- {
		m := snap.Messages[i]
		if m.Role != "assistant" || m.Usage == (reducer.Usage{}) {
			continue
		}
		if used := m.Usage.In + m.Usage.Cache; used > 0 {
			s.ctxUsed.Set(used)
		}
		break
	}
	s.turnTotal.Set(len(all))
	if limit := s.turnLimit.GetUntracked(); limit > 0 && len(all) > limit {
		s.turns.Set(all[len(all)-limit:])
	} else {
		s.turns.Set(all)
	}
	s.running.Set(snap.Running)
	var banner []string
	if snap.Retry != nil {
		banner = append(banner, fmt.Sprintf("自动重试 #%d：%s", snap.Retry.Attempt, snap.Retry.Message))
	}
	if snap.Compaction != "" {
		banner = append(banner, "上下文压缩："+snap.Compaction)
	}
	s.banner.Set(strings.Join(banner, " · "))
	s.queues.Set(fmt.Sprintf("steering %d · followUp %d", len(snap.Steering), len(snap.FollowUp)))
	s.steeringItems.Set(snap.Steering)
	s.followUpItems.Set(snap.FollowUp)
	s.widgetText.Set(strings.Join(snap.WidgetLines, " · "))
	if snap.BashOutput != "" {
		s.bashOutput.Set(snap.BashOutput)
	}
	parts := []string{}
	if s.connected.GetUntracked() {
		parts = append(parts, "已连接")
	} else {
		parts = append(parts, "未连接")
	}
	if snap.Phase != "" && snap.Phase != "idle" {
		parts = append(parts, snap.Phase)
	}
	if snap.Retry != nil {
		parts = append(parts, fmt.Sprintf("重试 %d", snap.Retry.Attempt))
	}
	if snap.Compaction != "" {
		parts = append(parts, "压缩中")
	}
	if snap.StatusText != "" {
		parts = append(parts, snap.StatusText)
	}
	if snap.Title != "" {
		parts = append(parts, snap.Title)
	}
	s.status.Set(strings.Join(parts, " · "))
	if s.wasRunning && !snap.Running && s.connected.GetUntracked() {
		s.notifyTurnDone(snap)
	}
	if s.wasRunning != snap.Running {
		s.updateWidgets()
	}
	// Track the run's wall-clock length for the working label and the
	// status capsule.
	if snap.Running && s.runStart.IsZero() {
		s.runStart = time.Now()
		s.runSessionID = s.selected.GetUntracked()
		s.tickRun()
	} else if !snap.Running && !s.runStart.IsZero() {
		elapsed := time.Since(s.runStart)
		if key := s.runSessionID; key != "" {
			s.lastElapsedBySession[key] = elapsed
		}
		s.runSessionID = ""
		s.runStart = time.Time{}
		s.runSeconds.Set(0)
		s.turns.Update(func(list []turnRow) []turnRow {
			if len(list) == 0 {
				return list
			}
			out := append([]turnRow(nil), list...)
			out[len(out)-1].Elapsed = elapsed
			return out
		})
	}
	s.wasRunning = snap.Running
}

// tickRun advances the run-seconds signal once a second while a run lives.
func (s *appState) tickRun() {
	s.cx.AfterFunc(time.Second, func() {
		if s.runStart.IsZero() {
			return
		}
		s.runSeconds.Set(int(time.Since(s.runStart).Seconds()))
		s.tickRun()
	})
}

// runElapsedLabel is "12s" / "1m 12s" of the current run, empty while idle.
// It reads the seconds signal so the working row re-renders every tick.
func (s *appState) runElapsedLabel() string {
	if s.runStart.IsZero() {
		return ""
	}
	return durationLabel(time.Duration(s.runSeconds.Get()) * time.Second)
}

// durationLabel prints a duration compactly: 42s, 1m 12s, 2h 05m.
func durationLabel(d time.Duration) string {
	d = d.Truncate(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
}

// buildTurns folds the flat message list into requests and their
// responses: a user message opens a turn, the assistant's prose becomes its
// steps, its tool calls become one step of activity cards fed by the
// toolResult messages that follow, and its last prose becomes the final
// answer once the run ends.
//
// Running marks ONLY the newest turn. Earlier turns used to inherit the
// live flag too, which made every old turn read "正在处理…" and forced its
// steps open while anything ran.
func buildTurns(snap reducer.Snapshot) []turnRow {
	var out []turnRow
	var cur *turnRow
	type result struct {
		text  string
		isErr bool
	}
	results := make(map[string]result)
	for _, m := range snap.Messages {
		if m.Role == "toolResult" && m.ToolCallID != "" {
			results[m.ToolCallID] = result{text: m.Text, isErr: m.IsError}
		}
	}
	flush := func() {
		if cur == nil {
			return
		}
		out = append(out, *cur)
		cur = nil
	}
	for _, m := range snap.Messages {
		switch m.Role {
		case "user":
			flush()
			cur = &turnRow{ID: m.ID, EntryID: m.EntryID, Prompt: m.Text}
		case "assistant":
			if cur == nil {
				cur = &turnRow{ID: m.ID, EntryID: m.EntryID}
			}
			var calls []toolRow
			for _, c := range m.Calls {
				action, detail := toolfields.Format(c.Name, c.Args)
				res, hasResult := results[c.ID]
				row := toolRow{
					ID: c.ID, Name: c.Name, Action: action, Detail: detail,
					Status: "completed", Partial: res.text, IsError: res.isErr,
					Args: c.Args,
				}
				// A call with no result never finished: a cancelled run or a
				// history cut. Painting it completed was the reason a tool
				// that failed yesterday opened green today.
				if !hasResult {
					row.Status = "cancelled"
				}
				if row.IsError {
					row.Status = "failed"
				}
				applyToolDiff(&row)
				calls = append(calls, row)
				cur.Tools = append(cur.Tools, row)
			}
			if len(calls) > 0 {
				cur.Blocks = append(cur.Blocks, turnBlock{Kind: "tools", Tools: calls})
			}
			// The turn's own cost, summed from its messages: pi reports the
			// new input tokens, the output and the cache reads per message,
			// so the footer never borrows the session's running totals.
			cur.Usage.In += m.Usage.In
			cur.Usage.Out += m.Usage.Out
			cur.Usage.Cache += m.Usage.Cache
			if t := strings.TrimSpace(m.Thinking); t != "" {
				cur.Thinks = append(cur.Thinks, t)
				cur.Blocks = append(cur.Blocks, turnBlock{Kind: "think", Text: t})
			}
			if strings.TrimSpace(m.Text) != "" {
				cur.Steps = append(cur.Steps, m.Text)
				cur.Blocks = append(cur.Blocks, turnBlock{Kind: "say", Text: m.Text})
			}
		}
	}
	flush()
	// The last prose of every settled turn becomes its final answer; the
	// live turn keeps its prose in Steps so the caret can stream there.
	for i := range out {
		if i == len(out)-1 && snap.Running {
			continue
		}
		t := &out[i]
		if n := len(t.Steps); n > 0 && t.Final == "" {
			t.Final = t.Steps[n-1]
			t.Steps = t.Steps[:n-1]
			// The answer renders through Final, so its say block would
			// otherwise draw the same text a second time.
			for j := len(t.Blocks) - 1; j >= 0; j-- {
				if t.Blocks[j].Kind == "say" {
					t.Blocks = append(t.Blocks[:j], t.Blocks[j+1:]...)
					break
				}
			}
		}
	}
	// Live tool calls and the run's flags belong to the newest turn only.
	if n := len(out); n > 0 {
		calls := toolRowsFrom(snap.Tools)
		if len(calls) > 0 {
			// Only the calls the history has not placed yet belong to this
			// turn: the reducer keeps a session-wide list, and merging all of
			// it made the last turn claim every file the session ever wrote.
			merged := append([]toolRow(nil), out[n-1].Tools...)
			seen := make(map[string]bool, len(merged))
			for _, c := range merged {
				seen[c.ID] = true
			}
			for _, c := range calls {
				if seen[c.ID] {
					// A live status may be newer than the replayed one.
					for i := range merged {
						if merged[i].ID == c.ID {
							merged[i].Status, merged[i].IsError = c.Status, c.IsError
							merged[i].Partial = c.Partial
						}
					}
					continue
				}
				merged = append(merged, c)
				seen[c.ID] = true
			}
			// Only calls the history has not shown yet become a live block,
			// in the order the calls came.
			var live []toolRow
			shown := map[string]bool{}
			for _, b := range out[n-1].Blocks {
				for _, t := range b.Tools {
					shown[t.ID] = true
				}
			}
			for _, c := range calls {
				if !shown[c.ID] {
					live = append(live, c)
				}
			}
			if len(live) > 0 {
				out[n-1].Blocks = append(out[n-1].Blocks, turnBlock{Kind: "tools", Tools: live})
			}
			out[n-1].Tools = merged
		}
		last := &out[n-1]
		last.Running = snap.Running
		last.Failed = snap.Retry != nil
		if snap.LastError != "" && !snap.Running && last.Final == "" {
			last.Error = snap.LastError
			last.Failed = true
		}
	}
	return out
}

// applyToolDiff turns an edit/write call's arguments into the patch its
// card renders, and records the +/− counts the summary row shows.
func applyToolDiff(row *toolRow) {
	patch, ok := toolfields.Patch(row.Name, row.Args)
	if !ok {
		return
	}
	row.Diff = patch
	row.Add, row.Del = textdiff.Stats(patch)
}

func toolRowsFrom(calls []reducer.ToolCall) []toolRow {
	out := make([]toolRow, 0, len(calls))
	for _, c := range calls {
		action, detail := toolfields.Format(c.Name, c.Args)
		if c.Action != "" {
			action = c.Action
		}
		if c.Detail != "" {
			detail = c.Detail
		}
		row := toolRow{
			ID: c.ID, Name: c.Name, Action: action, Detail: detail,
			Status: c.Status, Partial: c.Partial, IsError: c.IsError,
			Args: c.Args,
		}
		if len(c.Nested) > 0 {
			row.Nested = make([]nestedToolRow, len(c.Nested))
			for i, n := range c.Nested {
				row.Nested[i] = nestedToolRow{
					ID: n.ID, Action: n.Action, Detail: n.Detail,
					Status: n.Status, IsError: n.IsError,
				}
			}
		}
		applyToolDiff(&row)
		out = append(out, row)
	}
	return out
}

// messagesFromRPC converts get_messages data into reducer messages. A
// message's content is either a plain string or an array of blocks: "text"
// carries prose, "thinking" the reasoning, "toolCall" a call whose result
// arrives as its own toolResult message. The entry id and a toolResult's
// error flag survive the round trip: fork needs real ids, and a failed
// historical tool card must stay red.
func messagesFromRPC(raw []json.RawMessage) []reducer.Message {
	var out []reducer.Message
	for i, r := range raw {
		var m struct {
			Role       string          `json:"role"`
			Content    json.RawMessage `json:"content"`
			ToolName   string          `json:"toolName"`
			ToolCallID string          `json:"toolCallId"`
			IsError    bool            `json:"isError"`
			ID         string          `json:"id"`
			EntryID    string          `json:"entryId"`
			Usage      struct {
				Input, Output, CacheRead, CacheWrite int
			} `json:"usage"`
			Message struct {
				Usage struct {
					Input, Output, CacheRead, CacheWrite int
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(r, &m) != nil {
			continue
		}
		id := m.ID
		if id == "" {
			id = m.EntryID
		}
		if id == "" {
			id = fmt.Sprintf("history-%d", i)
		}
		if m.Role == "user" && (strings.HasPrefix(strings.TrimSpace(contentText(m.Content)), "[侧聊]") ||
			strings.HasPrefix(strings.TrimSpace(contentText(m.Content)), "[Side chat]")) {
			continue
		}
		msg := reducer.Message{
			ID:         id,
			EntryID:    m.EntryID,
			Role:       m.Role,
			Text:       contentText(m.Content),
			Thinking:   contentThinking(m.Content),
			ToolName:   m.ToolName,
			ToolCallID: m.ToolCallID,
			IsError:    m.IsError,
			Calls:      contentCalls(m.Content),
			Usage: reducer.Usage{
				In:    pickUsage(m.Usage.Input, m.Message.Usage.Input),
				Out:   pickUsage(m.Usage.Output, m.Message.Usage.Output),
				Cache: pickUsage(m.Usage.CacheRead, m.Message.Usage.CacheRead) + pickUsage(m.Usage.CacheWrite, m.Message.Usage.CacheWrite),
			},
		}
		out = append(out, msg)
	}
	return out
}

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func blocks(raw json.RawMessage) []contentBlock {
	if len(raw) == 0 {
		return nil
	}
	var list []contentBlock
	if json.Unmarshal(raw, &list) != nil {
		return nil
	}
	return list
}

func contentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var b strings.Builder
	for _, block := range blocks(raw) {
		if block.Type == "text" {
			b.WriteString(block.Text)
		}
	}
	return b.String()
}

func contentThinking(raw json.RawMessage) string {
	var b strings.Builder
	for _, block := range blocks(raw) {
		if block.Type == "thinking" {
			b.WriteString(block.Thinking)
		}
	}
	return b.String()
}

// contentCalls reads the tool calls of an assistant message. Arguments stay
// as raw JSON so toolfields.Format can read whichever keys the tool uses.
func contentCalls(raw json.RawMessage) []reducer.Call {
	var out []reducer.Call
	for _, block := range blocks(raw) {
		if block.Type != "toolCall" {
			continue
		}
		args := map[string]any{}
		if len(block.Arguments) > 0 {
			_ = json.Unmarshal(block.Arguments, &args)
		}
		out = append(out, reducer.Call{ID: block.ID, Name: block.Name, Args: args})
	}
	return out
}

// call sends a command without blocking the UI. prompt only waits for its
// preflight response; progress arrives as events.
func (s *appState) call(cmd agent.Command) {
	h := s.current()
	if h == nil {
		s.status.Set("未连接：先点「连接」")
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		resp, err := h.Client.Call(ctx, cmd)
		if err != nil {
			s.cx.Post(func() { s.status.Set("命令失败：" + err.Error()) })
			return
		}
		if resp != nil {
			s.cx.Post(func() { s.onResponse(*resp) })
		}
	}()
}

// callPrompt is call for a user prompt: when the preflight fails before pi
// acknowledges the message, the optimistic bubble is rolled back and the
// text/attachments return to the composer. A possibly-delivered prompt keeps
// its bubble and only gets an "unconfirmed" notice; it is never resent.
func (s *appState) callPrompt(cmd agent.Command, text string, atts []imageAttachment) {
	h := s.current()
	if h == nil {
		s.status.Set("未连接：先点「连接」")
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		resp, err := h.Client.Call(ctx, cmd)
		if err != nil {
			s.cx.Post(func() {
				if s.redux.RollbackSend() {
					s.draft.Set(text)
					if len(atts) > 0 {
						s.attachments.Set(atts)
					}
					s.refresh()
					s.notify(ui.ToastError, "未发送", err.Error())
				} else {
					s.notify(ui.ToastWarning, "未确认", "连接中断，消息可能已送达")
				}
			})
			return
		}
		if resp != nil {
			s.cx.Post(func() { s.onResponse(*resp) })
		}
	}()
}

func (s *appState) fire(cmd agent.Command) {
	h := s.current()
	if h == nil {
		return
	}
	if err := h.Client.Send(cmd); err != nil {
		s.cx.Post(func() { s.status.Set("发送失败：" + err.Error()) })
	}
}

func (s *appState) send(text string) {
	text = strings.TrimSpace(text)
	if s.paletteHandled {
		s.paletteHandled = false
		return
	}
	if text == "" {
		return
	}
	// Never insert an optimistic bubble without a live process: a ghost
	// question the transcript shows but pi never received was worse than
	// a visible "not connected" hint.
	if s.current() == nil || !s.connected.GetUntracked() {
		// A drop is usually over in a second: say so and restart, keeping the
		// draft where the user left it, instead of a dead end.
		if s.autoRetrying {
			s.notify(ui.ToastInfo, "正在重连", "连上后按发送即可")
			return
		}
		if s.selected.GetUntracked() != "" {
			s.notify(ui.ToastWarning, "连接已断开", "正在重连，连上后按发送即可")
			s.reconnect()
			return
		}
		s.notify(ui.ToastWarning, "未连接", "先连接会话或点「重连」再发送")
		return
	}
	if !s.ensureTrust() {
		return
	}
	if command, ok := bashCommandOf(text); ok {
		s.bashCommand.Set(command)
		s.bashOutput.Set("")
		s.draft.Set("")
		s.call(agent.Command{"type": agent.CmdBash, "command": command})
		return
	}
	s.rememberPrompt(text)
	s.redux.OptimisticUser(text)
	s.refresh()
	s.draft.Set("")
	cmd := agent.Command{"type": agent.CmdPrompt, "message": text}
	atts := s.attachments.GetUntracked()
	if images := s.attachmentBlocks(); len(images) > 0 {
		cmd["images"] = images
		s.attachments.Set(nil)
	}
	s.callPrompt(cmd, text, atts)
}

// saveAttachmentsFor keeps the composer's pictures with the session they
// were added to, as drafts are kept.
func (s *appState) saveAttachmentsFor(sessionID string) {
	if sessionID == "" {
		return
	}
	s.attachmentsBySession[sessionID] = append([]imageAttachment{}, s.attachments.GetUntracked()...)
}

// restoreAttachmentsFor loads the pictures that session had.
func (s *appState) restoreAttachmentsFor(sessionID string) {
	s.attachments.Set(append([]imageAttachment{}, s.attachmentsBySession[sessionID]...))
}

// currentSessionPath is the open session's file, empty before one opens.
func (s *appState) currentSessionPath() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.connKey
}

// rememberPrompt appends a sent prompt to the composer's ↑/↓ history.
func (s *appState) rememberPrompt(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	list := s.promptHistory.GetUntracked()
	if len(list) > 0 && list[len(list)-1] == text {
		s.historyAt.Set(-1)
		return
	}
	next := append(append([]string{}, list...), text)
	if len(next) > 100 {
		next = next[len(next)-100:]
	}
	s.promptHistory.Set(next)
	s.historyAt.Set(-1)
}

// attachmentBlocks encodes the composer's pictures the way pi's prompt
// command takes them.
func (s *appState) attachmentBlocks() []map[string]any {
	attachments := s.attachments.GetUntracked()
	out := make([]map[string]any, 0, len(attachments))
	for _, a := range attachments {
		mime := a.MIME
		if mime == "" {
			mime = "image/png"
		}
		out = append(out, map[string]any{"type": "image", "data": base64.StdEncoding.EncodeToString(a.Data), "mimeType": mime})
	}
	return out
}

func (s *appState) stop() {
	s.fire(agent.Command{"type": agent.CmdAbort})
	s.running.Set(false)
}

func (s *appState) reconnect() {
	// 重连 is a restart, so the live handle goes first: connectWith's
	// "already open" fast path reads s.handle/s.connKey, and waiting for the
	// disconnect callback to land left the press with nothing to do (the
	// window stayed offline and the next send failed).
	s.stopAutoReconnect()
	// First launch with no usable Node: there is no child to restart, so 重连
	// has to mean "try the runtime again" — otherwise a download that failed
	// once (offline airport wifi) leaves the window dead to every press.
	if s.nodePath == "" {
		go s.bootstrapNode()
		return
	}
	s.mu.Lock()
	h := s.handle
	s.handle, s.connKey = nil, ""
	s.mu.Unlock()
	s.connected.Set(false)
	s.running.Set(false)
	if h != nil {
		_ = h.Client.Close()
	}
	path := s.selected.GetUntracked()
	mode := s.toolMode.GetUntracked()
	tools := ""
	none := false
	switch mode {
	case "read":
		tools = "read,grep,glob,ls"
	case "none":
		none = true
		mode = "none"
	default:
		mode = "default"
	}
	if mode == "default" {
		s.connect(path)
		return
	}
	s.connectWith(path, "|mode:"+mode, tools, none)
}

// bashCommandOf reads the composer's `!command` shell mode; `!!` stays a
// plain prompt.
func bashCommandOf(text string) (string, bool) {
	if !strings.HasPrefix(text, "!") || strings.HasPrefix(text, "!!") {
		return "", false
	}
	command := strings.TrimSpace(text[1:])
	return command, command != ""
}

// pickUsage takes the first positive of a message's two usage shapes.
func pickUsage(a, b int) int {
	if a > 0 {
		return a
	}
	return b
}
