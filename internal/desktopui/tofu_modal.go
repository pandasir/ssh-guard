package desktopui

import (
	"ssh-guard/internal/store"

	"github.com/egoist/mygo/ui"
)

type tofuItem struct {
	tabID        int
	host         store.Host
	keyType      string
	fingerprint  string
	publicKeyB64 string
	expectedFP   string
	message      string
	isMismatch   bool
}

func (a *App) enqueueTofu(item *tofuItem) {
	if item == nil {
		return
	}
	if a.tofuPending == nil {
		a.tofuPending = item
		a.tofuOpen = true
		a.tofuErr = ""
		a.tofuBusy = false
		return
	}
	a.tofuQueue = append(a.tofuQueue, item)
}

func (a *App) removeTofuForTab(tabID int) {
	filtered := a.tofuQueue[:0]
	for _, item := range a.tofuQueue {
		if item.tabID != tabID {
			filtered = append(filtered, item)
		}
	}
	a.tofuQueue = filtered

	if a.tofuPending != nil && a.tofuPending.tabID == tabID {
		a.tofuPending = nil
		a.tofuOpen = false
		a.tofuErr = ""
		a.advanceTofuQueue()
	}
}

func (a *App) advanceTofuQueue() {
	if a.tofuPending != nil {
		return
	}
	for len(a.tofuQueue) > 0 {
		next := a.tofuQueue[0]
		a.tofuQueue = a.tofuQueue[1:]
		if a.findTabByID(next.tabID) != nil {
			a.tofuPending = next
			a.tofuOpen = true
			a.tofuErr = ""
			a.tofuBusy = false
			return
		}
	}
}

func (a *App) dismissActiveTofu() {
	a.tofuPending = nil
	a.tofuOpen = false
	a.tofuErr = ""
	a.tofuBusy = false
	a.advanceTofuQueue()
}

func (a *App) confirmActiveTofu() {
	if a.tofuPending == nil || a.tofuBusy {
		return
	}
	a.touchActivity()
	item := a.tofuPending
	a.tofuBusy = true
	a.tofuErr = ""

	if err := a.store.UpsertKnownHost(&store.KnownHost{
		HostID:          item.host.ID,
		KeyType:         item.keyType,
		Fingerprint:     item.fingerprint,
		PublicKeyBase64: item.publicKeyB64,
	}); err != nil {
		a.tofuBusy = false
		a.tofuErr = "保存信任指纹失败: " + err.Error()
		return
	}
	a.store.AddAuditLog(item.host.ID, item.host.Name, "TOFU_TRUST", "INFO", "desktop-local", "已信任主机公钥指纹: "+item.fingerprint)
	_ = a.loadHosts()

	tab := a.findTabByID(item.tabID)
	a.dismissActiveTofu()
	if tab != nil && !tab.closing {
		a.reconnectTab(tab)
	}
}

func (a *App) renderTofuModal(c *ui.Context) {
	if a.tofuPending == nil {
		a.tofuOpen = false
		return
	}
	item := a.tofuPending

	title := "首次连接主机公钥验证 (TOFU)"
	trustLabel := "确认指纹无误并信任"
	if item.isMismatch {
		title = "警告：主机公钥指纹变更 (疑似 MITM)"
		trustLabel = "我已核实风险，覆盖信任新指纹"
	}

	m := ui.DialogBase(c, &a.tofuOpen, func(backdrop, panel ui.Element) {
		styleModalPanel(backdrop, panel, 540)

		titleColor := colTextPrimary
		if item.isMismatch {
			titleColor = colDanger
		}
		ui.Text(c, title).FontSize(16).Bold().TextColor(titleColor)
		ui.Text(c, item.message).FontSize(13).LineHeight(1.5).TextColor(colTextSecondary)

		ui.Column(c).Padding(12).Gap(6).Radius(8).
			Background(colInputBg).Border(1, colBorder).Children(func() {
			ui.Text(c, "目标主机: "+item.host.Name+" ("+item.host.Address+")").FontSize(12).TextColor(colTextMuted)
			ui.Text(c, "密钥类型: "+item.keyType).FontSize(12).TextColor(colTextSecondary)
			if item.isMismatch {
				ui.Text(c, "历史已信任指纹: "+item.expectedFP).FontSize(12).TextColor(colWarn)
			}
			ui.Text(c, "当前收到指纹: "+item.fingerprint).FontSize(12).TextColor(colAccentText)
		})

		ui.Text(c, "可在目标服务器执行 ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub 核对指纹是否一致。").
			FontSize(11).TextColor(colTextDim)

		if a.tofuErr != "" {
			ui.Box(c).Padding(8, 10).Radius(7).
				Background(colDangerBg).Border(1, colDangerBorder).
				Children(func() {
					ui.Text(c, a.tofuErr).FontSize(12).TextColor(colDanger)
				})
		}

		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if subtleButton(c, "取消连接", 6, 14).Clicked() {
				a.dismissActiveTofu()
			}
			if item.isMismatch {
				if dangerActionButton(c, trustLabel, 6, 14).Disabled(a.tofuBusy).Clicked() {
					a.confirmActiveTofu()
				}
			} else {
				if glowPrimaryButton(c, trustLabel, 6, 16).Disabled(a.tofuBusy).Clicked() {
					a.confirmActiveTofu()
				}
			}
		})
	})

	if m.Dismissed() {
		a.dismissActiveTofu()
	}
}
