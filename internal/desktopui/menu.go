package desktopui

import "github.com/egoist/mygo"

const (
	menuIDAppAbout        = "app.about"
	menuIDNewLocal        = "session.new_local"
	menuIDNewHost         = "session.new_host"
	menuIDReconnect       = "session.reconnect"
	menuIDCloseTab        = "session.close_tab"
	menuIDCloseAllTabs    = "session.close_all"
	menuIDOpenDataDir     = "session.open_data_dir"
	menuIDSessionQuit     = "session.quit"
	menuIDClearScreen     = "terminal.clear_screen"
	menuIDClearScrollback = "terminal.clear_scrollback"
	menuIDZoomIn          = "terminal.zoom_in"
	menuIDZoomOut         = "terminal.zoom_out"
	menuIDZoomReset       = "terminal.zoom_reset"
	menuIDToggleSidebar   = "view.toggle_sidebar"
	menuIDLockVault       = "security.lock_vault"
	menuIDRecordings      = "security.recordings"
	menuIDAuditLogs       = "security.audit_logs"
	menuIDHelpAbout       = "help.about"
)

// menuState is a comparable snapshot of all app state that drives native menu
// enabled and checked flags. Diffing against lastMenuState avoids redundant
// native menu rebuilds on steady-state frames.
type menuState struct {
	unlocked       bool
	hasTabs        bool
	canReconnect   bool
	sidebarVisible bool
	canZoomIn      bool
	canZoomOut     bool
	canResetZoom   bool
}

func menuShortcut(goos, macAccel, otherAccel string) string {
	if goos == "darwin" {
		return macAccel
	}
	return otherAccel
}

func (a *App) openDataDirAsync() {
	if !a.unlocked {
		return
	}
	a.touchActivity()
	dir := a.dataDir
	openFn := a.openDirFn
	go func() {
		if err := openFn(dir); err != nil {
			a.runUI(func() {
				a.pushToast("无法打开数据目录", err.Error(), "error")
			})
		}
	}()
}

// buildAppMenu constructs the cross-platform native OS menu bar for the target
// GOOS with conflict-free terminal keyboard accelerators.
func (a *App) buildAppMenu(goos string) *mygo.Menu {
	sessionItems := []*mygo.MenuItem{
		{
			ID:          menuIDNewLocal,
			Label:       "新建本地终端",
			Accelerator: menuShortcut(goos, "Cmd+T", "Ctrl+Shift+T"),
			Click: func(_ *mygo.MenuItem, _ *mygo.Window) {
				a.runUI(func() {
					a.openLocalTerminal()
					a.syncAppMenu()
				})
			},
		},
		{
			ID:          menuIDNewHost,
			Label:       "新建 SSH 主机...",
			Accelerator: menuShortcut(goos, "Cmd+N", "Ctrl+Shift+N"),
			Click: func(_ *mygo.MenuItem, _ *mygo.Window) {
				a.runUI(func() {
					if !a.unlocked {
						return
					}
					a.touchActivity()
					a.openAddHostModal()
					a.syncAppMenu()
				})
			},
		},
		mygo.Separator(),
		{
			ID:          menuIDReconnect,
			Label:       "重连 / 重启当前会话",
			Accelerator: menuShortcut(goos, "Cmd+R", "Ctrl+Shift+R"),
			Click: func(_ *mygo.MenuItem, _ *mygo.Window) {
				a.runUI(func() {
					a.reconnectOrRestartActiveTab()
					a.syncAppMenu()
				})
			},
		},
		{
			ID:          menuIDCloseTab,
			Label:       "关闭当前标签页",
			Accelerator: menuShortcut(goos, "Cmd+W", "Ctrl+Shift+W"),
			Click: func(_ *mygo.MenuItem, _ *mygo.Window) {
				a.runUI(func() {
					a.closeActiveTab()
					a.syncAppMenu()
				})
			},
		},
		{
			ID:          menuIDCloseAllTabs,
			Label:       "关闭所有会话",
			Accelerator: menuShortcut(goos, "Cmd+Alt+W", "Ctrl+Alt+W"),
			Click: func(_ *mygo.MenuItem, _ *mygo.Window) {
				a.runUI(func() {
					if !a.unlocked || len(a.tabs) == 0 {
						return
					}
					a.touchActivity()
					a.closeAllTabs()
					a.syncAppMenu()
				})
			},
		},
		mygo.Separator(),
		{
			ID:    menuIDOpenDataDir,
			Label: "打开数据目录...",
			Click: func(_ *mygo.MenuItem, _ *mygo.Window) {
				a.runUI(func() {
					a.openDataDirAsync()
				})
			},
		},
	}
	if goos != "darwin" {
		sessionItems = append(sessionItems,
			mygo.Separator(),
			&mygo.MenuItem{
				ID:    menuIDSessionQuit,
				Label: "退出",
				Role:  mygo.RoleQuit,
			},
		)
	}

	terminalItems := []*mygo.MenuItem{
		{
			ID:          menuIDClearScreen,
			Label:       "清屏",
			Accelerator: menuShortcut(goos, "Cmd+K", "Ctrl+Shift+K"),
			Click: func(_ *mygo.MenuItem, _ *mygo.Window) {
				a.runUI(func() {
					a.clearActiveTerminalScreen()
				})
			},
		},
		{
			ID:    menuIDClearScrollback,
			Label: "清除回滚历史",
			Click: func(_ *mygo.MenuItem, _ *mygo.Window) {
				a.runUI(func() {
					a.clearActiveTerminalScrollback()
				})
			},
		},
		mygo.Separator(),
		{
			ID:          menuIDZoomIn,
			Label:       "增大字号",
			Accelerator: menuShortcut(goos, "Cmd+=", "Ctrl+="),
			Click: func(_ *mygo.MenuItem, _ *mygo.Window) {
				a.runUI(func() {
					a.zoomTerminalFont(1)
					a.syncAppMenu()
				})
			},
		},
		{
			ID:          menuIDZoomOut,
			Label:       "减小字号",
			Accelerator: menuShortcut(goos, "Cmd+-", "Ctrl+-"),
			Click: func(_ *mygo.MenuItem, _ *mygo.Window) {
				a.runUI(func() {
					a.zoomTerminalFont(-1)
					a.syncAppMenu()
				})
			},
		},
		{
			ID:          menuIDZoomReset,
			Label:       "重置字号 (14pt)",
			Accelerator: menuShortcut(goos, "Cmd+0", "Ctrl+0"),
			Click: func(_ *mygo.MenuItem, _ *mygo.Window) {
				a.runUI(func() {
					a.resetTerminalFont()
					a.syncAppMenu()
				})
			},
		},
	}

	viewItems := []*mygo.MenuItem{
		{
			ID:          menuIDToggleSidebar,
			Label:       "显示主机侧边栏",
			Type:        mygo.MenuItemCheckbox,
			Checked:     !a.sidebarHidden,
			Accelerator: menuShortcut(goos, "Cmd+B", "Ctrl+Shift+B"),
			Click: func(_ *mygo.MenuItem, _ *mygo.Window) {
				a.runUI(func() {
					a.toggleSidebar()
					a.syncAppMenu()
				})
			},
		},
		mygo.Separator(),
		{
			Label: "切换全屏",
			Role:  mygo.RoleToggleFullScreen,
		},
	}

	securityItems := []*mygo.MenuItem{
		{
			ID:          menuIDLockVault,
			Label:       "立即锁定凭证库",
			Accelerator: menuShortcut(goos, "Cmd+Shift+L", "Ctrl+Shift+L"),
			Click: func(_ *mygo.MenuItem, _ *mygo.Window) {
				a.runUI(func() {
					if !a.unlocked {
						return
					}
					a.LockVault("")
					a.syncAppMenu()
				})
			},
		},
		mygo.Separator(),
		{
			ID:          menuIDRecordings,
			Label:       "会话录像列表...",
			Accelerator: menuShortcut(goos, "Cmd+Shift+R", "Ctrl+Alt+R"),
			Click: func(_ *mygo.MenuItem, _ *mygo.Window) {
				a.runUI(func() {
					if !a.unlocked {
						return
					}
					a.touchActivity()
					a.openRecordingsModal()
				})
			},
		},
		{
			ID:          menuIDAuditLogs,
			Label:       "安全审计日志...",
			Accelerator: menuShortcut(goos, "Cmd+Shift+A", "Ctrl+Shift+A"),
			Click: func(_ *mygo.MenuItem, _ *mygo.Window) {
				a.runUI(func() {
					if !a.unlocked {
						return
					}
					a.touchActivity()
					a.openAuditModal()
				})
			},
		},
	}

	helpItems := []*mygo.MenuItem{
		{
			ID:    menuIDHelpAbout,
			Label: "关于 SSH-Guard",
			Click: func(_ *mygo.MenuItem, _ *mygo.Window) {
				a.runUI(func() {
					a.openAboutModal()
				})
			},
		},
	}

	var top []*mygo.MenuItem
	if goos == "darwin" {
		top = append(top, &mygo.MenuItem{
			Label: "SSH-Guard",
			Submenu: []*mygo.MenuItem{
				{
					ID:    menuIDAppAbout,
					Label: "关于 SSH-Guard",
					Click: func(_ *mygo.MenuItem, _ *mygo.Window) {
						a.runUI(func() {
							a.openAboutModal()
						})
					},
				},
				mygo.Separator(),
				{Role: mygo.RoleHide},
				{Role: mygo.RoleHideOthers},
				{Role: mygo.RoleUnhide},
				mygo.Separator(),
				{Role: mygo.RoleQuit},
			},
		})
	}

	top = append(top,
		&mygo.MenuItem{Label: "会话", Submenu: sessionItems},
		&mygo.MenuItem{Label: "编辑", Role: mygo.RoleEditMenu},
		&mygo.MenuItem{Label: "终端", Submenu: terminalItems},
		&mygo.MenuItem{Label: "视图", Submenu: viewItems},
		&mygo.MenuItem{Label: "安全", Submenu: securityItems},
	)

	if goos == "darwin" {
		top = append(top, &mygo.MenuItem{Label: "窗口", Role: mygo.RoleWindowMenu})
	}

	top = append(top, &mygo.MenuItem{Label: "帮助", Submenu: helpItems})
	return mygo.NewMenu(top)
}

func (a *App) computeMenuState() menuState {
	unlocked := a.unlocked
	active := a.activeSessionTab()
	hasTabs := unlocked && len(a.tabs) > 0
	canReconnect := unlocked && active != nil && !active.closing && (active.status == "idle" || active.status == "error")
	fontSize := a.terminalFontSize()

	return menuState{
		unlocked:       unlocked,
		hasTabs:        hasTabs,
		canReconnect:   canReconnect,
		sidebarVisible: !a.sidebarHidden,
		canZoomIn:      unlocked && fontSize < MaxTerminalFontSize,
		canZoomOut:     unlocked && fontSize > MinTerminalFontSize,
		canResetZoom:   unlocked && fontSize != DefaultTerminalFontSize,
	}
}

// syncAppMenu updates native menu item enabled/checked states only when the
// computed menuState differs from the last applied snapshot. Returns true when
// an update was applied.
func (a *App) syncAppMenu() bool {
	if a.appMenu == nil {
		return false
	}
	next := a.computeMenuState()
	if a.menuStateReady && next == a.lastMenuState {
		return false
	}

	setEnabled := func(id string, enabled bool) {
		if it := a.appMenu.ItemByID(id); it != nil {
			it.SetEnabled(enabled)
		}
	}

	setEnabled(menuIDNewLocal, next.unlocked)
	setEnabled(menuIDNewHost, next.unlocked)
	setEnabled(menuIDReconnect, next.canReconnect)
	setEnabled(menuIDCloseTab, next.hasTabs)
	setEnabled(menuIDCloseAllTabs, next.hasTabs)
	setEnabled(menuIDOpenDataDir, next.unlocked)

	setEnabled(menuIDClearScreen, next.hasTabs)
	setEnabled(menuIDClearScrollback, next.hasTabs)
	setEnabled(menuIDZoomIn, next.canZoomIn)
	setEnabled(menuIDZoomOut, next.canZoomOut)
	setEnabled(menuIDZoomReset, next.canResetZoom)

	if it := a.appMenu.ItemByID(menuIDToggleSidebar); it != nil {
		it.SetEnabled(next.unlocked)
		it.SetChecked(next.sidebarVisible)
	}

	setEnabled(menuIDLockVault, next.unlocked)
	setEnabled(menuIDRecordings, next.unlocked)
	setEnabled(menuIDAuditLogs, next.unlocked)

	a.lastMenuState = next
	a.menuStateReady = true
	return true
}
