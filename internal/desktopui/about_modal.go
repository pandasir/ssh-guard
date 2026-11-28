package desktopui

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const appFallbackVersion = "0.8.0"

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
		styleModalPanel(backdrop, panel, 460)

		ui.Row(c).Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "关于 SSH-Guard").FontSize(16).Bold().TextColor(colTextPrimary)
			ui.Box(c).Padding(2, 8).Radius(999).
				Background(ui.RGBA(59, 130, 246, 0.14)).Border(1, ui.RGBA(96, 165, 250, 0.34)).
				Children(func() {
					ui.Text(c, "v"+appDisplayVersion()).FontSize(11).Bold().TextColor(colAccentText)
				})
		})

		ui.Text(c, "零信任离线 SSH 原生桌面客户端").FontSize(12).TextColor(colTextMuted)

		ui.Column(c).Padding(10, 12).Gap(5).Radius(8).
			Background(colInputBg).Border(1, colBorder).Children(func() {
			ui.Text(c, fmt.Sprintf("环境: %s · %s", renderBackendLabel(), a.platform)).FontSize(12).TextColor(colTextSecondary)
			ui.Text(c, fmt.Sprintf("数据目录: %s", a.dataDir)).FontSize(12).TextColor(colTextMuted)
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
		ui.Row(c).Padding(10, 12).Gap(8).AlignItems(ui.Center).Radius(8).
			Background(colInputBg).Border(1, colBorder).Children(func() {
			ui.Spinner(c).Label("正在检查更新")
			ui.Text(c, "正在检查最新版本...").FontSize(12).TextColor(colTextSecondary)
		})

	case updateUpToDate:
		ui.Box(c).Padding(10, 12).Radius(8).
			Background(colSuccessBg).Border(1, colSuccessBorder).
			Children(func() {
				ui.Text(c, fmt.Sprintf("当前已是最新版本 (v%s)", appDisplayVersion())).
					FontSize(12).TextColor(colSuccess)
			})

	case updateAvailable:
		offer := a.update.offer
		if offer == nil {
			return
		}
		ui.Column(c).Padding(10, 12).Gap(8).Radius(8).
			Background(colInputBg).Border(1, colBorder).Children(func() {
			ui.Row(c).Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "发现新版本 v"+offer.Version).FontSize(13).Bold().TextColor(colAccentText)
				if !offer.Date.IsZero() {
					ui.Text(c, offer.Date.Format("2006-01-02")).FontSize(11).TextColor(colTextMuted)
				}
			})
			if offer.Notes != "" {
				ui.Scroll(c).MaxHeight(120).Padding(8, 10).Radius(6).
					Background(colCardBg).Border(1, colBorder).Children(func() {
					ui.Text(c, offer.Notes).FontSize(12).TextColor(colTextSecondary)
				})
			}
			if !offer.CanInstall && offer.FallbackReason != "" {
				ui.Box(c).Padding(8, 10).Radius(6).
					Background(colWarnBg).Border(1, colWarnBorder).
					Children(func() {
						ui.Text(c, offer.FallbackReason).FontSize(11).TextColor(colWarn)
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

		ui.Column(c).Padding(10, 12).Gap(8).Radius(8).
			Background(colInputBg).Border(1, colBorder).Children(func() {
			ui.Row(c).Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "正在下载并校验 v"+ver).FontSize(12).Bold().TextColor(colTextPrimary)
				ui.Text(c, progressText).FontSize(11).TextColor(colTextMuted)
			})
			ui.Progress(c, progressVal).Label("更新下载进度")
		})

	case updateReady:
		ver := a.update.installedVersion
		if ver == "" && a.update.offer != nil {
			ver = a.update.offer.Version
		}
		ui.Column(c).Padding(10, 12).Gap(8).Radius(8).
			Background(colSuccessBg).Border(1, colSuccessBorder).
			Children(func() {
				ui.Text(c, fmt.Sprintf("新版本 v%s 已下载并通过签名校验，重启即可完成更新。", ver)).
					FontSize(12).Bold().TextColor(colSuccess)
				if active := a.activeSessionCount(); active > 0 {
					ui.Box(c).Padding(8, 10).Radius(6).
						Background(colWarnBg).Border(1, colWarnBorder).
						Children(func() {
							ui.Text(c, fmt.Sprintf("注意：当前有 %d 个活跃终端会话，重启将断开这些会话并锁定凭证库。", active)).
								FontSize(11).TextColor(colWarn)
						})
				}
			})

	case updateError:
		ui.Box(c).Padding(10, 12).Radius(8).
			Background(colDangerBg).Border(1, colDangerBorder).
			Children(func() {
				ui.Text(c, a.update.errMsg).FontSize(12).TextColor(colDanger)
			})
	}
}

func (a *App) renderAboutFooter(c *ui.Context) {
	ui.Row(c).Justify(ui.SpaceBetween).AlignItems(ui.Center).Gap(8).Children(func() {
		// Left-aligned update secondary action(s).
		ui.Row(c).Gap(8).Children(func() {
			switch a.update.phase {
			case updateIdle:
				if subtleButton(c, "检查更新", 5, 12).Clicked() {
					a.startUpdateCheck()
				}
			case updateChecking:
				subtleButton(c, "检查中...", 5, 12).Disabled(true)
			case updateUpToDate:
				if subtleButton(c, "重新检查", 5, 12).Clicked() {
					a.startUpdateCheck()
				}
			case updateAvailable:
				if subtleButton(c, "重新检查", 5, 12).Clicked() {
					a.startUpdateCheck()
				}
			case updateDownloading:
				if subtleButton(c, "取消下载", 5, 12).Clicked() {
					a.cancelUpdateInstall()
				}
			case updateError:
				if subtleButton(c, "重试", 5, 12).Clicked() {
					if a.update.offer != nil && a.update.offer.CanInstall {
						a.startUpdateInstall()
					} else {
						a.startUpdateCheck()
					}
				}
				if a.update.offer != nil && a.update.offer.CanInstall {
					if subtleButton(c, "前往 GitHub 下载", 5, 12).Clicked() {
						a.openUpdateReleasePage()
					}
				}
			}
		})

		// Right-aligned primary action + dismiss.
		ui.Row(c).Gap(8).Children(func() {
			switch a.update.phase {
			case updateAvailable:
				if subtleButton(c, "关闭", 5, 14).Clicked() {
					a.closeAboutModal()
				}
				if a.update.offer != nil && a.update.offer.CanInstall {
					if glowPrimaryButton(c, "立即更新", 5, 14).Clicked() {
						a.startUpdateInstall()
					}
				} else {
					if glowPrimaryButton(c, "前往 GitHub 下载", 5, 14).Clicked() {
						a.openUpdateReleasePage()
					}
				}
			case updateReady:
				if subtleButton(c, "关闭", 5, 14).Clicked() {
					a.closeAboutModal()
				}
				if glowPrimaryButton(c, "立即重启并更新", 5, 14).Clicked() {
					a.restartToApplyUpdateLocked(a.win == nil)
				}
			default:
				if glowPrimaryButton(c, "关闭", 5, 14).Clicked() {
					a.closeAboutModal()
				}
			}
		})
	})
}
