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

func (a *App) renderSidebar(c *ui.Context) {
	active := a.activeSessionTab()

	ui.Column(c).Width(300).FillHeight().Background(ui.Hex("#161b22")).
		BorderWidth(0, 1, 0, 0).BorderColor(ui.Hex("#30363d")).Children(func() {
		// Sidebar header
		ui.Row(c).Padding(12, 14).Justify(ui.SpaceBetween).AlignItems(ui.Center).
			BorderWidth(0, 0, 1, 0).BorderColor(ui.Hex("#30363d")).Children(func() {
			ui.Column(c).Gap(2).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "SSH-Guard").FontSize(16).Bold()
					ui.Box(c).Padding(2, 6).Radius(4).
						Background(ui.RGBA(56, 139, 253, 0.15)).Border(1, ui.RGBA(56, 139, 253, 0.35)).
						Children(func() {
							ui.Text(c, "NATIVE").FontSize(9).Bold().TextColor(ui.Hex("#79c0ff"))
						})
				})
				ui.Text(c, "信封加密 · TOFU · 命令守卫").FontSize(11).TextColor(ui.Hex("#8b949e"))
			})
			ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
				if ui.Button(c, ">_ 终端").Padding(5, 8).Clicked() {
					a.openLocalTerminal()
				}
				if ui.PrimaryButton(c, "+ 主机").Padding(5, 10).Clicked() {
					a.touchActivity()
					a.openAddHostModal()
				}
			})
		})

		// Host search box
		ui.Box(c).Padding(8, 10, 4, 10).Children(func() {
			if ui.SearchField(c.Key("host-search"), &a.hostQuery).Placeholder("搜索主机名称 / IP / 用户名").Changed() {
				a.touchActivity()
			}
		})

		// Host list
		hosts := a.filteredHosts()
		ui.Scroll(c.Key("host-scroll")).Grow(1).Padding(8, 10).Gap(8).Children(func() {
			if len(hosts) == 0 {
				msg := "暂无主机，点击右上角「+ 主机」添加"
				if strings.TrimSpace(a.hostQuery) != "" {
					msg = "未找到匹配的主机"
				}
				ui.Column(c).Padding(20, 8).Center().Children(func() {
					ui.Text(c, msg).FontSize(12).TextColor(ui.Hex("#8b949e"))
				})
				return
			}
			for _, host := range hosts {
				h := host
				isActive := active != nil && active.kind == tabKindSSH && active.host.ID == h.ID
				border := ui.Hex("#30363d")
				if isActive {
					border = ui.Hex("#3fb950")
				}
				openCount := a.openTabCountForHost(h.ID)
				fpTag := "待首次验证"
				if h.KnownHostFP != "" {
					fpTag = "已绑定指纹"
				}
				if openCount > 0 {
					fpTag = fmt.Sprintf("%s · 已开 %d 个会话", fpTag, openCount)
				}
				authLabel := "密码"
				authColor := ui.Hex("#e3b341")
				authBorder := ui.RGBA(210, 153, 34, 0.4)
				if h.AuthType == store.AuthPrivateKey {
					authLabel = "公私钥"
					authColor = ui.Hex("#79c0ff")
					authBorder = ui.RGBA(56, 139, 253, 0.4)
				}
				title := h.Name
				if h.GuardEnabled {
					title += " [守卫]"
				}

				ui.Column(c.Key(h.ID)).Padding(10, 12).Gap(5).Radius(8).
					Background(ui.Hex("#0d1117")).Border(1, border).Children(func() {
					ui.Row(c).Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
						ui.Text(c, title).FontSize(13).Bold().SingleLine()
						ui.Box(c).Padding(2, 6).Radius(999).Border(1, authBorder).Children(func() {
							ui.Text(c, authLabel).FontSize(10).TextColor(authColor)
						})
					})
					ui.Text(c, fmt.Sprintf("%s@%s:%d", h.Username, h.Address, h.Port)).
						FontSize(12).TextColor(ui.Hex("#8b949e")).SingleLine()
					ui.Text(c, fpTag).FontSize(11).TextColor(ui.Hex("#8b949e")).SingleLine()

					ui.Row(c).Gap(6).Margin(4, 0, 0, 0).Children(func() {
						ui.PrimaryButton(c, "连接").Padding(4, 10).OnClick(func() {
							a.connectHost(h.ID)
						})
						ui.Button(c, "编辑").Padding(4, 10).OnClick(func() {
							a.touchActivity()
							a.openEditHostModal(h)
						})
						ui.Button(c, "删除").Padding(4, 10).
							Background(ui.RGBA(248, 81, 73, 0.14)).Border(1, ui.RGBA(248, 81, 73, 0.4)).
							TextColor(ui.Hex("#ff7b72")).OnClick(func() {
							a.touchActivity()
							target := h
							a.deleteTarget = &target
							a.deleteAsk = true
						})
					})
				})
			}
		})

		// Sidebar footer actions
		ui.Row(c).Padding(10, 10).Gap(6).Justify(ui.SpaceBetween).
			BorderWidth(1, 0, 0, 0).BorderColor(ui.Hex("#30363d")).Children(func() {
			if ui.Button(c, "录像").Padding(5, 8).Clicked() {
				a.touchActivity()
				a.openRecordingsModal()
			}
			if ui.Button(c, "审计").Padding(5, 8).Clicked() {
				a.touchActivity()
				a.openAuditModal()
			}
			if ui.Button(c, "数据目录").Padding(5, 8).Clicked() {
				a.touchActivity()
				go func() {
					if err := a.openDirFn(a.dataDir); err != nil {
						a.runUI(func() {
							a.pushToast("无法打开数据目录", err.Error(), "error")
						})
					}
				}()
			}
			if ui.Button(c, "锁定").Padding(5, 8).
				Background(ui.RGBA(248, 81, 73, 0.15)).Border(1, ui.RGBA(248, 81, 73, 0.4)).
				TextColor(ui.Hex("#ff7b72")).Clicked() {
				a.LockVault("")
			}
		})
	})
}
