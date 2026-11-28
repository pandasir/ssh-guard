package desktopui

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"ssh-guard/internal/store"

	"github.com/egoist/mygo/ui"
)

func TestRedesignedUIAndVisualRender(t *testing.T) {
	if got := formatHostEndpoint(store.Host{Username: "root", Address: "192.168.1.203", Port: 22}); got != "root@192.168.1.203" {
		t.Fatalf("expected port 22 omitted, got %q", got)
	}
	if got := formatHostEndpoint(store.Host{Username: "ops", Address: "10.0.0.5", Port: 2222}); got != "ops@10.0.0.5:2222" {
		t.Fatalf("expected non-default port included, got %q", got)
	}

	app, err := NewApp(Options{DataDir: t.TempDir(), SkipLoadVT: true})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	defer app.Shutdown()

	tt := ui.NewTester(app.View, 1280, 740)
	tt.SetScale(2)
	tt.Frame()

	dumpDir := os.Getenv("SSH_GUARD_DUMP_DIR")
	savePNG := func(name string) {
		if dumpDir == "" {
			return
		}
		if err := os.MkdirAll(dumpDir, 0o755); err != nil {
			t.Fatalf("mkdir dumpDir: %v", err)
		}
		f, err := os.Create(filepath.Join(dumpDir, name))
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		defer f.Close()
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatalf("encode %s: %v", name, err)
		}
	}

	savePNG("unlock_view.png")

	app.unlockPass = "MasterPass!2026"
	app.unlockConfirm = "MasterPass!2026"
	app.submitUnlock()
	if !app.unlocked {
		t.Fatalf("unlock failed: %s", app.unlockErr)
	}

	// Add sample host matching the redesigned mockup
	app.openAddHostModal()
	app.hostModal.name = "192.168.1.203"
	app.hostModal.username = "root"
	app.hostModal.address = "192.168.1.203"
	app.hostModal.port = "22"
	app.hostModal.authType = store.AuthPassword
	app.hostModal.password = "secret123"
	app.hostModal.guardEnabled = true
	app.saveHostForm()
	if app.hostModal.errMsg != "" {
		t.Fatalf("saveHostForm failed: %s", app.hostModal.errMsg)
	}
	if err := app.store.UpsertKnownHost(&store.KnownHost{
		HostID:          app.hosts[0].ID,
		KeyType:         "ssh-ed25519",
		Fingerprint:     "SHA256:DEMO",
		PublicKeyBase64: "AAAA",
	}); err != nil {
		t.Fatalf("UpsertKnownHost: %v", err)
	}
	if err := app.loadHosts(); err != nil {
		t.Fatalf("loadHosts: %v", err)
	}

	tt.Frame()

	for _, want := range []string{
		"SSH-Guard", "NATIVE", "+ 主机",
		"192.168.1.203 [守卫]", "root@192.168.1.203", "已绑定指纹", "密码",
		"连接", "编辑", "删除",
		"录像", "审计", "数据目录", "锁定",
		"未连接 · 请从左侧选择主机",
		"SSH 与本地 Shell 原生终端",
		">_ 打开本地终端",
	} {
		if !tt.HasText(want) {
			t.Fatalf("missing expected UI text %q in %q", want, tt.Texts())
		}
	}

	savePNG("workspace_empty.png")
}
