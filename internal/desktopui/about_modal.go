package desktopui

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const appFallbackVersion = "0.7.0"

func appDisplayVersion() string {
	if v := strings.TrimSpace(mygo.App.Version()); v != "" {
		return v
	}
	return appFallbackVersion
}

func (a *App) openAboutModal() {
	a.aboutOpen = true
}

func (a *App) renderAboutModal(c *ui.Context) {
	if !a.aboutOpen {
		return
	}

	m := ui.DialogBase(c, &a.aboutOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.72))
		panel.Width(460).Padding(20).Gap(10).Radius(10).
			Background(ui.Hex("#161b22")).Border(1, ui.Hex("#30363d"))

		ui.Row(c).Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "关于 SSH-Guard").FontSize(16).Bold().TextColor(ui.Hex("#e6edf3"))
			ui.Box(c).Padding(2, 8).Radius(999).
				Background(ui.RGBA(56, 139, 253, 0.15)).Border(1, ui.RGBA(56, 139, 253, 0.35)).
				Children(func() {
					ui.Text(c, "v"+appDisplayVersion()).FontSize(11).Bold().TextColor(ui.Hex("#79c0ff"))
				})
		})

		ui.Text(c, "零信任离线 SSH 原生桌面客户端").FontSize(12).TextColor(ui.Hex("#8b949e"))

		ui.Column(c).Padding(10, 12).Gap(5).Radius(6).
			Background(ui.Hex("#0d1117")).Border(1, ui.Hex("#30363d")).Children(func() {
			ui.Text(c, fmt.Sprintf("环境: %s · %s", renderBackendLabel(), a.platform)).FontSize(12)
			ui.Text(c, fmt.Sprintf("数据目录: %s", a.dataDir)).FontSize(12).TextColor(ui.Hex("#8b949e"))
		})

		a.renderAboutUpdatePanel(c)
		a.renderAboutFooter(c)
	})

	if m.Dismissed() {
		a.closeAboutModal()
	}
}

func (a *App) renderAboutUpdatePanel(c *ui.Context) {
	switch a.update.phase {
	case updateChecking:
		ui.Row(c).Padding(10, 12).Gap(8).AlignItems(ui.Center).Radius(6).
			Background(ui.Hex("#0d1117")).Border(1, ui.Hex("#30363d")).Children(func() {
			ui.Spinner(c).Label("正在检查更新")
			ui.Text(c, "正在检查最新版本...").FontSize(12).TextColor(ui.Hex("#c9d1d9"))
		})

	case updateUpToDate:
		ui.Box(c).Padding(10, 12).Radius(6).
			Background(ui.RGBA(46, 160, 67, 0.12)).Border(1, ui.RGBA(46, 160, 67, 0.4)).
			Children(func() {
				ui.Text(c, fmt.Sprintf("当前已是最新版本 (v%s)", appDisplayVersion())).
					FontSize(12).TextColor(ui.Hex("#3fb950"))
			})

	case updateAvailable:
		offer := a.update.offer
		if offer == nil {
			return
		}
		ui.Column(c).Padding(10, 12).Gap(8).Radius(6).
			Background(ui.Hex("#0d1117")).Border(1, ui.Hex("#30363d")).Children(func() {
			ui.Row(c).Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "发现新版本 v"+offer.Version).FontSize(13).Bold().TextColor(ui.Hex("#79c0ff"))
				if !offer.Date.IsZero() {
					ui.Text(c, offer.Date.Format("2006-01-02")).FontSize(11).TextColor(ui.Hex("#8b949e"))
				}
			})
			if offer.Notes != "" {
				ui.Scroll(c).MaxHeight(120).Padding(8, 10).Radius(4).
					Background(ui.Hex("#161b22")).Border(1, ui.Hex("#21262d")).Children(func() {
					ui.Text(c, offer.Notes).FontSize(12).TextColor(ui.Hex("#c9d1d9"))
				})
			}
			if !offer.CanInstall && offer.FallbackReason != "" {
				ui.Box(c).Padding(8, 10).Radius(4).
					Background(ui.RGBA(210, 153, 34, 0.14)).Border(1, ui.RGBA(210, 153, 34, 0.4)).
					Children(func() {
						ui.Text(c, offer.FallbackReason).FontSize(11).TextColor(ui.Hex("#e3b341"))
					})
			}
		})

	case updateDownloading:
		ver := ""
		if a.update.offer != nil {
			ver = a.update.offer.Version
		}
		progressVal := -1.0
		progressText := "正在连接下载服务器并校验签名..."
		if a.update.total > 0 {
			progressVal = float64(a.update.downloaded) / float64(a.update.total)
			if progressVal < 0 {
				progressVal = 0
			}
			if progressVal > 1 {
				progressVal = 1
			}
			pct := int(progressVal * 100)
			progressText = fmt.Sprintf("%d%% (%s / %s)", pct, formatUpdateBytes(a.update.downloaded), formatUpdateBytes(a.update.total))
		} else if a.update.downloaded > 0 {
			progressText = fmt.Sprintf("已下载 %s...", formatUpdateBytes(a.update.downloaded))
		}

		ui.Column(c).Padding(10, 12).Gap(8).Radius(6).
			Background(ui.Hex("#0d1117")).Border(1, ui.Hex("#30363d")).Children(func() {
			ui.Row(c).Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "正在下载并校验 v"+ver).FontSize(12).Bold().TextColor(ui.Hex("#e6edf3"))
				ui.Text(c, progressText).FontSize(11).TextColor(ui.Hex("#8b949e"))
			})
			ui.Progress(c, progressVal).Label("更新下载进度")
		})

	case updateReady:
		ver := a.update.installedVersion
		if ver == "" && a.update.offer != nil {
			ver = a.update.offer.Version
		}
		ui.Column(c).Padding(10, 12).Gap(8).Radius(6).
			Background(ui.RGBA(46, 160, 67, 0.12)).Border(1, ui.RGBA(46, 160, 67, 0.4)).
			Children(func() {
				ui.Text(c, fmt.Sprintf("新版本 v%s 已下载并通过签名校验，重启即可完成更新。", ver)).
					FontSize(12).Bold().TextColor(ui.Hex("#3fb950"))
				if active := a.activeSessionCount(); active > 0 {
					ui.Box(c).Padding(8, 10).Radius(4).
						Background(ui.RGBA(210, 153, 34, 0.16)).Border(1, ui.RGBA(210, 153, 34, 0.45)).
						Children(func() {
							ui.Text(c, fmt.Sprintf("注意：当前有 %d 个活跃终端会话，重启将断开这些会话并锁定凭证库。", active)).
								FontSize(11).TextColor(ui.Hex("#e3b341"))
						})
				}
			})

	case updateError:
		ui.Box(c).Padding(10, 12).Radius(6).
			Background(ui.RGBA(248, 81, 73, 0.14)).Border(1, ui.RGBA(248, 81, 73, 0.4)).
			Children(func() {
				ui.Text(c, a.update.errMsg).FontSize(12).TextColor(ui.Hex("#ff7b72"))
			})
	}
}

func (a *App) renderAboutFooter(c *ui.Context) {
	ui.Row(c).Justify(ui.SpaceBetween).AlignItems(ui.Center).Gap(8).Children(func() {
		// Left-aligned update secondary action(s).
		ui.Row(c).Gap(8).Children(func() {
			switch a.update.phase {
			case updateIdle:
				if ui.Button(c, "检查更新").Padding(5, 12).Clicked() {
					a.startUpdateCheck()
				}
			case updateChecking:
				ui.Button(c, "检查中...").Disabled(true).Padding(5, 12)
			case updateUpToDate:
				if ui.Button(c, "重新检查").Padding(5, 12).Clicked() {
					a.startUpdateCheck()
				}
			case updateAvailable:
				if ui.Button(c, "重新检查").Padding(5, 12).Clicked() {
					a.startUpdateCheck()
				}
			case updateDownloading:
				if ui.Button(c, "取消下载").Padding(5, 12).Clicked() {
					a.cancelUpdateInstall()
				}
			case updateError:
				if ui.Button(c, "重试").Padding(5, 12).Clicked() {
					if a.update.offer != nil && a.update.offer.CanInstall {
						a.startUpdateInstall()
					} else {
						a.startUpdateCheck()
					}
				}
				if a.update.offer != nil && a.update.offer.CanInstall {
					if ui.Button(c, "前往 GitHub 下载").Padding(5, 12).Clicked() {
						a.openUpdateReleasePage()
					}
				}
			}
		})

		// Right-aligned primary action + dismiss.
		ui.Row(c).Gap(8).Children(func() {
			switch a.update.phase {
			case updateAvailable:
				if ui.Button(c, "关闭").Padding(5, 14).Clicked() {
					a.closeAboutModal()
				}
				if a.update.offer != nil && a.update.offer.CanInstall {
					if ui.PrimaryButton(c, "立即更新").Padding(5, 14).Clicked() {
						a.startUpdateInstall()
					}
				} else {
					if ui.PrimaryButton(c, "前往 GitHub 下载").Padding(5, 14).Clicked() {
						a.openUpdateReleasePage()
					}
				}
			case updateReady:
				if ui.Button(c, "关闭").Padding(5, 14).Clicked() {
					a.closeAboutModal()
				}
				if ui.PrimaryButton(c, "立即重启并更新").Padding(5, 14).Clicked() {
					a.restartToApplyUpdateLocked(a.win == nil)
				}
			default:
				if ui.PrimaryButton(c, "关闭").Padding(5, 14).Clicked() {
					a.closeAboutModal()
				}
			}
		})
	})
}
