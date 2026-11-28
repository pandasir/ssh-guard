package desktopui

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const appFallbackVersion = "0.6.0"

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
		panel.Width(420).Padding(20).Gap(10).Radius(10).
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

		ui.Row(c).Justify(ui.End).Children(func() {
			if ui.PrimaryButton(c, "关闭").Padding(5, 14).Clicked() {
				a.aboutOpen = false
			}
		})
	})

	if m.Dismissed() {
		a.aboutOpen = false
	}
}
