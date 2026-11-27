package desktopui

import (
	"fmt"
	"strconv"
	"strings"

	"ssh-guard/internal/crypto"
	"ssh-guard/internal/store"

	"github.com/egoist/mygo/ui"
)

type hostFormState struct {
	editingID    int64 // 0 for new host
	name         string
	username     string
	address      string
	port         string
	authType     store.AuthType
	password     string
	privateKey   string
	passphrase   string
	guardEnabled bool
	notes        string
	errMsg       string
	pickingKey   bool
	secretGen    int
}

func (f *hostFormState) clearSecrets() {
	f.password = ""
	f.privateKey = ""
	f.passphrase = ""
	f.secretGen++
}

func (a *App) openAddHostModal() {
	a.hostModal.clearSecrets()
	a.hostModal.editingID = 0
	a.hostModal.name = ""
	a.hostModal.username = ""
	a.hostModal.address = ""
	a.hostModal.port = "22"
	a.hostModal.authType = store.AuthPrivateKey
	a.hostModal.guardEnabled = true
	a.hostModal.notes = ""
	a.hostModal.errMsg = ""
	a.hostModal.pickingKey = false
	a.hostOpen = true
}

func (a *App) openEditHostModal(h store.Host) {
	a.hostModal.clearSecrets()
	a.hostModal.editingID = h.ID
	a.hostModal.name = h.Name
	a.hostModal.username = h.Username
	a.hostModal.address = h.Address
	a.hostModal.port = strconv.Itoa(h.Port)
	a.hostModal.authType = h.AuthType
	a.hostModal.guardEnabled = h.GuardEnabled
	a.hostModal.notes = h.Notes
	a.hostModal.errMsg = ""
	a.hostModal.pickingKey = false
	a.hostOpen = true
}

func (a *App) closeHostModal() {
	a.hostOpen = false
	a.hostModal.clearSecrets()
	a.hostModal.errMsg = ""
}

func (a *App) saveHostForm() {
	a.touchActivity()
	f := &a.hostModal
	f.errMsg = ""

	name := strings.TrimSpace(f.name)
	username := strings.TrimSpace(f.username)
	address := strings.TrimSpace(f.address)
	notes := strings.TrimSpace(f.notes)
	port, err := strconv.Atoi(strings.TrimSpace(f.port))
	if err != nil || port <= 0 || port > 65535 {
		port = 22
	}
	authType := f.authType
	if authType != store.AuthPassword && authType != store.AuthPrivateKey {
		f.errMsg = "认证类型仅支持 password 或 private_key"
		return
	}

	secret := f.privateKey
	if authType == store.AuthPassword {
		secret = f.password
	}
	passphrase := f.passphrase

	if name == "" || address == "" || username == "" {
		f.errMsg = "名称、地址和用户名不能为空"
		return
	}

	if f.editingID == 0 {
		if secret == "" {
			f.errMsg = "新增主机时凭据不能为空"
			return
		}
		secBytes := []byte(secret)
		secEnv, err := a.vault.EncryptSecret(secBytes)
		crypto.Zeroize(secBytes)
		if err != nil {
			f.errMsg = "加密凭据失败: " + err.Error()
			return
		}

		passEnv := &crypto.Envelope{}
		if authType == store.AuthPrivateKey && passphrase != "" {
			passBytes := []byte(passphrase)
			passEnv, err = a.vault.EncryptSecret(passBytes)
			crypto.Zeroize(passBytes)
			if err != nil {
				f.errMsg = "加密私钥口令失败: " + err.Error()
				return
			}
		}

		h := &store.Host{
			Name:          name,
			Address:       address,
			Port:          port,
			Username:      username,
			AuthType:      authType,
			EncSecretDEK:  secEnv.EncDEK,
			EncSecretData: secEnv.EncSecret,
			EncPassDEK:    passEnv.EncDEK,
			EncPassData:   passEnv.EncSecret,
			GuardEnabled:  f.guardEnabled,
			Notes:         notes,
		}
		id, err := a.store.CreateHost(h)
		if err != nil {
			f.errMsg = "保存主机失败: " + err.Error()
			return
		}
		a.store.AddAuditLog(id, h.Name, "HOST_CREATE", "INFO", "desktop-local",
			fmt.Sprintf("新增主机 %s@%s:%d (%s)", h.Username, h.Address, h.Port, h.AuthType))
	} else {
		existing, err := a.store.GetHost(f.editingID)
		if err != nil {
			f.errMsg = "读取原主机配置失败: " + err.Error()
			return
		}
		existing.Name = name
		existing.Address = address
		existing.Port = port
		existing.Username = username
		existing.AuthType = authType
		existing.GuardEnabled = f.guardEnabled
		existing.Notes = notes

		if secret != "" {
			secBytes := []byte(secret)
			secEnv, err := a.vault.EncryptSecret(secBytes)
			crypto.Zeroize(secBytes)
			if err != nil {
				f.errMsg = "加密凭据失败: " + err.Error()
				return
			}
			existing.EncSecretDEK = secEnv.EncDEK
			existing.EncSecretData = secEnv.EncSecret
		}
		if authType == store.AuthPassword {
			existing.EncPassDEK = ""
			existing.EncPassData = ""
		} else if passphrase != "" {
			passBytes := []byte(passphrase)
			passEnv, err := a.vault.EncryptSecret(passBytes)
			crypto.Zeroize(passBytes)
			if err != nil {
				f.errMsg = "加密私钥口令失败: " + err.Error()
				return
			}
			existing.EncPassDEK = passEnv.EncDEK
			existing.EncPassData = passEnv.EncSecret
		}

		if err := a.store.UpdateHost(existing); err != nil {
			f.errMsg = "更新主机失败: " + err.Error()
			return
		}
		a.store.AddAuditLog(existing.ID, existing.Name, "HOST_UPDATE", "INFO", "desktop-local", "更新主机配置")
	}

	a.closeHostModal()
	_ = a.loadHosts()
	a.pushToast("主机已加密保存", name, "info")
}

func (a *App) triggerPickPrivateKey() {
	if a.hostModal.pickingKey {
		return
	}
	a.hostModal.pickingKey = true
	a.hostModal.errMsg = ""
	go func() {
		content, err := a.pickKeyFn()
		a.runUI(func() {
			a.hostModal.pickingKey = false
			if err != nil {
				a.hostModal.errMsg = "读取私钥文件失败: " + err.Error()
				return
			}
			if content != "" {
				a.hostModal.privateKey = content
				a.hostModal.secretGen++
			}
		})
	}()
}

func (a *App) renderHostModal(c *ui.Context) {
	if !a.hostOpen {
		return
	}
	f := &a.hostModal
	title := "新增 SSH 主机"
	secretHint := " *"
	if f.editingID != 0 {
		title = "编辑 SSH 主机"
		secretHint = "（留空则保留原加密凭据）"
	}

	m := ui.DialogBase(c, &a.hostOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.72))
		panel.Width(540).MaxHeight(680).Padding(20).Gap(12).Radius(10).
			Background(ui.Hex("#161b22")).Border(1, ui.Hex("#30363d"))

		ui.Row(c).Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
			ui.Text(c, title).FontSize(16).Bold()
			if ui.Button(c, "×").Padding(2, 8).Clicked() {
				a.closeHostModal()
			}
		})

		ui.Scroll(c.Key("host-form-scroll")).Shrink(1).Gap(10).Children(func() {
			ui.Row(c).Gap(10).Children(func() {
				ui.Column(c).Grow(1).Children(func() {
					ui.Field(c, "主机名称 *", func() {
						ui.TextInput(c.Key("host-name"), &f.name).Placeholder("例如：生产-Web-01").AutoFocus()
					})
				})
				ui.Column(c).Grow(1).Children(func() {
					ui.Field(c, "用户名 *", func() {
						ui.TextInput(c.Key("host-user"), &f.username).Placeholder("root / ubuntu")
					})
				})
			})

			ui.Row(c).Gap(10).Children(func() {
				ui.Column(c).Grow(2).Children(func() {
					ui.Field(c, "主机地址 (IP / 域名) *", func() {
						ui.TextInput(c.Key("host-addr"), &f.address).Placeholder("10.0.0.12")
					})
				})
				ui.Column(c).Grow(1).Children(func() {
					ui.Field(c, "SSH 端口 *", func() {
						ui.TextInput(c.Key("host-port"), &f.port).Placeholder("22")
					})
				})
			})

			ui.Field(c, "认证方式", func() {
				ui.RadioGroup(c, func() {
					ui.Radio(c, &f.authType, store.AuthPrivateKey, "公私钥认证 (推荐)")
					ui.Radio(c, &f.authType, store.AuthPassword, "密码认证")
				}).Row().Gap(16).Label("认证方式")
			})

			if f.authType == store.AuthPassword {
				ui.Field(c, "SSH 密码"+secretHint, func() {
					ui.TextInput(c.Key(fmt.Sprintf("host-pw-%d", f.secretGen)), &f.password).
						Password().Placeholder("输入目标服务器密码（AES-256-GCM 加密存储）")
				})
			} else {
				ui.Column(c).Gap(6).Children(func() {
					ui.Row(c).Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "SSH 私钥内容 (PEM / OpenSSH)"+secretHint).FontSize(12).TextColor(ui.Hex("#8b949e"))
						if ui.Button(c, "从本机导入私钥文件...").Disabled(f.pickingKey).Padding(3, 8).Clicked() {
							a.triggerPickPrivateKey()
						}
					})
					ui.TextArea(c.Key(fmt.Sprintf("host-key-%d", f.secretGen)), &f.privateKey).
						Height(110).Placeholder("-----BEGIN OPENSSH PRIVATE KEY-----\n...\n-----END OPENSSH PRIVATE KEY-----")
				})
				ui.Field(c, "私钥口令 Passphrase (可选)", func() {
					ui.TextInput(c.Key(fmt.Sprintf("host-pass-%d", f.secretGen)), &f.passphrase).
						Password().Placeholder("若私钥未加密可留空")
				})
			}

			ui.Checkbox(c.Key("host-guard"), &f.guardEnabled, "启用高危命令拦截守卫（自动阻断 rm -rf /、mkfs、dd 覆写磁盘等）")

			ui.Field(c, "备注", func() {
				ui.TextInput(c.Key("host-notes"), &f.notes).Placeholder("可选备注信息")
			})
		})

		if f.errMsg != "" {
			ui.Box(c).Padding(8, 10).Radius(6).
				Background(ui.RGBA(248, 81, 73, 0.14)).Border(1, ui.RGBA(248, 81, 73, 0.4)).
				Children(func() {
					ui.Text(c, f.errMsg).FontSize(12).TextColor(ui.Hex("#ff7b72"))
				})
		}

		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if ui.Button(c, "取消").Clicked() {
				a.closeHostModal()
			}
			if ui.PrimaryButton(c, "加密并保存").Clicked() {
				a.saveHostForm()
			}
		})
	})

	if m.Dismissed() {
		a.closeHostModal()
	}
}

func (a *App) renderDeleteHostDialog(c *ui.Context) {
	if !a.deleteAsk || a.deleteTarget == nil {
		return
	}
	target := *a.deleteTarget
	res := ui.AlertDialog(c, &a.deleteAsk,
		"删除主机 "+target.Name+"？",
		"确认删除该主机及其加密凭据？该主机已打开的终端会话将被同步关闭。",
		"取消", "确认删除")
	if res == 1 {
		a.deleteHost(target)
		a.deleteTarget = nil
	} else if res == 0 || res == ui.AlertDismissed {
		a.deleteTarget = nil
	}
}

func (a *App) deleteHost(target store.Host) {
	a.touchActivity()
	a.closeTabsForHost(target.ID)
	if err := a.store.DeleteHost(target.ID); err != nil {
		a.pushToast("删除主机失败", err.Error(), "error")
		return
	}
	a.store.AddAuditLog(target.ID, target.Name, "HOST_DELETE", "WARN", "desktop-local", "删除主机及关联加密凭据")
	_ = a.loadHosts()
	a.pushToast("主机已删除", target.Name, "info")
}
