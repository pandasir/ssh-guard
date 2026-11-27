# Proposal

## Why

SSH-Guard 当前桌面端基于 Wails v2 + 系统 WebView（WKWebView / WebView2）+ `xterm.js` 运行：即便 Go 后端已压紧内存（`GOMEMLIMIT=96MiB`）并采用纯进程内 IPC，浏览器内核子进程仍带来较高的常驻内存开销，且高频终端输出与全屏 TUI 受限于 IPC/JS 序列化、Windows 端禁用 WebView2 GPU 加速（`WebviewGpuIsDisabled: true`）以及 DOM 重绘开销，流畅度与多标签扩展性不足。借助纯 Go、零 CGO、原生 GPU 渲染且内置 Ghostty (`libghostty-vt`) 终端引擎的 `egoist/mygo` 重构桌面端，可以在不破坏离线 Web 版的前提下大幅降低内存占用、消除 WebView 运行时依赖与浏览器攻击面，并显著提升终端渲染帧率。

## What Changes

- **解耦 SSH 会话核心层 (`internal/sshproxy`)**：抽离传输无关的 `DialSession` 与 `LiveSession`（统一负责凭据即时解密与 `Zeroize`、TOFU/MITM 校验、合并 `stdout`/`stderr`、高危命令拦截与告警注入、PTY resize、`.cast` 录像及流式读取、幂等 `Close()` 清理），让离线 Web 版（`cmd/web`）与原生桌面端共享同一套安全契约。
- **使用 MyGo Native UI + Ghostty 重写桌面端界面 (`internal/desktopui` + 根 `main.go`)**：以纯 Go 原生组件（无 WebView，不引入混合 `ui.WebView`）完整覆盖解锁/初始化保险库、主机 CRUD 与原生私钥文件选择、多标签 Ghostty 终端（显式限制每标签 `Scrollback` 上限）、多会话排队的 TOFU/MITM 验证弹窗与原标签重连、高危命令终端内红字提示 + Toast、独立桌面端空闲/最长有效期自动锁定、审计日志表、录像列表及原生流式 `.cast` 倍速回放。
- **BREAKING — 移除 Wails v2 桌面壳与桌面端终端左侧时间/行号 Gutter**：
  - 经过阶段 0 最小 PoC 验证通过后，移除 `github.com/wailsapp/wails/v2`、`wails.json`、`web/static/wailsjs/` 以及 `internal/api` / `internal/auth` / `web/static/app.js` 中仅服务于 Wails 桌面桥接的兼容代码。
  - 因 `mygo/plugins/terminal` 基于 `libghostty-vt` GPU 黑盒渲染、不暴露行级 marker 与视口滚动偏移，桌面原生端不再提供终端左侧时间戳/行号 Gutter（离线 Web 版继续保留）。
- **BREAKING — 升级工具链至 Go 1.27 并重构桌面打包发布链路**：
  - 根目录 `go.mod` 与 CI 工作流统一升级至 Go 1.27+（满足 `egoist/mygo` 要求；`cmd/web` 不引用 `mygo`，保持零 CGO 纯 Go 产物体积不变）。
  - macOS 桌面最低支持版本提高至 macOS 13.0（`libghostty-vt` 硬性要求），适配 `libghostty-vt.dylib` 签名与 `.dmg` 打包。
  - Windows 桌面端继续保持双单文件交付：NSIS 安装包（`installer.exe`）与 100% 离线单文件便携版（`portable.exe`，构建时内嵌 `ghostty-vt.dll`，首次启动经 SHA-256 校验原子释放至 `%LocalAppData%\SSH-Guard\natives\` 并通过 `MYGO_GHOSTTY_VT` 加载）。

## Capabilities

### New Capabilities
- `ssh-session-core`: 传输无关的 SSH 会话核心引擎与流式录像能力，统一封装凭据即时解密清零、TOFU/MITM 指纹校验、合并输出流、高危命令实时拦截、PTY 尺寸同步、幂等清理与 `.cast` 流式读取，供 Web 与桌面双端共同复用。
- `desktop-native-ui`: 基于 MyGo Native UI 与 Ghostty (`plugins/terminal`) 的零 WebView 原生桌面界面与安全生命周期管理，覆盖保险库解锁、空闲自动锁定、主机管理、多标签有界 Scrollback 终端、TOFU/MITM 弹窗队列、高危命令提示、审计日志与原生录像回放。
- `desktop-packaging`: 基于 Go 1.27+ 与 `go tool mygo build` 的零 CGO 跨平台桌面构建与发布能力，覆盖 macOS 13+ `.app`/`.dmg`（含 `libghostty-vt.dylib` 签名）、Windows NSIS 单文件安装包与 Windows 离线自解压单文件 `portable.exe`，并保持离线 Web 版四端构建不变。

### Modified Capabilities
<!-- 当前仓库首次初始化 OpenSpec，尚无既有 specs/ 条目，因此全部相关能力作为 New Capabilities 建档 -->

## Impact

- **受影响代码与配置**：
  - 核心层：`internal/sshproxy/proxy.go`、`internal/sshproxy/recorder.go`、新增 `internal/sshproxy/session.go`；清理 `internal/api/server.go`、`internal/auth/session.go`、`web/static/app.js` 中的桌面桥接分支（阶段 4 收口）。
  - 桌面层：替换根 `main.go` 与 `main_test.go`，新增 `internal/desktopui/` 与 `mygo.json`、`resources/icon.png`；移除 `wails.json`、`web/static/wailsjs/` 与原 Wails 专属 `build/` 模板。
  - 依赖与工具链：`go.mod` / `go.sum` 升级至 Go 1.27，引入 `github.com/egoist/mygo`（锁定版本 tag），移除 `github.com/wailsapp/wails/v2`。
  - 构建与发布：`Makefile`、`scripts/build-mac-m4.sh`、`scripts/build-windows.ps1`、`scripts/fix-mac-gatekeeper.sh`、`.github/workflows/build-release.yml`、`README.md`。
- **关键风险与前置门禁**：`egoist/mygo` 处于 v0.x 高速迭代期且 `libghostty-vt` 以 FFI 方式在进程内运行，必须先通过阶段 0 最小 PoC 验证 macOS / Windows 下的中文 IME、CJK 渲染、多标签内存以及 `portable.exe` 自解压加载 `ghostty-vt.dll`，验证达标后再移除 Wails 入口。
