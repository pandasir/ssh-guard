# Design

## Context

See [proposal.md](proposal.md) for motivation and scope.

SSH-Guard Desktop runs on pure-Go `mygo v0.10.0` with native `ui.View` rendering and embedded Ghostty (`plugins/terminal`) tabs under a tight 28 MiB steady-state memory budget (`GOGC=25`, `GOMEMLIMIT=28 MiB`). Currently:
- `App.Run()` in `internal/desktopui/app.go` never calls `mygo.App.SetMenu(...)`, leaving macOS with MyGo's generic placeholder menu and Windows/Linux with no menu bar (`defaultMenu()` returns `nil` off macOS).
- `App.View()` configures `ui.Context` with `newDesktopTheme()` (`#0d1117` dark palette), but never calls `mygo.Theme.SetSource(mygo.ThemeDark)`, so on light-mode Windows systems the native title bar and any native menu bar would render light (`#FFFFFF`) above a dark workspace.
- In MyGo's Windows/native-UI keyboard dispatch (`internal/windows/surface.go`), custom menu accelerators run *before* surface/terminal content, whereas standard `MenuRole` items (`RoleCopy`, `RolePaste`, etc.) run *after* surface content leaves the key unconsumed.
- Existing modals use `ui.DialogBase(c, &openBool, ...)` and are currently rendered only in the unlocked branch of `App.View()`.

## Goals / Non-Goals

**Goals:**
- Provide a clean, state-synchronized native OS menu bar across macOS, Windows, and Linux with zero extra runtime dependencies and negligible memory overhead.
- Ensure Windows 10/11 title bars, native menu bars, and popup menus always render in dark mode (`#202020` / Win11 Fluent dark) regardless of OS theme preference.
- Keep interactive terminal shortcuts (`Ctrl+C`, `Ctrl+W`, `Ctrl+R`, `Ctrl+K`, `Ctrl+B`, etc.) completely free from menu accelerator interception on Windows/Linux.
- Make menu state computation, command handlers, font zoom, sidebar toggle, and the About modal fully testable in existing headless `ui.NewTester` unit tests.

**Non-Goals:**
- Custom in-window drawn menu bars (`ui.Menu` / `PopoverMenuButton`) or `AutoHideMenuBar` toggle in this iteration.
- Persisting terminal font size or sidebar visibility across app restarts (keep in-memory session state to avoid DB schema migrations or config files).
- Tab-switching shortcuts (`Cmd+Shift+[` / `]`), host-search focus shortcuts, or a separate keyboard-shortcuts reference dialog (deferred beyond this pragmatic v1).
- Removing existing sidebar footer buttons (`录像`, `审计`, `数据目录`, `锁定`).

## Decisions

### 1. Native OS Menu Bar (`mygo.App.SetMenu`) with Forced Dark Theme Source
- **Choice**: Call `mygo.Theme.SetSource(mygo.ThemeDark)` before `mygo.App.Run()`, and register a single application menu via `mygo.App.SetMenu(a.buildAppMenu(goruntime.GOOS))` inside `mygo.App.WhenReady(...)` right before creating the main window.
- **Rationale**: On macOS this replaces the placeholder top menu bar natively; on Windows 10/11 MyGo already draws dark UAH / Win11 Fluent rounded menu bars and Acrylic dark popups automatically when `Theme.SetSource(ThemeDark)` is active, costing virtually zero Go heap and zero extra render frames.
- **Alternative considered**: Drawing a custom dark menu bar inside `App.View()` using `ui.Row` + `PopoverMenuButton`. Rejected because macOS would display a redundant double menu bar and custom in-app shortcuts would have to be manually re-implemented instead of using native OS accelerators.

### 2. Platform-Adapted Menu Layout & Conflict-Free Accelerators
- **Choice**: Build the menu template from `goos string` (defaulting to `runtime.GOOS`, injectable for unit tests) using a helper `menuShortcut(goos, macAccel, otherAccel string)`:
  - **macOS (`darwin`)**:
    - `SSH-Guard` (custom app submenu): `关于 SSH-Guard`, separator, `锁定凭证库` (`Cmd+Shift+L`), separator, `RoleHide`, `RoleHideOthers`, `RoleUnhide`, separator, `RoleQuit`
    - `会话`: `新建本地终端` (`Cmd+T`), `新建 SSH 主机...` (`Cmd+N`), separator, `重连 / 重启当前会话` (`Cmd+R`), `关闭当前标签页` (`Cmd+W`), `关闭所有会话` (`Cmd+Alt+W`), separator, `打开数据目录...`
    - `编辑`: `RoleEditMenu`
    - `终端`: `清屏` (`Cmd+K`), `清除回滚历史`, separator, `增大字号` (`Cmd+=`), `减小字号` (`Cmd+-`), `重置字号 (14pt)` (`Cmd+0`)
    - `视图`: `显示主机侧边栏` (`MenuItemCheckbox`, `Cmd+B`), separator, `RoleToggleFullScreen`
    - `安全`: `立即锁定凭证库` (`Cmd+Shift+L`), separator, `会话录像列表...` (`Cmd+Shift+R`), `安全审计日志...` (`Cmd+Shift+A`)
    - `窗口`: `RoleWindowMenu`
    - `帮助`: `关于 SSH-Guard`
  - **Windows & Linux (`!= "darwin"`)**:
    - Omit the macOS-only app menu and `RoleWindowMenu`.
    - Append separator + `RoleQuit` (`退出`) to `会话`.
    - Use `Ctrl+Shift+T` / `Ctrl+Shift+N` / `Ctrl+Shift+R` / `Ctrl+Shift+W` / `Ctrl+Alt+W` / `Ctrl+Shift+K` / `Ctrl+Shift+B` / `Ctrl+Shift+L` / `Ctrl+Alt+R` / `Ctrl+Shift+A`, and `Ctrl+=` / `Ctrl+-` / `Ctrl+0` for font zoom.
- **Rationale**: Matches native OS conventions while guaranteeing custom accelerators never intercept bare `Ctrl+<letter>` control sequences needed by remote shells, Vim, or tmux on Windows/Linux, and relying on MyGo's role-after-content dispatch so `RoleEditMenu` leaves `Ctrl+C` intact for terminal SIGINT.
- **Note on duplicate `Cmd+Shift+L` on macOS**: Avoid binding the identical `Cmd+Shift+L` accelerator on two separate items at once; keep the accelerator on `安全 -> 立即锁定凭证库` (or omit the duplicate item in `SSH-Guard` app menu so each shortcut maps cleanly to one item). Decision: place `立即锁定凭证库` (`Cmd+Shift+L` / `Ctrl+Shift+L`) exclusively under `安全`, keeping the macOS `SSH-Guard` app menu focused on `关于 SSH-Guard`, hide/unhide, and quit.

### 3. Diff-Checked State Synchronization (`menuStateSnapshot`)
- **Choice**: Represent all dynamic menu state as a comparable value struct `menuState`:
  ```go
  type menuState struct {
      unlocked       bool
      hasTabs        bool
      canReconnect   bool
      sidebarVisible bool
      canZoomIn      bool
      canZoomOut     bool
      canResetZoom   bool
  }
  ```
  Store item pointers (or look up by stable item IDs on `a.appMenu`) and cache `a.lastMenuState` alongside `a.menuStateReady bool`. `a.syncAppMenu()` computes `next := a.computeMenuState()`; if `a.menuStateReady && next == a.lastMenuState`, it returns immediately without touching any `mygo.MenuItem`. Otherwise it updates `SetEnabled` / `SetChecked` on changed items and records `a.lastMenuState`.
- **Rationale**: Calling `MenuItem.SetEnabled` / `SetChecked` unconditionally on every frame would trigger native `refreshMenus()` calls and break MyGo's idle redraw budget. Value-equality diffing makes calling `a.syncAppMenu()` at the end of `App.View()` (and immediately after menu creation / state transitions) O(1) with zero native churn when idle.
- **Thread safety**: Every `MenuItem.Click` callback runs on the main thread (per MyGo's threading contract), and dispatches mutations through `a.runUI(...)` so state updates always trigger a frame repaint and stay safe in tests.

### 4. Terminal Font Size & Clear Operations
- **Choice**:
  - Add `termFontSize float32` to `App` (default `DefaultTerminalFontSize = 14`, bounds `MinTerminalFontSize = 10`, `MaxTerminalFontSize = 28`, step `1`).
  - Pass `Font: terminal.Font{Size: a.terminalFontSize()}` when creating SSH and local `terminal.Terminal` instances in `session_tab.go` (do not alter read-only `.cast` replay terminal sizing).
  - `zoomTerminalFont(delta float32)` and `resetTerminalFont()` clamp the size, update `a.termFontSize`, and call `tab.term.SetFont(terminal.Font{Size: a.termFontSize})` on every open tab with a non-nil `term`.
  - `clearActiveTerminalScreen()` calls `active.term.ClearScreen()` and `clearActiveTerminalScrollback()` calls `active.term.ClearScrollback()` on the active tab when unlocked.
- **Rationale**: Directly uses Ghostty `terminal.Terminal`'s thread-safe `SetFont`, `ClearScreen`, and `ClearScrollback` APIs with zero new dependencies or PTY hacks.

### 5. Sidebar Toggle & About Modal Lifecycle
- **Choice**:
  - Add `sidebarHidden bool` to `App` (default `false`, so sidebar is visible by default). In `App.View()`, render `a.renderSidebar(c)` only when `!a.sidebarHidden`. Reset `a.sidebarHidden = false` on `LockVault`? Keep user's layout preference across lock/unlock (just hide/disable the toggle while locked), or keep it visible when unlocked; keeping `sidebarHidden` intact across lock/unlock avoids surprising the user, while `menuState.sidebarVisible` reflects `!a.sidebarHidden`.
  - Add `aboutOpen bool` to `App` and render `a.renderAboutModal(c)` in `internal/desktopui/about_modal.go` using `ui.DialogBase`, invoked in `App.View()` for **both** locked and unlocked views so `关于 SSH-Guard` works on the lock screen as well.
  - Display version from `mygo.App.Version()` with fallback constant `appFallbackVersion = "0.5.0"` (matching `mygo.json`), plus `renderBackendLabel()`, `a.platform`, `a.dataDir`, and a concise zero-trust architecture note.

## Risks / Trade-offs

- **Risk**: Windows `Ctrl+=` / `Ctrl+-` / `Ctrl+0` menu accelerators could overlap with Ghostty `terminal.DefaultKeybinds` (`Control+=`, `Control+-`, `Control+0`).
  → **Mitigation**: Since custom menu accelerators run before terminal content in MyGo, the menu handler runs `zoomTerminalFont(+1/-1)` / `resetTerminalFont()` directly, keeping `App.termFontSize` and all tabs synchronized (whereas terminal's internal zoom would only affect one tab and diverge from menu state). On macOS, `Cmd+=`/`Cmd+-`/`Cmd+0` behaves identically.
- **Risk**: Updating native menus inside `App.View()` could re-enter or cause redundant OS menu rebuilds.
  → **Mitigation**: Guard `syncAppMenu()` behind `a.appMenu != nil` and `menuState` value diffing so native `SetEnabled`/`SetChecked` calls only execute on actual state transitions, never during steady-state frames.
- **Risk**: `LockVault()` leaving the About modal open or leaving privileged modals open.
  → **Mitigation**: Keep `LockVault()` closing all privileged modals (host, delete, TOFU, audit, recordings, replay) as today; `aboutOpen` holds no secrets and can safely remain accessible (or close cleanly via button/Escape).
- **Risk**: Headless unit tests (`ui.NewTester`) run without `mygo.App.Run()`.
  → **Mitigation**: Keep `buildAppMenu(goos)` / `computeMenuState()` / action methods decoupled from a live window (`a.win == nil` safe), so all state transitions, disabled rules, font bounds, sidebar collapse, and About modal rendering are verified in `go test ./...`.
