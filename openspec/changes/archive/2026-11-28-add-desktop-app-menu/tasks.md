# Tasks

## 1. Terminal Controls, Sidebar Visibility, and About Modal

- [x] 1.1 Add terminal font size state (`DefaultTerminalFontSize = 14`, bounds `10`–`28`) and active-tab helpers (`clearActiveTerminalScreen`, `clearActiveTerminalScrollback`, `zoomTerminalFont`, `resetTerminalFont`, `reconnectOrRestartActiveTab`, `closeActiveTab`) in `internal/desktopui/app.go` and `internal/desktopui/session_tab.go`, wiring `a.terminalFontSize()` into SSH and local `terminal.New` calls, and add unit tests in `internal/desktopui/desktopui_test.go` verifying font zoom clamping, reset, and no-op safety when locked or without tabs (`go test ./internal/desktopui -run TestTerminalMenuActions`).
- [x] 1.2 Add `sidebarHidden` state and `toggleSidebar()` on `App` so `App.View()` conditionally hides `renderSidebar(c)` while expanding the terminal workspace, and verify sidebar show/hide rendering in `internal/desktopui/desktopui_test.go` via headless `ui.NewTester` (`go test ./internal/desktopui -run TestSidebarToggle`).
- [x] 1.3 Create `internal/desktopui/about_modal.go` implementing the native "关于 SSH-Guard" modal (`ui.DialogBase`) showing app version (`mygo.App.Version()` with `"0.5.0"` fallback), active rendering backend, platform, data directory, and zero-trust summary; render it in `App.View()` for both locked and unlocked views, and verify locked/unlocked open and dismiss behavior in `internal/desktopui/desktopui_test.go` (`go test ./internal/desktopui -run TestAboutModal`).

## 2. Cross-Platform Native Menu Bar and Dark Chrome

- [x] 2.1 Create `internal/desktopui/menu.go` implementing `buildAppMenu(goos string)` with platform-specific menu layouts (`SSH-Guard`, `会话`, `编辑`, `终端`, `视图`, `安全`, `窗口`, `帮助` on `darwin`; `会话` with `退出`, `编辑`, `终端`, `视图`, `安全`, `帮助` on Windows/Linux), conflict-free accelerators (`Cmd+...` on macOS, `Ctrl+Shift+...` / `Ctrl+Alt+...` on Windows/Linux, `Ctrl+=/Ctrl+-/Ctrl+0` for zoom), and `runUI`-wrapped click handlers; add unit tests verifying both `darwin` and `windows` menu structures and verifying no custom Windows item binds bare `Ctrl+C`, `Ctrl+W`, `Ctrl+R`, `Ctrl+K`, or `Ctrl+B` (`go test ./internal/desktopui -run TestBuildAppMenu`).
- [x] 2.2 Implement `menuState`, `computeMenuState()`, and diff-checked `syncAppMenu()` in `internal/desktopui/menu.go` so `SetEnabled` / `SetChecked` update only on state changes across locked, unlocked-empty, connected-tab, disconnected/error-tab, font-bound, and sidebar-hidden states, and verify all state transitions in `internal/desktopui/desktopui_test.go` (`go test ./internal/desktopui -run TestAppMenuStateSync`).
- [x] 2.3 Wire `mygo.Theme.SetSource(mygo.ThemeDark)`, `a.appMenu = a.buildAppMenu(goruntime.GOOS)`, `a.syncAppMenu()`, and `mygo.App.SetMenu(a.appMenu)` in `App.Run()` (`internal/desktopui/app.go`), invoke `a.syncAppMenu()` at the end of `App.View()`, and verify `go build ./...` compiles cleanly across host and `GOOS=windows GOARCH=amd64 go build ./...` (`go build ./... && GOOS=windows GOARCH=amd64 go build ./...`).

## 3. Full Verification

- [x] 3.1 Run the full test suite and OpenSpec validation to confirm all existing and new desktop UI tests pass without regressions (`go test ./... && openspec validate add-desktop-app-menu`).
