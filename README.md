# pigo

把 [pi](https://github.com/earendil-works/pi) 这个命令行编码 agent 装进一个真正的桌面窗口：
会话、对话流、文件树、Git 审阅、终端、子代理、MCP 全在一处，不用开浏览器，也不用记 CLI 参数。

Go + [NSGo](https://github.com/oneo-me/nsgo) 写成，单进程壳子 + 一个 `pi --mode rpc` 子进程，
两者用 JSONL 走 stdin/stdout 通信。

**优先用你自己装的 pi**（`PI_EXECUTABLE` → `PATH` → 登录 shell），跑的就是终端里那套模型、扩展、
技能、会话和信任记录。没装 pi 时窗口给一张安装卡片（命令可复制、「重新检测」），也可以改用随包
内置的 pi RPC bundle：那一路才需要 Node ≥ 22，没有就在首次启动时自动下载官方 tarball。

---

## 界面组件

窗口壳（inset 悬浮卡片、渐变窗框、玻璃 composer）、左侧栏骨架、设置对话框、模型切换、思考强度、上下文环和配色/颗粒，来自 [nsgo-agio](https://github.com/oneo-me/nsgo-agio) 的设计组件，复制在 `agio/`。它们只通过 `agio_bridge.go` 实现的 `agio.Backend` 读取窗口状态；pi 引擎、会话库、文件/Git/终端和全部设置页仍然是 pigo 自己的，功能没有被替换掉。

## 功能

**会话**
- 多项目、多会话列表，会话按目录分组，支持搜索
- 新建 / 重命名 / 克隆 / Fork（从任意一条消息分叉）/ 侧聊（只读工具的支线对话）
- 会话导出为单文件 HTML
- 上下文压缩（compact），steering / followUp 队列可视、可撤回

**对话**
- 流式输出、工具调用的折叠卡片（命令、diff、文件、搜索结果各有自己的渲染）
- `@` 引用文件、`/` 打开命令、`⌘K` 命令面板、`!command` 直接跑 shell
- 运行时长、token 用量、缓存命中率、模型与思考档位实时显示

**右侧面板**
- 文件树（改动标记、隐藏文件开关）
- Git 审阅：未提交 diff、逐文件统计、提交历史
- 终端（多标签，跟随项目目录）
- 子代理与工具输出检视

**连接**
- 子进程崩溃自动重连（三次退避），也可手动「重连」
- 断连时明确说明原因，并提供子进程日志入口

**平台**
- macOS / Windows / Linux，Apple silicon 与 Intel、arm64 与 x64 都有对应构建目标（见下文）
- 明暗外观、系统通知、菜单与快捷键

## 运行要求

| | 要求 |
|---|---|
| 运行（首选） | 已安装 **pi 1.4+**（`npm i -g @earendil-works/pi-coding-agent`）；版本读不出来或主版本不符会在窗口里说明 |
| 运行（备选） | 不装 pi 时用内置 RPC bundle：需要系统 Node ≥ 22，没有则自动下载到 `~/.pi/agent/pigo/node/` |
| macOS 权限 | 项目放在「桌面 / 文稿 / 下载」时，系统会要求授权（TCC）。没授权时子进程会**卡住**而不是报错，窗口会直接告诉你点哪里 |
| 构建 | Go 1.27+、NSGo（作为 Go tool 引入，见 `go.mod` 的 `tool` 指令） |
| 打包 Windows 安装器 | `makensis`（`brew install makensis`；没有时只产出免安装 exe） |

## 快速开始

```sh
# 开发窗口（热重载，不打包）
go tool nsgo dev

# 校验 + 测试 + 打本机平台的安装包
sh scripts/build.sh

# 产物
open native/dist            # .app / dmg / setup.exe / .deb / .AppImage
```

`scripts/build.sh` 会依次做：`nsgo check` → `nsgo test` → `bundle-rpc.sh`（把 pi 的
RPC bundle 和 jiti 拷进 `resources/rpc/`）→ 清掉任何残留的 node → `nsgo build`。

### 指定平台

```sh
TARGET=windows_x64 sh scripts/build.sh
go tool nsgo build -target macos_arm64,macos_x64,windows_x64,windows_arm64,linux_x64,linux_arm64
```

`-installers`（默认开）会额外产出：macOS 的 dmg、Windows 的 NSIS `setup.exe`、
Linux 的 `.deb`、Arch `.pkg.tar.gz` 和 `.AppImage`。

## 目录结构

```
main.go app.go desktop.go      窗口、菜单、桌面集成
state*.go                      应用状态与动作（会话、模型、设置）
ui_*.go                        各屏视图：shell / chat / status / docker / dialogs / settings
internal/agent                 pi 子进程：传输层（JSONL）、协议、会话池、信任
internal/nodeinstall           系统 node 探测、官方 tarball 下载与校验、解包
internal/sessions              会话读写、缓存、导出
internal/term                  终端（pty）
internal/files internal/worktree internal/textdiff internal/review…
                              文件树、worktree、diff 与审阅统计
internal/models internal/skills internal/config
                              模型目录、技能、配置
scripts/bundle-rpc.sh          从已装 pi 拷贝 RPC bundle + jiti，并放入 EPIPE 保护
scripts/build.sh               唯一的构建入口
locales/                       中英文文案（en 为源语言）
```

## 实现要点

- **先找用户的 pi**：`internal/agent/pi.go` 按 `PI_EXECUTABLE` → `PATH` → 登录 shell 的顺序找 `pi`，
  读 `pi --version` 判断能不能驱动（1.4+ 同主版本），并把 pi 所在目录放到子进程 PATH 首位
  （pi 是 `#!/usr/bin/env node` 脚本，nvm / `~/.local` 都靠这个）。
- **macOS 文件夹授权**：桌面/文稿/下载受 TCC 保护，未授权时 `open()` 不返回 —— pi 起来但一句话不说，
  看起来就是断连。`connectWith` 先探一次目录，3 秒没回就给出「打开隐私设置」和具体勾选项，
  而不是干等 24 秒后报超时。
- **JSONL 边界**：只有 stdout 被切帧解析，stderr 只进日志不参与协议，异常时日志里有退出原因。
- **子进程即会话**：切会话、换模型、改工具集合都换一个进程，避免半截状态。
- **EPIPE 保护**：父进程关闭管道时 pi 仍有帧在飞，Node 会因未处理的 `error` 事件崩溃。
  `scripts/epipe-guard.cjs` 用 `node --require` 预加载，对这一个预期条件安静退出，其余照旧致命。
- **信任库自愈**：启动子进程前修掉 `~/.pi/agent/trust.json` 里的历史格式（`trustedDirs`），
  否则 pi 会直接拒绝启动，看起来就像「又断连了」。
- **子进程组收尾**：pi 在自进程组里拉起 MCP 服务器。它退出时若没带走这些子进程，
  下一个 pi 会卡在等待那个服务器（端口/数据库/锁被占），对外表现就是「pi 起来了但
  一直不回应」→ 界面显示未连接。所以每次子进程退出后都向它的进程组补一次 SIGTERM。
- **就绪探测 + 一次重开**：`get_state` 12 秒没回应就丢掉这个子进程重开一次；
  等的那段时间胶囊显示「连接中」而不是「未连接」。
- **先礼后兵地停子进程**：关会话只关 stdin（这是 pi 自己的退出信号），等 8 秒让它写完
  会话文件和 MCP 收尾；超时才 SIGTERM，再超时 SIGKILL。以前是关掉 stdin 立刻 SIGTERM，
  日志里每次都是 `wait=signal: terminated`，等于每切一次会话就可能写坏半个会话文件。
- **断连时把子进程说的最后一句抓出来**：stderr 除了进日志，还留最后 40 行在内存里，
  错误提示和断连卡片直接显示那一行（`env: node: No such file or directory`、
  `Cannot find module 'jiti'` 这类），不用去翻日志。
- **打包不带 node**：`.app` 里没有 node 可执行文件；没有系统 Node 时由 `internal/nodeinstall`
  下载 `v22.x` 官方 tarball（校验 SHA-256 后解包，缓存复用）。

## 已知限制

- `go.mod` 里的 `replace github.com/oneo-me/nsgo => ../nsgo-main` 指向同级目录的 NSGo 检出，
  克隆后需要按这个布局放好框架，或改成自己 fork 的模块路径。
- 非 macOS 平台不重定向文件描述符 2，Go 运行时自己的 fatal 报告不会进日志文件
  （`internal/agent/agent.log` 与通过 `os.Stderr` 的输出不受影响）。
- Windows 上停止子进程用 `taskkill /T`，没有 POSIX 的 SIGTERM 语义。
- **Windows arm64 没有自动下载 node 路径**：nodejs.org 不发布 win-arm64 官方包，
  `nodeinstall` 会明确报错，需要用户自己装 Node ≥ 22；x64 走官方 `.zip`，正常自动安装。
- **移动端只有 Android arm64 能出包**（`go tool nsgo build -target android_arm64`），
  但本应用的核心是拉起 `node` 子进程跑 pi 的 RPC，Android 里没有可执行的 node，
  所以 APK 目前只有界面；iOS 需要完整 Xcode（只装 Command Line Tools 时 `-target ios` 直接报
  `the iOS SDK needs Xcode`），且同样无法 fork/exec。

## 致谢

pigo 站着三个上游的肩膀：

- **[NSGo](https://github.com/oneo-me/nsgo)** — 原生 UI 框架。窗口、控件、文本排版、
  终端、i18n、打包（dmg / NSIS / deb / AppImage / APK）全部由它提供，本仓库只写应用层。
- **[pi-web](https://github.com/agegr/pi-web)** — 原 Web 界面。交互流程、布局、状态模型和
  几乎全部文案都来自它，`scripts/gen_locales.py` 至今仍从它的 message 文件生成
  `locales/*.yaml`。
- **[pi](https://github.com/earendil-works/pi)** — agent 本体。pigo 不实现 agent，
  只把它的 RPC 模式装进一个窗口，RPC bundle 由 `scripts/bundle-rpc.sh` 从已安装的 pi 取。
- **[pi-gpui](https://github.com/bytesbay/pi-gpui)** — Rust/GPUI 的同门实现。子进程的
  优雅关闭 + watchdog、把 stderr 尾巴显示给用户、按 pi 版本做能力判断，这几处借鉴自它。

## License

[MIT](LICENSE) © 2026 pigo contributors。

随包分发的 pi RPC bundle（`resources/rpc/`，构建时从已安装的 pi 拷贝）、NSGo 以及
其他依赖各自遵循上游许可证；发布二进制前请确认它们的条款。
