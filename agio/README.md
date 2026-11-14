# pigo's copy of nsgo-agio's design components

These packages are copied from `nsgo-agio-main` (the `oneo-me/nsgo-agio`
working tree) so native keeps its local NSGo checkout and its own pi engine.
They are not a module dependency: the two projects use different NSGo module
paths, and mixing them would mix `core.Node` types.

## What is here

| Path | Source | Changes |
|---|---|---|
| `agio.go` | `nsgo-agio-main/agio.go` | imports rewritten to `pigo/agio/...` |
| `agent/agent.go` | `nsgo-agio-main/agent/agent.go` | only the types and helpers; no drivers |
| `transcript/` | `nsgo-agio-main/transcript/` | none |
| `quota/` | `nsgo-agio-main/quota/` | none |
| `chatview/` | `nsgo-agio-main/chatview/` | imports to `oneo-me/nsgo`; `AppVersion` instead of `core.App`; `For()` without `LocaleSettings`; added embedding hooks (see below) |
| `chatview/locales/` | `nsgo-agio-main/chatview/locales/` | `For()` without `LocaleSettings`; `catalog_gen.go` renamed `messages_gen.go` so `go tool nsgo check` does not look for YAML sources |

## Embedding hooks added to chatview

- `App`: `Sidebar`, `Chat`, `NewTask`, `NewTaskChat`, `NewSession`, `Title`,
  `Right`, `RightHeader`, `RightOpen`, `RightWidth`, `RightDefaultWidth`,
  `SidebarFooter`, `SidebarActions`, `SettingsOpen`.
- `Conversation`: `Turn`, `Composer`, `Overlay`, `TurnLimit`, `State`,
  `OnSend` exported.

The app uses them in `agio_workbench.go` and `agio_bridge.go`. `agio_bridge.go` implements
`agio.Backend` over native's existing `appState`, so the pi engine stays
native's; the components only see `State`.

## Framework patch

The local `nsgo-main` gained Agio's inset shell API, copied from `nsgo-dev`:

- `ui/sidebarshell.go`: `SidebarShellVariant`, `SidebarShellPlain`,
  `SidebarShellInset`, `Variant`, `ChromeStyle`, `Backdrop`, the inset layout,
  `insetCard` and `insetControls`.
- `internal/driver/services.go`: `WindowControls.Offset`.
- `internal/platform/macos/desktop.go`: the traffic lights follow `Offset`,
  so they slide with the inset shell's margin.

## Syncing with upstream

```sh
diff -ru ../nsgo-agio-main/chatview agio/chatview
diff -u  ../nsgo-agio-main/agio.go agio/agio.go
```

Bring back only bug fixes and API changes the app uses; keep the embedding
hooks and the local changes listed above. Update this file when the copy
changes.
