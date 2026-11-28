package desktopui

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

func (a *App) clearUnlockInputs() {
	a.unlockPass = ""
	a.unlockConfirm = ""
	a.unlockGen++
}

func (a *App) submitUnlock() {
	pw := a.unlockPass
	confirm := a.unlockConfirm
	// Immediately discard bound plaintext secrets & rotate input keys so Undo cannot restore them.
	a.clearUnlockInputs()
	a.unlockErr = ""
	a.unlockInfo = ""

	if !a.initialized {
		if len(pw) < 8 {
			a.unlockErr = "主密码长度至少需要 8 位"
			return
		}
		if pw != confirm {
			a.unlockErr = "两次输入的主密码不一致"
			return
		}
		meta, err := a.vault.Setup(pw)
		if err != nil {
			a.unlockErr = err.Error()
			return
		}
		if err := a.store.SaveVaultMeta(meta); err != nil {
			a.vault.Lock()
			a.unlockErr = "保存主密钥元数据失败: " + err.Error()
			return
		}
		a.store.AddAuditLog(0, "Vault", "VAULT_SETUP", "INFO", "desktop-local", "初始化主密钥并解锁凭证库")
		a.initialized = true
	} else {
		meta, err := a.store.GetVaultMeta()
		if err != nil {
			a.unlockErr = "读取主密钥元数据失败: " + err.Error()
			return
		}
		if err := a.vault.Unlock(pw, meta); err != nil {
			a.store.AddAuditLog(0, "Vault", "VAULT_UNLOCK_FAILED", "WARN", "desktop-local", "主密码校验失败")
			a.unlockErr = "主密码错误"
			return
		}
		a.store.AddAuditLog(0, "Vault", "VAULT_UNLOCK", "INFO", "desktop-local", "主密钥已解锁至内存")
	}

	a.unlocked = true
	if err := a.loadHosts(); err != nil {
		a.pushToast("加载主机列表失败", err.Error(), "error")
	}
	if a.idle != nil {
		a.idle.Start(func(reason string) {
			a.runUI(func() {
				if a.unlocked {
					a.LockVault(reason)
				}
			})
		})
	}
}

func (a *App) renderUnlockView(c *ui.Context) {
	title := "解锁凭证保险库"
	btnLabel := "解锁进入面板"
	if !a.initialized {
		title = "初始化主密钥 (首次启动)"
		btnLabel = "创建保险库并进入"
	}

	ui.Column(c).Fill().Center().Background(colCanvasBg).
		RadialGradient(ui.RadialGradient{
			Center: ui.Point{X: 0.5, Y: 0.42},
			Radius: 420,
			Oklab:  true,
			Stops: []ui.GradientStop{
				{Color: ui.RGBA(26, 72, 164, 0.24), At: 0},
				{Color: ui.RGBA(14, 34, 78, 0.10), At: 0.48},
				{Color: colCanvasBg, At: 1},
			},
		}).Children(func() {
		ui.Column(c).Width(430).Padding(28).Gap(15).Radius(14).
			Background(colModalBg).Border(1, colBorderStrong).
			Shadow(0, 24, 60, 0, ui.RGBA(0, 0, 0, 0.58)).
			Children(func() {
				// Brand row
				ui.Row(c).Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Box(c).Size(30, 30).Radius(8).Center().
							LinearGradient(ui.LinearGradient{
								From:  ui.RGBA(77, 143, 248, 0.26),
								To:    ui.RGBA(31, 91, 212, 0.34),
								Angle: 145,
							}).
							Border(1, ui.RGBA(147, 197, 253, 0.32)).
							Children(func() {
								ui.Icon(c, iconShieldLockSVG).Size(16, 16).TextColor(colAccentText)
							})
						ui.Box(c).Padding(3, 9).Radius(999).
							Background(ui.RGBA(59, 130, 246, 0.14)).Border(1, ui.RGBA(96, 165, 250, 0.34)).
							Children(func() {
								ui.Text(c, "SSH-GUARD").FontSize(11).Bold().LetterSpacing(0.4).TextColor(colAccentText)
							})
					})
					ui.Box(c).Padding(3, 9).Radius(999).
						Background(colSuccessBg).Border(1, colSuccessBorder).
						Children(func() {
							ui.Text(c, "NATIVE · "+strings.ToUpper(a.platform)).FontSize(10).Bold().TextColor(colSuccess)
						})
				})

				ui.Text(c, title).FontSize(20).Bold().TextColor(colTextPrimary)
				ui.Text(c, "主密码仅在内存中通过 Argon2id 派生 KEK，绝不写入磁盘。零 WebView 原生渲染，关闭应用或空闲超时后自动清零内存。").
					FontSize(12).LineHeight(1.55).TextColor(colTextMuted)

				if a.unlockInfo != "" {
					ui.Box(c).Padding(8, 10).Radius(7).
						Background(colWarnBg).Border(1, colWarnBorder).
						Children(func() {
							ui.Text(c, a.unlockInfo).FontSize(12).TextColor(colWarn)
						})
				}

				ui.Column(c).Gap(10).Children(func() {
					ui.Field(c, "主密码 (Master Password)", func() {
						// Intentionally omit AutoFocus so the lock screen does not start a 30s
						// 530 ms caret-blink redraw loop on launch that blocks 2s idle framebuffer
						// / Metal drawable trimming (+85 MiB on macOS Retina, +4-9 MiB on Windows).
						in := ui.TextInput(c.Key(fmt.Sprintf("unlock-pw-%d", a.unlockGen)), &a.unlockPass).
							Password().Placeholder("请输入至少 8 位主密码")
						if in.Submitted() && a.initialized {
							a.submitUnlock()
						}
					})

					if !a.initialized {
						ui.Field(c, "确认主密码", func() {
							in := ui.TextInput(c.Key(fmt.Sprintf("unlock-confirm-%d", a.unlockGen)), &a.unlockConfirm).
								Password().Placeholder("再次输入主密码")
							if in.Submitted() {
								a.submitUnlock()
							}
						})
					}
				})

				if a.unlockErr != "" {
					ui.Box(c).Padding(8, 10).Radius(7).
						Background(colDangerBg).Border(1, colDangerBorder).
						Children(func() {
							ui.Text(c, a.unlockErr).FontSize(12).TextColor(colDanger)
						})
				}

				if glowPrimaryButton(c, btnLabel, 9, 16).FillWidth().Clicked() {
					a.submitUnlock()
				}
			})
	})
}
