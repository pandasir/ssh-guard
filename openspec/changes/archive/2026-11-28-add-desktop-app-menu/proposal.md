# Proposal

## Why

SSH-Guard Desktop currently does not register a custom application menu with `mygo.App.SetMenu`: macOS only receives MyGo's generic placeholder menu with no SSH-Guard actions, and Windows/Linux receive no menu bar at all. Adding a concise, pragmatic native OS application menu bar surfaces existing session, host, security, and terminal controls through standard desktop menus and conflict-free keyboard shortcuts, while enforcing consistent dark native chrome on Windows.

## What Changes

- Register a cross-platform native OS menu bar via `mygo.App.SetMenu` (`SSH-Guard` app menu on macOS, plus `会话`, `编辑`, `终端`, `视图`, `安全`, `窗口` on macOS, and `帮助`) with platform-safe keyboard accelerators (`Cmd+...` on macOS; `Ctrl+Shift+...` / `Ctrl+Alt+...` on Windows/Linux so shell control keys like `Ctrl+C`, `Ctrl+W`, `Ctrl+R`, `Ctrl+K`, and `Ctrl+B` are not hijacked).
- Force MyGo's application appearance to dark mode (`mygo.Theme.SetSource(mygo.ThemeDark)`) before `mygo.App.Run()` so Windows 10/11 title bars, native menu bars, and popup menus match SSH-Guard's `#0d1117` dark workspace even when the OS runs in light mode.
- Synchronize menu item enabled and checked states whenever vault unlock/lock, tab list/active tab, session status, or sidebar visibility changes, strictly disabling session, host, terminal, audit, recording, and lock actions while the vault is locked.
- Wire existing desktop capabilities directly into the menu (`新建本地终端`, `新建 SSH 主机...`, `重连 / 重启当前会话`, `关闭当前标签页`, `关闭所有会话`, `打开数据目录...`, `立即锁定凭证库`, `会话录像列表...`, `安全审计日志...`, standard `RoleEditMenu` and `RoleToggleFullScreen` / `RoleWindowMenu` / `RoleQuit`).
- Add lightweight native terminal and view actions exposed through the menu:
  - Clear active terminal screen (`term.ClearScreen()`) and clear scrollback (`term.ClearScrollback()`).
  - Terminal font size zoom in / out / reset (`10pt`–`28pt`, default `14pt`), applied to open terminal tabs and inherited by newly opened tabs.
  - Toggleable host sidebar visibility (`显示主机侧边栏` checkbox item) to reclaim horizontal workspace on smaller screens.
  - Native "关于 SSH-Guard" (`About`) modal displaying app version, rendering backend (`Ghostty GPU`/`Ghostty CPU`), platform, data directory, and zero-trust summary.
- Keep existing sidebar footer buttons (`录像`, `审计`, `数据目录`, `锁定`) in place so existing users retain direct one-click controls alongside the menu bar.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `desktop-native-ui`: add a state-aware native OS application menu bar with platform-safe shortcuts, forced dark native chrome on Windows, terminal clear/zoom controls, sidebar visibility toggle, and a native About dialog.

## Impact

- `internal/desktopui/app.go`: configure dark theme source before run, initialize/register native menu bar, hold terminal font size / sidebar visibility / about modal state, render about modal, and trigger menu state sync on lifecycle changes.
- `internal/desktopui/menu.go` (new): build the cross-platform `mygo.Menu` template, compute state-aware enabled/checked flags, and dispatch menu commands cleanly on the UI thread.
- `internal/desktopui/about_modal.go` (new): render the native "关于 SSH-Guard" dialog.
- `internal/desktopui/session_tab.go`: inherit current app terminal font size on tab creation, expose active-tab clear/zoom helpers, and sync menu state on tab/status changes.
- `internal/desktopui/workspace.go` & `internal/desktopui/sidebar.go`: honor sidebar visibility toggle and trigger menu sync on active tab / sidebar state changes.
- `internal/desktopui/desktopui_test.go`: add headless unit tests for menu state calculation (locked vs unlocked, active vs idle/error tabs), terminal font zoom bounds & sync, clear actions, sidebar toggle, and About modal rendering.
- No new third-party dependencies, database schema changes, or SSH session core changes.
