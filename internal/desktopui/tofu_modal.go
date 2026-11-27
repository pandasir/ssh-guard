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
		backdrop.Background(ui.RGBA(0, 0, 0, 0.72))
		panel.Width(540).Padding(20).Gap(12).Radius(10).
			Background(ui.Hex("#161b22")).Border(1, ui.Hex("#30363d"))

		titleColor := ui.Hex("#e6edf3")
		if item.isMismatch {
			titleColor = ui.Hex("#ff7b72")
		}
		ui.Text(c, title).FontSize(16).Bold().TextColor(titleColor)
		ui.Text(c, item.message).FontSize(13).TextColor(ui.Hex("#e6edf3"))

		ui.Column(c).Padding(12).Gap(6).Radius(6).
			Background(ui.Hex("#0d1117")).Border(1, ui.Hex("#30363d")).Children(func() {
			ui.Text(c, "目标主机: "+item.host.Name+" ("+item.host.Address+")").FontSize(12).TextColor(ui.Hex("#8b949e"))
			ui.Text(c, "密钥类型: "+item.keyType).FontSize(12)
			if item.isMismatch {
				ui.Text(c, "历史已信任指纹: "+item.expectedFP).FontSize(12).TextColor(ui.Hex("#e3b341"))
			}
			ui.Text(c, "当前收到指纹: "+item.fingerprint).FontSize(12).TextColor(ui.Hex("#79c0ff"))
		})

		ui.Text(c, "可在目标服务器执行 ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub 核对指纹是否一致。").
			FontSize(11).TextColor(ui.Hex("#8b949e"))

		if a.tofuErr != "" {
			ui.Box(c).Padding(8, 10).Radius(6).
				Background(ui.RGBA(248, 81, 73, 0.14)).Border(1, ui.RGBA(248, 81, 73, 0.4)).
				Children(func() {
					ui.Text(c, a.tofuErr).FontSize(12).TextColor(ui.Hex("#ff7b72"))
				})
		}

		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if ui.Button(c, "取消连接").Clicked() {
				a.dismissActiveTofu()
			}
			if item.isMismatch {
				if ui.Button(c, trustLabel).Disabled(a.tofuBusy).
					Background(ui.RGBA(248, 81, 73, 0.22)).Border(1, ui.RGBA(248, 81, 73, 0.55)).
					TextColor(ui.Hex("#ff7b72")).Clicked() {
					a.confirmActiveTofu()
				}
			} else {
				if ui.PrimaryButton(c, trustLabel).Disabled(a.tofuBusy).Clicked() {
					a.confirmActiveTofu()
				}
			}
		})
	})

	if m.Dismissed() {
		a.dismissActiveTofu()
	}
}
