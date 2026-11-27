package desktopui

import (
	"fmt"
	"os"
	"strconv"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

func renderBackendLabel() string {
	if os.Getenv("MYGO_GPU") == "0" {
		return "Ghostty CPU"
	}
	return "Ghostty GPU"
}

func statusDotColor(status string) ui.Color {
	switch status {
	case "connected":
		return ui.Hex("#3fb950")
	case "connecting":
		return ui.Hex("#d29922")
	case "error":
		return ui.Hex("#f85149")
	default:
		return ui.Hex("#6e7681")
	}
}

func (a *App) renderWorkspace(c *ui.Context) {
	active := a.activeSessionTab()

	ui.Column(c).Grow(1).FillHeight().Background(ui.Hex("#0a0d12")).Children(func() {
		// Top session status bar
		ui.Row(c).Height(48).Padding(0, 16).Justify(ui.SpaceBetween).AlignItems(ui.Center).
			Background(ui.Hex("#161b22")).BorderWidth(0, 0, 1, 0).BorderColor(ui.Hex("#30363d")).
			Children(func() {
				status := "idle"
				label := "未连接 · 请从左侧选择主机"
				if active != nil {
					status = active.status
					label = active.statusText
				}
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Size(9, 9).Radius(999).Background(statusDotColor(status))
					ui.Text(c, label).FontSize(13).SingleLine()
				})

				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, fmt.Sprintf("原生桌面端 · %s (%s)", renderBackendLabel(), a.platform)).
						FontSize(12).TextColor(ui.Hex("#8b949e")).SingleLine()
					if active != nil && active.kind == tabKindSSH && (active.status == "idle" || active.status == "error") {
						if ui.Button(c, "重连").Padding(5, 10).Clicked() {
							a.reconnectTab(active)
						}
					}
					if active != nil {
						actionLabel := "断开连接"
						if active.kind == tabKindLocal {
							actionLabel = "关闭终端"
						}
						if ui.Button(c, actionLabel).Padding(5, 10).Clicked() {
							a.touchActivity()
							a.closeTabByID(active.id)
						}
					}
				})
			})

		// Multi-tab session strip
		if len(a.tabs) > 0 {
			counts := make(map[string]int)
			ui.ScrollHorizontal(c.Key("session-tabs")).Height(40).Padding(6, 10).
				Background(ui.Hex("#0d1117")).BorderWidth(0, 0, 1, 0).BorderColor(ui.Hex("#21262d")).
				Children(func() {
					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						for i, tab := range a.tabs {
							idx := i
							tabID := tab.id
							key := tab.countKey()
							counts[key]++
							seen := counts[key]
							tabLabel := tab.stripLabel(seen)
							isCur := idx == a.activeTab
							bg := ui.Hex("#161b22")
							border := ui.Hex("#30363d")
							txtColor := ui.Hex("#8b949e")
							if isCur {
								bg = ui.Hex("#1f2630")
								border = ui.Hex("#2f81f7")
								txtColor = ui.Hex("#e6edf3")
							}
							ui.Row(c.Key(tabID)).Padding(4, 8).Gap(6).Radius(6).AlignItems(ui.Center).
								Background(bg).Border(1, border).Children(func() {
								ui.ButtonBase(c).Label("切换会话 " + tabLabel).OnClick(func() {
									a.touchActivity()
									a.activeTab = idx
								}).Children(func() {
									ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
										ui.Box(c).Size(8, 8).Radius(999).Background(statusDotColor(tab.status))
										ui.Text(c, tabLabel).FontSize(12).TextColor(txtColor).SingleLine()
									})
								})
								ui.ButtonBase(c).Label("关闭会话 "+strconv.Itoa(tabID)).Padding(0, 4).Radius(4).
									OnClick(func() {
										a.touchActivity()
										a.closeTabByID(tabID)
									}).Children(func() {
									ui.Text(c, "×").FontSize(13).TextColor(ui.Hex("#8b949e"))
								})
							})
						}
					})
				})
		}

		// Native Ghostty terminal pane (intentionally omits the Web edition's left gutter).
		if active == nil || active.term == nil {
			ui.Column(c).Grow(1).Center().Gap(10).Padding(24).Children(func() {
				ui.Text(c, ">_").FontSize(40).Bold().TextColor(ui.Hex("#30363d"))
				ui.Text(c, "SSH 与本地 Shell 原生终端").FontSize(17).Bold()
				ui.Text(c, "选择左侧主机连接，或直接打开本地终端。SSH 密码和私钥仅在本地 Go 核心握手瞬间解密并立即内存清零，全部终端由 Ghostty 引擎原生渲染。").
					FontSize(13).TextColor(ui.Hex("#8b949e")).MaxWidth(480)
				if ui.PrimaryButton(c, ">_ 打开本地终端").Padding(6, 14).Clicked() {
					a.openLocalTerminal()
				}
			})
		} else {
			ui.Box(c).Grow(1).FillWidth().Padding(6).Background(ui.Hex("#0a0d12")).Children(func() {
				terminal.View(c.Key(active.id), active.term).Fill().AutoFocus()
			})
		}
	})
}
