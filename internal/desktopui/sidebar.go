package desktopui

import (
	"fmt"
	"strings"

	"ssh-guard/internal/store"

	"github.com/egoist/mygo/ui"
)

func (a *App) filteredHosts() []store.Host {
	q := strings.ToLower(strings.TrimSpace(a.hostQuery))
	if q == "" {
		return a.hosts
	}
	var out []store.Host
	for _, h := range a.hosts {
		if strings.Contains(strings.ToLower(h.Name), q) ||
			strings.Contains(strings.ToLower(h.Address), q) ||
			strings.Contains(strings.ToLower(h.Username), q) ||
			strings.Contains(strings.ToLower(h.Notes), q) {
			out = append(out, h)
		}
	}
	return out
}

func (a *App) openTabCountForHost(hostID int64) int {
	n := 0
	for _, t := range a.tabs {
		if t.kind == tabKindSSH && t.host.ID == hostID {
			n++
		}
	}
	return n
}

func (a *App) openLocalTabCount() int {
	n := 0
	for _, t := range a.tabs {
		if t.kind == tabKindLocal {
			n++
		}
	}
	return n
}

func formatHostEndpoint(h store.Host) string {
	if h.Port == 0 || h.Port == 22 {
		return fmt.Sprintf("%s@%s", h.Username, h.Address)
	}
	return fmt.Sprintf("%s@%s:%d", h.Username, h.Address, h.Port)
}

func (a *App) renderSidebar(c *ui.Context) {
	active := a.activeSessionTab()

	ui.Column(c).Width(288).FillHeight().Background(colSidebarBg).
		BorderWidth(0, 1, 0, 0).BorderColor(colDivider).Children(func() {
		// Sidebar header
		ui.Row(c).Padding(14, 14).Justify(ui.SpaceBetween).AlignItems(ui.Center).
			BorderWidth(0, 0, 1, 0).BorderColor(colDivider).Children(func() {
			ui.Column(c).Gap(3).Children(func() {
				ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "SSH-Guard").FontSize(16).Bold().TextColor(colTextPrimary)
					ui.Box(c).Padding(2, 6).Radius(4).
						Background(ui.RGBA(59, 130, 246, 0.12)).
						Border(1, ui.RGBA(96, 165, 250, 0.30)).
						Children(func() {
							ui.Text(c, "NATIVE").FontSize(9).Bold().LetterSpacing(0.4).TextColor(colAccentText)
						})
				})
				ui.Text(c, "信封加密 · TOFU · 命令守卫").FontSize(11).TextColor(colTextDim)
			})
			if glowPrimaryButton(c, "+ 主机", 6, 11).Clicked() {
				a.touchActivity()
				a.openAddHostModal()
			}
		})

		// Host search box
		ui.Box(c).Padding(10, 12, 6, 12).Children(func() {
			sf := ui.SearchField(c.Key("host-search"), &a.hostQuery).
				Placeholder("搜索主机名称 / IP / 用户名").
				Background(colInputBg).Border(1, colBorder).Radius(7)
			if sf.Changed() {
				a.touchActivity()
			}
		})

		// Host list
		hosts := a.filteredHosts()
		ui.Scroll(c.Key("host-scroll")).Grow(1).Padding(6, 12, 12, 12).Gap(10).Children(func() {
			if len(hosts) == 0 {
				msg := "暂无主机，点击右上角「+ 主机」添加"
				if strings.TrimSpace(a.hostQuery) != "" {
					msg = "未找到匹配的主机"
				}
				ui.Column(c).Padding(28, 12).Center().Children(func() {
					ui.Text(c, msg).FontSize(12).TextColor(colTextDim).TextAlign(ui.Center)
				})
				return
			}
			for _, host := range hosts {
				h := host
				isActive := active != nil && active.kind == tabKindSSH && active.host.ID == h.ID
				cardBorder := colBorder
				cardBg := colCardBg
				if isActive {
					cardBorder = ui.RGBA(52, 211, 153, 0.55)
					cardBg = ui.Hex("#111c28")
				}
				openCount := a.openTabCountForHost(h.ID)
				fpTag := "待首次验证"
				fpColor := colTextDim
				if h.KnownHostFP != "" {
					fpTag = "已绑定指纹"
					fpColor = colTextMuted
				}
				if openCount > 0 {
					fpTag = fmt.Sprintf("%s · 已开 %d 个会话", fpTag, openCount)
				}

				authLabel := "密码"
				authColor := colWarn
				authBg := colWarnBg
				authBorder := colWarnBorder
				if h.AuthType == store.AuthPrivateKey {
					authLabel = "公私钥"
					authColor = colAccentText
					authBg = ui.RGBA(59, 130, 246, 0.12)
					authBorder = ui.RGBA(96, 165, 250, 0.34)
				}

				title := h.Name
				if h.GuardEnabled {
					title += " [守卫]"
				}

				card := ui.Column(c.Key(h.ID)).Padding(12, 12).Gap(5).Radius(10).
					Background(cardBg).Border(1, cardBorder).
					Shadow(0, 4, 16, 0, ui.RGBA(0, 0, 0, 0.28))
				if !isActive && card.Hovered() {
					card.Background(colCardBgHover).Border(1, colBorderStrong)
				}
				card.Children(func() {
					ui.Row(c).Justify(ui.SpaceBetween).AlignItems(ui.Center).Gap(8).Children(func() {
						ui.Text(c, title).FontSize(13).Bold().TextColor(colTextPrimary).SingleLine().Shrink(1)
						ui.Box(c).Padding(2, 7).Radius(5).Background(authBg).Border(1, authBorder).Shrink(0).Children(func() {
							ui.Text(c, authLabel).FontSize(10).FontWeight(500).TextColor(authColor).SingleLine()
						})
					})

					ui.Text(c, formatHostEndpoint(h)).
						FontSize(12).TextColor(colTextMuted).SingleLine()

					ui.Row(c).Gap(5).AlignItems(ui.Center).Margin(1, 0, 0, 0).Children(func() {
						ui.Icon(c, iconFingerprintSVG).Size(13, 13).TextColor(fpColor)
						ui.Text(c, fpTag).FontSize(11).TextColor(fpColor).SingleLine()
					})

					ui.Row(c).Gap(7).Margin(6, 0, 0, 0).Children(func() {
						glowPrimaryButton(c, "连接", 5, 11).OnClick(func() {
							a.connectHost(h.ID)
						})
						subtleButton(c, "编辑", 5, 11).OnClick(func() {
							a.touchActivity()
							a.openEditHostModal(h)
						})
						dangerActionButton(c, "删除", 5, 11).OnClick(func() {
							a.touchActivity()
							target := h
							a.deleteTarget = &target
							a.deleteAsk = true
						})
					})
				})
			}
		})

		// Sidebar footer icon bar (matches redesigned 4-item vertical icon navigation)
		ui.Row(c).Padding(6, 8).Gap(2).AlignItems(ui.Center).
			Background(colSidebarBg).
			BorderWidth(1, 0, 0, 0).BorderColor(colDivider).Children(func() {
			if sidebarFooterItem(c, iconVideoCamSVG, "录像").Clicked() {
				a.touchActivity()
				a.openRecordingsModal()
			}
			if sidebarFooterItem(c, iconAuditDocSVG, "审计").Clicked() {
				a.touchActivity()
				a.openAuditModal()
			}
			if sidebarFooterItem(c, iconDatabaseSVG, "数据目录").Clicked() {
				a.touchActivity()
				go func() {
					if err := a.openDirFn(a.dataDir); err != nil {
						a.runUI(func() {
							a.pushToast("无法打开数据目录", err.Error(), "error")
						})
					}
				}()
			}
			if sidebarFooterItem(c, iconLockSVG, "锁定").Clicked() {
				a.LockVault("")
			}
		})
	})
}
