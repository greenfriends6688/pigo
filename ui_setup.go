package main

import (
	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/icons/lucide"
	"github.com/oneo-me/nsgo/ui"
	"pigo/internal/agent"
)

// The setup screen: what replaces the conversation while there is no runtime
// to drive. It exists for one reason — a window that cannot start pi has to
// say what to install and offer the next step, instead of showing an empty
// chat whose composer refuses every message.

const (
	piInstallCommand = "npm install -g @earendil-works/pi-coding-agent"
	piDocsURL        = "https://github.com/earendil-works/pi"
)

// setupCopy is the words for one state: what is wrong, what to do, and which
// command (if any) does it.
func setupCopy(state string) (title, text, command, commandLabel string) {
	switch state {
	case piSetupMissing:
		return "先装一个 pi",
			"pigo 是 pi 编码 agent 的窗口，本身不含 agent。装一次 pi，就跟终端里用的是同一套模型、扩展和会话。",
			piInstallCommand, "安装命令（需要 Node ≥ 22）"
	case piSetupOld:
		return "pi 版本太旧",
			"这个窗口需要 pi " + versionFloor() + " 或更新的 1.x。先更新 pi，再点下面的「重新检测」。",
			"pi update", "更新命令"
	case piSetupNew:
		return "pi 比这个窗口新",
			"检测到的 pi 是 2.x 以上，RPC 可能已经变了，这个版本没有验证过。可以改用内置 pi 继续，或装回 1.x。",
			"", ""
	case piSetupUnreadable:
		return "读不出 pi 的版本",
			"找到了 pi，但 `pi --version` 没有回话。多半是它找不到 node：把 node 放到 pi 同目录，或设置 PI_EXECUTABLE 指向可用的 pi。",
			"", ""
	}
	return "", "", "", ""
}

func versionFloor() string {
	return "1." + itoa(agent.MinPiMinor)
}

// piSetupCard renders that screen.
func (s *appState) piSetupCard() core.Node {
	colors := ui.ColorsOf(s.cx)
	state := s.piSetup.Get()
	title, text, command, commandLabel := setupCopy(state)

	children := []core.Node{
		ui.Icon(lucide.SquareTerminal, 26, colors.Accent),
		core.Text{Content: title, Style: core.Style{FontSize: 17, FontWeight: core.FontWeightSemibold, Color: colors.Text()}},
		core.Text{Content: text, MaxLines: 4,
			Style: core.Style{FontSize: 12.5, LineHeight: 19, TextAlign: core.TextAlignCenter, Color: colors.Muted()}},
	}

	if command != "" {
		children = append(children, core.View{
			Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8,
				Padding: core.Symmetric(7, 10), CornerRadius: 9, BorderWidth: 1,
				Background: ui.Alpha(colors.Text(), 0.04)},
			StyleFunc: func() core.Style { return core.Style{BorderColor: colors.Border()} },
			Children: []core.Node{
				core.Text{Content: commandLabel, MaxLines: 1,
					Style: core.Style{FontSize: 10.5, Color: colors.Muted()}},
				core.Text{Content: command, Selectable: core.SelectableOn, MaxLines: 1,
					Style: core.Style{FontSize: 12, FontFamily: core.FontMonospace, Color: colors.Text()}},
				ui.Button{Text: "复制", Variant: ui.ButtonGhost, Size: ui.SizeSmall, TestID: "setup-copy",
					OnPress: func() {
						s.cx.SetClipboard(command)
						s.notify(ui.ToastInfo, "已复制命令", command)
					}},
			},
		})
	}

	actions := []core.Node{
		ui.Button{Text: "重新检测", Variant: ui.ButtonPrimary, Size: ui.SizeSmall,
			TestID: "setup-recheck", OnPress: s.recheckRuntime},
		ui.Button{Text: "用内置 pi 继续", Variant: ui.ButtonSecondary, Size: ui.SizeSmall,
			TestID: "setup-bundled", OnPress: s.useBundledRuntime},
		ui.Button{Text: "打开 pi 文档", Variant: ui.ButtonGhost, Size: ui.SizeSmall,
			TestID: "setup-docs", OnPress: func() { s.openExternal(piDocsURL, "text/plain") }},
	}
	if state == piSetupNew {
		actions = actions[1:]
	}
	children = append(children, core.View{
		Style:    core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8},
		Children: actions,
	})

	found := ""
	if s.piPath != "" {
		found = s.piPath
	} else if p := agent.DiscoverPi(); p != "" {
		found = p
	}
	if found != "" {
		children = append(children, core.Text{Content: "检测到的 pi：" + found, MaxLines: 1,
			Style: core.Style{FontSize: 10.5, Color: colors.Muted()}})
	}
	children = append(children, core.Text{
		Content:  "装在别处？把 PI_EXECUTABLE 指向它再重开窗口。",
		MaxLines: 1, Style: core.Style{FontSize: 10.5, Color: colors.Muted()}})

	return core.View{
		TestID: "pi-setup",
		Style: core.Style{AlignItems: core.AlignCenter, Gap: 10, Padding: core.Symmetric(28, 22),
			CornerRadius: 14, BorderWidth: 1, MaxWidth: core.Pt(520), AlignSelf: core.AlignCenter,
			Background: ui.Alpha(colors.Text(), 0.03)},
		StyleFunc: func() core.Style { return core.Style{BorderColor: colors.Border()} },
		Children:  children,
	}
}
