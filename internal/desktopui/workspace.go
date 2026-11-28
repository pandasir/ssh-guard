package desktopui

import (
	"fmt"
	"os"
	"strconv"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

type termViewKey struct {
	id  int
	gen uint64
}

func renderBackendLabel() string {
	if os.Getenv("MYGO_GPU") == "0" {
		return "Ghostty CPU"
	}
	return "Ghostty GPU"
}

func statusDotColor(status string) ui.Color {
	switch status {
	case "connected":
		return colSuccess
	case "connecting":
		return colWarn
	case "error":
		return colDangerDot
	default:
		return colTextDim
	}
}

func (a *App) renderWorkspace(c *ui.Context) {
	active := a.activeSessionTab()

	ui.Column(c).Grow(1).FillHeight().Background(colCanvasBg).Children(func() {
		// Top session status bar
		ui.Row(c).Height(48).Padding(0, 20).Justify(ui.SpaceBetween).AlignItems(ui.Center).
			Background(colTopBarBg).BorderWidth(0, 0, 1, 0).BorderColor(colDivider).
			Children(func() {
				status := "idle"
				label := "未连接 · 请从左侧选择主机"
				if active != nil {
					status = active.status
					label = active.statusText
				}
				ui.Row(c).Gap(9).AlignItems(ui.Center).Children(func() {
					dot := ui.Box(c).Size(8, 8).Radius(999).Background(statusDotColor(status))
					if status == "connected" {
						dot.Shadow(0, 0, 8, 0, ui.RGBA(52, 211, 153, 0.55))
					}
					ui.Text(c, label).FontSize(13).TextColor(colTextSecondary).SingleLine()
				})

				ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Text(c, fmt.Sprintf("原生桌面端 · %s (%s)", renderBackendLabel(), a.platform)).
						FontSize(12).TextColor(colTextDim).SingleLine()
					if active != nil && active.kind == tabKindSSH && (active.status == "idle" || active.status == "error") {
						if subtleButton(c, "重连", 5, 11).Clicked() {
							a.reconnectTab(active)
						}
					}
					if active != nil && active.kind == tabKindLocal && (active.status == "idle" || active.status == "error") {
						if subtleButton(c, "重启终端", 5, 11).Clicked() {
							a.restartLocalTerminal(active)
						}
					}
					if active != nil {
						actionLabel := "断开连接"
						if active.kind == tabKindLocal {
							actionLabel = "关闭终端"
						}
						if subtleButton(c, actionLabel, 5, 11).Clicked() {
							a.touchActivity()
							a.closeTabByID(active.id)
						}
					}
				})
			})

		// Multi-tab session strip
		if len(a.tabs) > 0 {
			counts := make(map[string]int)
			ui.ScrollHorizontal(c.Key("session-tabs")).Height(42).Padding(6, 12).
				Background(colTabStripBg).BorderWidth(0, 0, 1, 0).BorderColor(colDivider).
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
							bg := colCardBg
							border := colBorder
							txtColor := colTextMuted
							if isCur {
								bg = ui.Hex("#152033")
								border = ui.RGBA(59, 130, 246, 0.55)
								txtColor = colTextPrimary
							}
							row := ui.Row(c.Key(tabID)).Padding(4, 9).Gap(6).Radius(7).AlignItems(ui.Center).
								Background(bg).Border(1, border)
							if isCur {
								row.Shadow(0, 2, 10, 0, ui.RGBA(37, 99, 235, 0.18))
							}
							row.Children(func() {
								ui.ButtonBase(c).Label("切换会话 " + tabLabel).Cursor(ui.CursorPointer).OnClick(func() {
									a.touchActivity()
									a.activeTab = idx
								}).Children(func() {
									ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
										ui.Box(c).Size(7, 7).Radius(999).Background(statusDotColor(tab.status))
										ui.Text(c, tabLabel).FontSize(12).TextColor(txtColor).SingleLine()
									})
								})
								closeBtn := ui.ButtonBase(c).Label("关闭会话 "+strconv.Itoa(tabID)).Padding(0, 4).Radius(4).Cursor(ui.CursorPointer)
								closeFg := colTextDim
								if closeBtn.Hovered() {
									closeBtn.Background(ui.RGBA(148, 163, 184, 0.14))
									closeFg = colTextPrimary
								}
								closeBtn.OnClick(func() {
									a.touchActivity()
									a.closeTabByID(tabID)
								}).Children(func() {
									ui.Text(c, "×").FontSize(13).TextColor(closeFg)
								})
							})
						}
						addLocal := ui.ButtonBase(c.Key("tabbar-new-local")).Label("新建本地终端").Padding(4, 10).Radius(7).
							Background(colCardBg).Border(1, colBorder).Cursor(ui.CursorPointer)
						addFg := colTextSecondary
						if addLocal.Hovered() {
							addLocal.Background(colSurfaceBtnHover).Border(1, colBorderStrong)
							addFg = colTextPrimary
						}
						addLocal.OnClick(func() {
							a.openLocalTerminal()
						}).Children(func() {
							ui.Text(c, "+ 终端").FontSize(12).TextColor(addFg).SingleLine()
						})
					})
				})
		}

		// Native Ghostty terminal pane (intentionally omits the Web edition's left gutter).
		if active == nil || active.term == nil {
			a.renderEmptyWorkspaceHero(c)
		} else {
			ui.Box(c).Grow(1).FillWidth().Padding(6).Background(colCanvasBg).Children(func() {
				terminal.View(c.Key(termViewKey{id: active.id, gen: active.localGen}), active.term).Fill().AutoFocus()
			})
		}
	})
}

func (a *App) renderEmptyWorkspaceHero(c *ui.Context) {
	// Atmospheric radial blue spotlight over deep obsidian canvas.
	ui.Column(c).Grow(1).FillWidth().Center().Padding(32).
		RadialGradient(ui.RadialGradient{
			Center: ui.Point{X: 0.5, Y: 0.43},
			Radius: 430,
			Oklab:  true,
			Stops: []ui.GradientStop{
				{Color: ui.RGBA(28, 78, 178, 0.34), At: 0},
				{Color: ui.RGBA(18, 48, 114, 0.19), At: 0.36},
				{Color: ui.RGBA(10, 24, 56, 0.08), At: 0.70},
				{Color: colCanvasBg, At: 1},
			},
		}).Children(func() {
		ui.Column(c).AlignItems(ui.Center).Gap(14).MaxWidth(520).Children(func() {
			// Frosted glass 3D terminal emblem tile
			ui.Box(c).Size(108, 108).Radius(24).Center().Margin(0, 0, 10, 0).
				LinearGradient(ui.LinearGradient{
					Angle: 145,
					Stops: []ui.GradientStop{
						{Color: ui.RGBA(120, 140, 174, 0.50), At: 0},
						{Color: ui.RGBA(46, 76, 132, 0.55), At: 0.46},
						{Color: ui.RGBA(24, 62, 134, 0.68), At: 1},
					},
				}).
				Border(1.5, ui.RGBA(147, 197, 253, 0.30)).
				BorderLinearGradient(ui.LinearGradient{
					Angle: 145,
					Stops: []ui.GradientStop{
						{Color: ui.RGBA(255, 255, 255, 0.46), At: 0},
						{Color: ui.RGBA(147, 197, 253, 0.22), At: 0.5},
						{Color: ui.RGBA(59, 130, 246, 0.42), At: 1},
					},
				}).
				InsetShadow(0, 1, 2, 0, ui.RGBA(255, 255, 255, 0.28)).
				Shadow(0, 14, 38, 2, ui.RGBA(30, 86, 212, 0.38)).
				Children(func() {
					ui.Image(c, iconHeroPromptSVG).Size(68, 68)
				})

			// Headline
			ui.Text(c, "SSH 与本地 Shell 原生终端").
				FontSize(24).Bold().LetterSpacing(0.3).
				TextColor(colTextPrimary).TextAlign(ui.Center)

			// Subtitle / zero-trust architecture note
			ui.Text(c, "选择左侧主机连接，或直接打开本地终端（支持同时打开多个）。SSH 密码和私钥仅在本地 Go 核心握手瞬间解密并立即内存清零，全部终端由 Ghostty 引擎原生渲染。").
				FontSize(13).LineHeight(1.65).
				TextColor(colTextMuted).TextAlign(ui.Center).MaxWidth(504)

			// Glowing primary CTA button
			ui.Box(c).Margin(14, 0, 0, 0).Children(func() {
				cta := ui.ButtonBase(c).Padding(10, 24).Radius(10).Cursor(ui.CursorPointer)
				from, to := ui.Hex("#5093fb"), ui.Hex("#2664e6")
				border := ui.RGBA(191, 219, 254, 0.50)
				glow := ui.RGBA(37, 99, 235, 0.58)
				if cta.Pressed() {
					from, to = ui.Hex("#2f73f5"), ui.Hex("#1d4ed8")
					glow = ui.RGBA(37, 99, 235, 0.34)
				} else if cta.Hovered() {
					from, to = ui.Hex("#64a2fc"), ui.Hex("#2f6ff2")
					border = ui.RGBA(219, 234, 254, 0.68)
					glow = ui.RGBA(59, 130, 246, 0.72)
				}
				cta.LinearGradient(ui.LinearGradient{From: from, To: to, Angle: 180}).
					Border(1, border).
					InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.30)).
					Shadow(0, 6, 26, 0, glow).
					Children(func() {
						ui.Text(c, ">_ 打开本地终端").FontSize(14).FontWeight(600).TextColor(ui.Hex("#ffffff")).SingleLine()
					})
				if cta.Clicked() {
					a.openLocalTerminal()
				}
			})
		})
	})
}
