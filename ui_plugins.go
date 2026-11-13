package main

import (
	"context"
	"strings"
	"time"

	"github.com/oneo-me/nsgo/base"
	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/ui"
)

// pluginDialog installs or removes one pi package through pi's own command,
// so settings.json stays the one writer. pi-web's Plugins section does the
// same through its package API; the CLI is the desktop's equivalent.
func (s *appState) pluginDialog() core.Node {
	return ui.Dialog{
		Open:         s.pluginOpen.Get,
		OnOpenChange: func(open bool, _ base.Reason) { s.pluginOpen.Set(open) },
		Title:        "插件",
		Description:  "通过 pi install / pi remove 写入 settings.json 的 packages。",
		Style:        core.Style{Width: core.Pt(480)},
		Children: []core.Node{
			ui.Input{Value: s.pluginSource.Get, OnChange: s.pluginSource.Set,
				Placeholder: "npm:包名 或 GitHub 地址", Label: "包地址", TestID: "plugin-source",
				Style: core.Style{Width: core.Pct(100)}},
		},
		Footer: []core.Node{
			ui.Button{Text: "取消", TestID: "plugin-cancel", OnPress: func() { s.pluginOpen.Set(false) }},
			ui.Button{Text: "安装", Variant: ui.ButtonPrimary, TestID: "plugin-install", OnPress: func() { s.runPlugin(false) }},
			ui.Button{Text: "移除", Variant: ui.ButtonGhost, TestID: "plugin-remove", OnPress: func() { s.runPlugin(true) }},
		},
		TestID: "plugin-dialog",
	}
}

// openPluginDialog shows the package form for install or removal.
func (s *appState) openPluginDialog(remove bool) {
	s.pluginRemove.Set(remove)
	s.pluginOpen.Set(true)
}

// runPlugin runs pi install or pi remove for the source, then rereads
// settings so the package list updates.
func (s *appState) runPlugin(remove bool) {
	source := strings.TrimSpace(s.pluginSource.GetUntracked())
	if source == "" {
		s.notify(ui.ToastWarning, "需要包地址", "")
		return
	}
	s.pluginOpen.Set(false)
	verb := "install"
	if remove {
		verb = "remove"
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		out, err := s.cfg.RunPi(ctx, verb, source)
		s.cx.Post(func() {
			if err != nil {
				s.notify(ui.ToastError, "插件操作失败", err.Error())
				return
			}
			if text := strings.TrimSpace(string(out)); text != "" {
				s.notify(ui.ToastSuccess, "已完成", firstLine(text, 120))
			} else {
				s.notify(ui.ToastSuccess, "已完成", source)
			}
			s.pluginSource.Set("")
			go s.refreshSettings()
		})
	}()
}
