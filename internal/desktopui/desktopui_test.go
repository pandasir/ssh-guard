package desktopui

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ssh-guard/internal/sshproxy"
	"ssh-guard/internal/store"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

// --- In-process buffered SSH test server (zero TCP sockets) ---

type testPipeAddr string

func (a testPipeAddr) Network() string { return "pipe" }
func (a testPipeAddr) String() string  { return string(a) }

type testMemPipe struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buf    bytes.Buffer
	closed bool
}

func newTestMemPipe() *testMemPipe {
	p := &testMemPipe{}
	p.cond = sync.NewCond(&p.mu)
	return p
}

func (p *testMemPipe) Read(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for p.buf.Len() == 0 && !p.closed {
		p.cond.Wait()
	}
	if p.buf.Len() == 0 && p.closed {
		return 0, io.EOF
	}
	return p.buf.Read(b)
}

func (p *testMemPipe) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return 0, io.ErrClosedPipe
	}
	n, err := p.buf.Write(b)
	p.cond.Broadcast()
	return n, err
}

func (p *testMemPipe) Close() error {
	p.mu.Lock()
	p.closed = true
	p.cond.Broadcast()
	p.mu.Unlock()
	return nil
}

type testPipeConn struct {
	r *testMemPipe
	w *testMemPipe
}

func newTestConnPair() (net.Conn, net.Conn) {
	a, b := newTestMemPipe(), newTestMemPipe()
	return &testPipeConn{r: b, w: a}, &testPipeConn{r: a, w: b}
}

func (c *testPipeConn) Read(b []byte) (int, error)       { return c.r.Read(b) }
func (c *testPipeConn) Write(b []byte) (int, error)      { return c.w.Write(b) }
func (c *testPipeConn) Close() error                     { _ = c.w.Close(); return c.r.Close() }
func (c *testPipeConn) LocalAddr() net.Addr              { return testPipeAddr("local") }
func (c *testPipeConn) RemoteAddr() net.Addr             { return testPipeAddr("remote") }
func (c *testPipeConn) SetDeadline(time.Time) error      { return nil }
func (c *testPipeConn) SetReadDeadline(time.Time) error  { return nil }
func (c *testPipeConn) SetWriteDeadline(time.Time) error { return nil }

type testSSHServer struct {
	pubKey ssh.PublicKey
	cfg    *ssh.ServerConfig
}

func newTestSSHServer(t *testing.T, password string) *testSSHServer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if string(pass) == password {
				return &ssh.Permissions{}, nil
			}
			return nil, errors.New("bad password")
		},
	}
	cfg.AddHostKey(signer)
	return &testSSHServer{pubKey: signer.PublicKey(), cfg: cfg}
}

func (s *testSSHServer) dial(_ context.Context, _ string) (net.Conn, error) {
	cConn, sConn := newTestConnPair()
	go func() {
		defer sConn.Close()
		_, chans, reqs, err := ssh.NewServerConn(sConn, s.cfg)
		if err != nil {
			return
		}
		go ssh.DiscardRequests(reqs)
		for nc := range chans {
			if nc.ChannelType() != "session" {
				_ = nc.Reject(ssh.UnknownChannelType, "unsupported")
				continue
			}
			ch, in, err := nc.Accept()
			if err != nil {
				continue
			}
			go func() {
				defer ch.Close()
				for req := range in {
					switch req.Type {
					case "pty-req", "window-change":
						if req.Type == "window-change" && len(req.Payload) >= 8 {
							_ = binary.BigEndian.Uint32(req.Payload[0:4])
						}
						_ = req.Reply(true, nil)
					case "shell":
						_ = req.Reply(true, nil)
						_, _ = ch.Write([]byte("WELCOME-NATIVE-GHOSTTY\r\n"))
						go func() {
							buf := make([]byte, 1024)
							for {
								n, rerr := ch.Read(buf)
								if n > 0 {
									_, _ = ch.Write([]byte("OUT:" + string(buf[:n])))
								}
								if rerr != nil {
									return
								}
							}
						}()
					default:
						_ = req.Reply(false, nil)
					}
				}
			}()
		}
	}()
	return cConn, nil
}

// --- Tests ---

func TestIdleGuardTimeoutRefreshAndMaxLifetime(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	advance := func(d time.Duration) {
		mu.Lock()
		now = now.Add(d)
		mu.Unlock()
	}

	g := NewIdleGuard(30*time.Minute, 8*time.Hour)
	g.tickEvery = time.Hour // manual CheckNow in test
	g.nowFn = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	g.Start(nil)
	defer g.Stop()

	advance(20 * time.Minute)
	if expired, _ := g.CheckNow(); expired {
		t.Fatal("should not expire before 30m idle")
	}
	g.Touch()
	advance(20 * time.Minute)
	if expired, _ := g.CheckNow(); expired {
		t.Fatal("Touch should have refreshed idle timer")
	}
	advance(31 * time.Minute)
	if expired, reason := g.CheckNow(); !expired || !strings.Contains(reason, "30 分钟") {
		t.Fatalf("expected idle expiry, got expired=%v reason=%q", expired, reason)
	}

	// Test 8-hour max lifetime even with frequent Touch().
	g.Start(nil)
	for i := 0; i < 16; i++ {
		advance(25 * time.Minute)
		g.Touch()
	}
	advance(2 * time.Hour)
	g.Touch()
	if expired, reason := g.CheckNow(); !expired || !strings.Contains(reason, "8 小时") {
		t.Fatalf("expected 8h max-lifetime expiry, got expired=%v reason=%q", expired, reason)
	}
}

func TestUnlockSetupFlowSecretClearingAndLock(t *testing.T) {
	app, err := NewApp(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	defer app.Shutdown()

	tt := ui.NewTester(app.View, 1280, 820)
	if !tt.HasText("初始化主密钥 (首次启动)") {
		t.Fatalf("expected setup screen, got texts: %q", tt.Texts())
	}

	// Mismatched confirmation must fail and clear secret inputs + rotate unlockGen.
	genBefore := app.unlockGen
	app.unlockPass = "StrongPass123!"
	app.unlockConfirm = "DifferentPass!"
	app.submitUnlock()
	tt.Frame()
	if app.unlocked || app.unlockErr == "" {
		t.Fatalf("expected mismatch error, unlocked=%v err=%q", app.unlocked, app.unlockErr)
	}
	if app.unlockPass != "" || app.unlockConfirm != "" || app.unlockGen <= genBefore {
		t.Fatalf("expected password fields cleared and key gen rotated")
	}

	// Valid setup unlocks vault and transitions to main workspace.
	app.unlockPass = "StrongPass123!"
	app.unlockConfirm = "StrongPass123!"
	app.submitUnlock()
	tt.Frame()
	if !app.unlocked || !app.vault.IsUnlocked() {
		t.Fatalf("expected vault unlocked after setup, err=%q", app.unlockErr)
	}
	if app.unlockPass != "" || app.unlockConfirm != "" {
		t.Fatalf("expected password fields cleared after setup")
	}
	if !tt.HasText("SSH 与本地 Shell 原生终端") {
		t.Fatalf("expected workspace empty state, got %q", tt.Texts())
	}

	// Lock wipes KEK and returns to unlock screen.
	app.LockVault("空闲超时测试")
	tt.Frame()
	if app.unlocked || app.vault.IsUnlocked() {
		t.Fatal("expected vault locked after LockVault")
	}
	if !tt.HasText("解锁凭证保险库") || !tt.HasText("空闲超时测试") {
		t.Fatalf("expected lock screen with reason, got %q", tt.Texts())
	}

	// Wrong password fails; right password unlocks.
	app.unlockPass = "WrongPass123!"
	app.submitUnlock()
	if app.unlocked || app.unlockErr != "主密码错误" {
		t.Fatalf("expected wrong password error, got unlocked=%v err=%q", app.unlocked, app.unlockErr)
	}
	app.unlockPass = "StrongPass123!"
	app.submitUnlock()
	if !app.unlocked || !app.vault.IsUnlocked() {
		t.Fatalf("expected unlock to succeed, err=%q", app.unlockErr)
	}
}

func TestLocalTerminalTabCreationLifecycleAndVaultLock(t *testing.T) {
	if err := terminal.Load(); err != nil {
		t.Skipf("terminal runtime unavailable: %v", err)
	}
	var dialCalls atomic.Int32
	app, err := NewApp(Options{
		DataDir: t.TempDir(),
		DialConn: func(ctx context.Context, addr string) (net.Conn, error) {
			dialCalls.Add(1)
			return nil, errors.New("unexpected SSH dial from local terminal")
		},
	})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	defer app.Shutdown()

	app.unlockPass = "MasterPass!2026"
	app.unlockConfirm = "MasterPass!2026"
	app.submitUnlock()
	if !app.unlocked {
		t.Fatalf("unlock failed: %s", app.unlockErr)
	}

	tt := ui.NewTester(app.View, 1280, 820)
	tt.Frame()
	if !tt.HasText(">_ 打开本地终端") {
		t.Fatalf("expected empty-workspace local terminal action, got %q", tt.Texts())
	}

	auditsBefore, err := app.store.ListAuditLogs(500)
	if err != nil {
		t.Fatalf("ListAuditLogs before: %v", err)
	}
	recordingsBefore, err := app.store.ListRecordings(200)
	if err != nil {
		t.Fatalf("ListRecordings before: %v", err)
	}

	if err := tt.Click(">_ 打开本地终端"); err != nil {
		t.Fatalf("click empty-workspace local terminal action: %v", err)
	}
	tt.Frame()
	if len(app.tabs) != 1 {
		t.Fatalf("expected one local tab, got %d", len(app.tabs))
	}
	tab := app.tabs[0]
	if tab.kind != tabKindLocal || tab.host.ID != 0 || tab.conn != nil || tab.term == nil {
		t.Fatalf("unexpected local tab shape: kind=%v host=%+v conn=%v term=%v", tab.kind, tab.host, tab.conn, tab.term != nil)
	}
	if tab.displayName() != "本地终端 #1" || tab.status != "connected" || tab.localGen != 1 {
		t.Fatalf("unexpected local tab state: name=%q status=%q gen=%d", tab.displayName(), tab.status, tab.localGen)
	}
	if !tt.HasText("+ 终端") {
		t.Fatalf("expected tab-bar + 终端 action, got %q", tt.Texts())
	}
	if dialCalls.Load() != 0 {
		t.Fatalf("local terminal triggered %d SSH dial(s)", dialCalls.Load())
	}
	if app.tofuPending != nil || len(app.tofuQueue) != 0 {
		t.Fatal("local terminal unexpectedly opened TOFU verification")
	}

	auditsAfter, err := app.store.ListAuditLogs(500)
	if err != nil {
		t.Fatalf("ListAuditLogs after: %v", err)
	}
	recordingsAfter, err := app.store.ListRecordings(200)
	if err != nil {
		t.Fatalf("ListRecordings after: %v", err)
	}
	if len(auditsAfter) != len(auditsBefore) {
		t.Fatalf("local terminal wrote audit logs: before=%d after=%d", len(auditsBefore), len(auditsAfter))
	}
	if len(recordingsAfter) != len(recordingsBefore) {
		t.Fatalf("local terminal created recordings: before=%d after=%d", len(recordingsBefore), len(recordingsAfter))
	}

	// Dynamic OSC title updates status bar and strips control characters.
	app.uiMu.Lock()
	app.localTerminalTitleChanged(tab.id, tab.localGen, sanitizeTerminalTitle("~/www/ssh-guard\n\x07"))
	app.uiMu.Unlock()
	if tab.statusText != "本地终端 #1 · ~/www/ssh-guard" {
		t.Fatalf("unexpected title statusText: %q", tab.statusText)
	}

	// Stale generation callbacks must be ignored.
	app.uiMu.Lock()
	app.localTerminalExited(tab.id, tab.localGen+1, 9)
	app.uiMu.Unlock()
	if tab.status != "connected" {
		t.Fatalf("stale exit generation mutated tab status: %q", tab.status)
	}

	// Matching exit callback marks tab idle and exposes in-place restart button.
	app.uiMu.Lock()
	app.localTerminalExited(tab.id, tab.localGen, 7)
	app.uiMu.Unlock()
	if tab.status != "idle" || !strings.Contains(tab.statusText, "本地终端 #1") || !strings.Contains(tab.statusText, "退出码 7") {
		t.Fatalf("unexpected exited tab state: status=%q text=%q", tab.status, tab.statusText)
	}
	app.uiMu.Lock()
	app.localTerminalExited(9999, 1, 0)
	app.uiMu.Unlock()

	tt.Frame()
	if !tt.HasText("重启终端") {
		t.Fatalf("expected 重启终端 button on exited local tab, got %q", tt.Texts())
	}
	oldTerm := tab.term
	if err := tt.Click("重启终端"); err != nil {
		t.Fatalf("click restart local terminal: %v", err)
	}
	tt.Frame()
	if tab.status != "connected" || tab.localGen != 2 || tab.term == nil || tab.term == oldTerm || tab.statusText != "本地终端 #1" {
		t.Fatalf("restart did not replace local shell cleanly: status=%q gen=%d statusText=%q", tab.status, tab.localGen, tab.statusText)
	}
	select {
	case <-oldTerm.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("restarted local terminal did not close previous shell")
	}

	// Open second and third local tabs via the tab bar + 终端 button; closing #1 keeps #2 and #3 stable.
	if err := tt.Click("+ 终端"); err != nil {
		t.Fatalf("click tab bar + 终端: %v", err)
	}
	tt.Frame()
	if err := tt.Click("+ 终端"); err != nil {
		t.Fatalf("click tab bar + 终端 second time: %v", err)
	}
	tt.Frame()
	if len(app.tabs) != 3 {
		t.Fatalf("expected three local tabs, got %d", len(app.tabs))
	}
	if app.tabs[0].displayName() != "本地终端 #1" || app.tabs[1].displayName() != "本地终端 #2" || app.tabs[2].displayName() != "本地终端 #3" {
		t.Fatalf("unexpected local tab labels: %q, %q, %q", app.tabs[0].displayName(), app.tabs[1].displayName(), app.tabs[2].displayName())
	}

	firstTerm := app.tabs[0].term
	app.uiMu.Lock()
	app.closeTabByID(app.tabs[0].id)
	app.uiMu.Unlock()
	if len(app.tabs) != 2 {
		t.Fatalf("closing one local tab left %d tabs", len(app.tabs))
	}
	if app.tabs[0].displayName() != "本地终端 #2" || app.tabs[1].displayName() != "本地终端 #3" {
		t.Fatalf("closing #1 renumbered remaining tabs: %q, %q", app.tabs[0].displayName(), app.tabs[1].displayName())
	}
	select {
	case <-firstTerm.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("closed local terminal did not release its shell")
	}
	app.uiMu.Lock()
	app.closeTabByID(app.tabs[1].id)
	app.closeTabByID(app.tabs[0].id)
	app.uiMu.Unlock()
	if len(app.tabs) != 0 {
		t.Fatalf("expected all local tabs closed, got %d", len(app.tabs))
	}

	// Opening again after closing all local tabs resets numbering back to #1.
	app.uiMu.Lock()
	app.openLocalTerminal()
	app.uiMu.Unlock()
	if len(app.tabs) != 1 || app.tabs[0].displayName() != "本地终端 #1" {
		t.Fatalf("expected reset to 本地终端 #1 after closing all, got count=%d name=%q", len(app.tabs), app.tabs[0].displayName())
	}
	lockedTerm := app.tabs[0].term
	app.uiMu.Lock()
	app.LockVault("本地终端锁定测试")
	app.uiMu.Unlock()
	if len(app.tabs) != 0 || app.unlocked || app.vault.IsUnlocked() {
		t.Fatalf("lock did not close local tabs: tabs=%d unlocked=%v vault=%v", len(app.tabs), app.unlocked, app.vault.IsUnlocked())
	}
	select {
	case <-lockedTerm.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("vault lock did not release the local shell")
	}

	logs, err := app.store.ListAuditLogs(500)
	if err != nil {
		t.Fatalf("ListAuditLogs after lock: %v", err)
	}
	for _, entry := range logs {
		if entry.EventType == "CONNECT" || entry.EventType == "DISCONNECT" {
			t.Fatalf("local terminal produced SSH audit entry: %+v", entry)
		}
	}
}

func TestLocalTabLabelsControlsAndHostIsolation(t *testing.T) {
	var dialCalls atomic.Int32
	app, err := NewApp(Options{
		DataDir: t.TempDir(),
		DialConn: func(ctx context.Context, addr string) (net.Conn, error) {
			dialCalls.Add(1)
			return nil, errors.New("unexpected SSH dial from local tab")
		},
	})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	defer app.Shutdown()

	app.unlockPass = "MasterPass!2026"
	app.unlockConfirm = "MasterPass!2026"
	app.submitUnlock()
	if !app.unlocked {
		t.Fatalf("unlock failed: %s", app.unlockErr)
	}

	local1 := &SessionTab{id: 1, kind: tabKindLocal, title: "本地终端", status: "connected", statusText: "本地终端"}
	local2 := &SessionTab{id: 2, kind: tabKindLocal, title: "本地终端", status: "connected", statusText: "本地终端"}
	sshTab := &SessionTab{
		id:         3,
		kind:       tabKindSSH,
		host:       store.Host{ID: 7, Name: "prod"},
		status:     "connected",
		statusText: "已连接 · prod",
	}
	app.tabs = []*SessionTab{local1, local2, sshTab}
	app.activeTab = 0
	app.nextTabSeq = 3

	if got := local1.stripLabel(1); got != "本地终端" {
		t.Fatalf("first local label = %q", got)
	}
	if got := local2.stripLabel(2); got != "本地终端 #2" {
		t.Fatalf("second local label = %q", got)
	}
	if app.openTabCountForHost(0) != 0 || app.openTabCountForHost(7) != 1 {
		t.Fatalf("unexpected host tab counts: local=%d ssh=%d", app.openTabCountForHost(0), app.openTabCountForHost(7))
	}

	app.closeTabsForHost(0)
	if len(app.tabs) != 3 {
		t.Fatalf("host-scoped close removed local tabs: %d remain", len(app.tabs))
	}
	app.reconnectTab(local1)
	if dialCalls.Load() != 0 || local1.status != "connected" {
		t.Fatalf("local reconnect touched SSH path: dials=%d status=%q", dialCalls.Load(), local1.status)
	}

	tt := ui.NewTester(app.View, 1280, 820)
	tt.Frame()
	if !tt.HasText("本地终端 #2") {
		t.Fatalf("expected local tab sequence label, got %q", tt.Texts())
	}
	if !tt.HasText("关闭终端") || tt.HasText("断开连接") || tt.HasText("重连") {
		t.Fatalf("unexpected local workspace controls: %q", tt.Texts())
	}
	if !tt.HasText(">_ 打开本地终端") {
		t.Fatalf("expected empty-workspace local action, got %q", tt.Texts())
	}

	app.activeTab = 2
	sshTab.status = "idle"
	sshTab.statusText = "会话已断开 · prod"
	tt.Frame()
	if !tt.HasText("重连") || !tt.HasText("断开连接") {
		t.Fatalf("expected SSH reconnect/disconnect controls, got %q", tt.Texts())
	}
}

func TestLocalAndSSHTabsCoexistAcrossResizeAndVaultLock(t *testing.T) {
	if err := terminal.Load(); err != nil {
		t.Skipf("terminal runtime unavailable: %v", err)
	}
	srv := newTestSSHServer(t, "pass123")
	app, err := NewApp(Options{
		DataDir:  t.TempDir(),
		DialConn: srv.dial,
	})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	defer app.Shutdown()

	app.unlockPass = "MasterPass!2026"
	app.unlockConfirm = "MasterPass!2026"
	app.submitUnlock()
	if !app.unlocked {
		t.Fatalf("unlock failed: %s", app.unlockErr)
	}

	tt := ui.NewTester(app.View, 1280, 820)
	tt.Frame()
	if err := tt.Click(">_ 打开本地终端"); err != nil {
		t.Fatalf("open first local terminal: %v", err)
	}
	tt.Frame()
	if err := tt.Click("+ 终端"); err != nil {
		t.Fatalf("open second local terminal via tab bar: %v", err)
	}
	tt.Frame()
	if len(app.tabs) != 2 {
		t.Fatalf("expected two local tabs, got %d", len(app.tabs))
	}
	if !tt.HasText("本地终端 #1") || !tt.HasText("本地终端 #2") {
		t.Fatalf("expected two local tab labels, got %q", tt.Texts())
	}

	app.openAddHostModal()
	app.hostModal.name = "smoke-ssh"
	app.hostModal.username = "ops"
	app.hostModal.address = "127.0.0.1"
	app.hostModal.port = "22"
	app.hostModal.authType = store.AuthPassword
	app.hostModal.password = "pass123"
	app.saveHostForm()
	if app.hostModal.errMsg != "" {
		t.Fatalf("save SSH host: %s", app.hostModal.errMsg)
	}
	host := app.hosts[0]
	if err := app.store.UpsertKnownHost(&store.KnownHost{
		HostID:          host.ID,
		KeyType:         srv.pubKey.Type(),
		Fingerprint:     ssh.FingerprintSHA256(srv.pubKey),
		PublicKeyBase64: base64.StdEncoding.EncodeToString(srv.pubKey.Marshal()),
	}); err != nil {
		t.Fatalf("trust test SSH host: %v", err)
	}
	app.connectHost(host.ID)

	waitCond(t, 3*time.Second, func() bool {
		app.uiMu.Lock()
		defer app.uiMu.Unlock()
		if len(app.tabs) != 3 {
			return false
		}
		sshTab := app.tabs[2]
		return sshTab.kind == tabKindSSH && sshTab.status == "connected"
	}, "SSH tab connected beside local tabs")

	for i := 0; i < 2; i++ {
		app.uiMu.Lock()
		app.activeTab = i
		localTab := app.tabs[i]
		app.uiMu.Unlock()
		tt.Frame()
		marker := fmt.Sprintf("LOCAL-%d-OK", i+1)
		localTab.term.Send([]byte(fmt.Sprintf("printf '%s\\n'\r", marker)))
		waitCond(t, 3*time.Second, func() bool {
			return strings.Contains(localTab.term.Text(), marker)
		}, "local terminal command output")
	}

	tt.SetSize(1180, 760)
	app.uiMu.Lock()
	app.activeTab = 2
	sshTab := app.tabs[2]
	app.uiMu.Unlock()
	tt.Frame()
	waitCond(t, 3*time.Second, func() bool {
		return strings.Contains(sshTab.term.Text(), "WELCOME-NATIVE-GHOSTTY")
	}, "SSH tab rendered after resize")
	sshTab.term.Send([]byte("echo SSH-TAB-OK\r"))
	waitCond(t, 3*time.Second, func() bool {
		return strings.Contains(sshTab.term.Text(), "SSH-TAB-OK")
	}, "SSH terminal command output")

	app.uiMu.Lock()
	app.activeTab = 0
	app.uiMu.Unlock()
	tt.Frame()
	if !tt.HasText("本地终端 #2") || !tt.HasText("smoke-ssh") {
		t.Fatalf("tabs lost after resize: %q", tt.Texts())
	}

	app.uiMu.Lock()
	app.LockVault("本地与 SSH 标签集成测试")
	app.uiMu.Unlock()
	if len(app.tabs) != 0 || app.unlocked || app.vault.IsUnlocked() {
		t.Fatalf("lock did not close all tabs: tabs=%d unlocked=%v vault=%v", len(app.tabs), app.unlocked, app.vault.IsUnlocked())
	}

	recordings, err := app.store.ListRecordings(200)
	if err != nil {
		t.Fatalf("ListRecordings: %v", err)
	}
	if len(recordings) != 1 || recordings[0].HostName != host.Name {
		t.Fatalf("expected only the SSH session recording, got %+v", recordings)
	}
	logs, err := app.store.ListAuditLogs(500)
	if err != nil {
		t.Fatalf("ListAuditLogs: %v", err)
	}
	connectCount, disconnectCount := 0, 0
	for _, entry := range logs {
		switch entry.EventType {
		case "CONNECT":
			connectCount++
			if entry.HostID != host.ID {
				t.Fatalf("unexpected CONNECT from local tab: %+v", entry)
			}
		case "DISCONNECT":
			disconnectCount++
			if entry.HostID != host.ID {
				t.Fatalf("unexpected DISCONNECT from local tab: %+v", entry)
			}
		}
	}
	if connectCount != 1 || disconnectCount != 1 {
		t.Fatalf("unexpected SSH audit counts: connect=%d disconnect=%d", connectCount, disconnectCount)
	}
}

func TestTabConnLifecycleAndMultiTabTOFUReconnect(t *testing.T) {
	if err := terminal.Load(); err != nil {
		t.Fatalf("terminal.Load: %v", err)
	}
	srv := newTestSSHServer(t, "pass123")
	app, err := NewApp(Options{
		DataDir:  t.TempDir(),
		DialConn: srv.dial,
	})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	defer app.Shutdown()

	app.unlockPass = "MasterPass!2026"
	app.unlockConfirm = "MasterPass!2026"
	app.submitUnlock()
	if !app.unlocked {
		t.Fatalf("unlock failed: %s", app.unlockErr)
	}

	// Verify TabConn pre-attach behavior directly.
	tc := newTabConn(nil)
	tc.WriteLocal("PRE-ATTACH-BANNER")
	_ = tc.Resize(130, 40)
	if cols, rows := tc.Size(); cols != 130 || rows != 40 {
		t.Fatalf("expected captured size 130x40, got %dx%d", cols, rows)
	}
	buf := make([]byte, 64)
	n, err := tc.Read(buf)
	if err != nil || string(buf[:n]) != "PRE-ATTACH-BANNER" {
		t.Fatalf("unexpected pre-attach read: %q err=%v", buf[:n], err)
	}
	if _, err := tc.Write([]byte("ignored")); err != nil {
		t.Fatalf("pre-attach write should discard without error: %v", err)
	}
	_ = tc.Close()
	if _, err := tc.Read(buf); !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF after Close, got %v", err)
	}

	// Create two hosts pointing at mock SSH server so concurrent connections test TOFU queue.
	createHost := func(name string) int64 {
		app.openAddHostModal()
		app.hostModal.name = name
		app.hostModal.username = "ops"
		app.hostModal.address = "127.0.0.1"
		app.hostModal.port = "22"
		app.hostModal.authType = store.AuthPassword
		app.hostModal.password = "pass123"
		app.hostModal.guardEnabled = true
		app.saveHostForm()
		if app.hostModal.errMsg != "" {
			t.Fatalf("saveHostForm %s failed: %s", name, app.hostModal.errMsg)
		}
		return app.hosts[0].ID
	}
	id1 := createHost("prod-01")
	id2 := createHost("prod-02")

	tt := ui.NewTester(app.View, 1280, 820)
	app.uiMu.Lock()
	app.connectHost(id1)
	app.connectHost(id2)
	app.uiMu.Unlock()

	// Wait for both TOFU events (one active, one queued).
	waitCond(t, 3*time.Second, func() bool {
		app.uiMu.Lock()
		defer app.uiMu.Unlock()
		return app.tofuPending != nil && len(app.tofuQueue) == 1
	}, "two queued TOFU prompts")

	tt.Frame()
	if !tt.HasText("首次连接主机公钥验证 (TOFU)") {
		t.Fatalf("expected TOFU modal text, got %q", tt.Texts())
	}

	// Confirm first TOFU -> reconnects tab 1 in-place and pops second TOFU from queue!
	app.uiMu.Lock()
	firstTabID := app.tofuPending.tabID
	app.confirmActiveTofu()
	app.uiMu.Unlock()

	waitCond(t, 3*time.Second, func() bool {
		app.uiMu.Lock()
		defer app.uiMu.Unlock()
		tab := app.findTabByID(firstTabID)
		return tab != nil && tab.status == "connected" && app.tofuPending != nil
	}, "first tab connected and second TOFU promoted")

	// Confirm second TOFU too.
	app.uiMu.Lock()
	secondTabID := app.tofuPending.tabID
	app.confirmActiveTofu()
	app.uiMu.Unlock()

	waitCond(t, 3*time.Second, func() bool {
		app.uiMu.Lock()
		defer app.uiMu.Unlock()
		tab := app.findTabByID(secondTabID)
		return tab != nil && tab.status == "connected" && app.tofuPending == nil
	}, "second tab connected")

	// Verify Ghostty terminal received remote welcome banner and high-risk command triggers toast.
	app.uiMu.Lock()
	tab1 := app.findTabByID(firstTabID)
	app.uiMu.Unlock()
	waitCond(t, 3*time.Second, func() bool {
		return strings.Contains(tab1.term.Text(), "WELCOME-NATIVE-GHOSTTY")
	}, "Ghostty terminal rendered WELCOME-NATIVE-GHOSTTY")

	tab1.term.Send([]byte("rm -rf /\r"))
	waitCond(t, 3*time.Second, func() bool {
		return strings.Contains(tab1.term.Text(), "SSH-Guard 已拦截高危命令")
	}, "Ghostty terminal rendered inline guard warning")
	tt.Frame()

	// MITM mismatch test: corrupt stored fingerprint for tab1's host and reconnect.
	if err := app.store.UpsertKnownHost(&store.KnownHost{
		HostID:          tab1.host.ID,
		KeyType:         "ssh-ed25519",
		Fingerprint:     "SHA256:TAMPERED",
		PublicKeyBase64: "AAAA",
	}); err != nil {
		t.Fatalf("corrupt known_host: %v", err)
	}
	app.uiMu.Lock()
	app.reconnectTab(tab1)
	app.uiMu.Unlock()

	waitCond(t, 3*time.Second, func() bool {
		app.uiMu.Lock()
		defer app.uiMu.Unlock()
		return app.tofuPending != nil && app.tofuPending.isMismatch
	}, "MITM mismatch modal")
	tt.Frame()
	if !tt.HasText("警告：主机公钥指纹变更 (疑似 MITM)") {
		t.Fatalf("expected MITM warning modal, got %q", tt.Texts())
	}
	app.uiMu.Lock()
	app.dismissActiveTofu()
	app.uiMu.Unlock()
}

func TestHostCRUDBlankEditPreservesSecretPickerLimitAndDeleteClosesTabs(t *testing.T) {
	if err := terminal.Load(); err != nil {
		t.Fatalf("terminal.Load: %v", err)
	}
	tmpDir := t.TempDir()
	oversized := filepath.Join(tmpDir, "too_big.pem")
	if err := os.WriteFile(oversized, bytes.Repeat([]byte("X"), MaxPrivateKeyFileBytes+1), 0o600); err != nil {
		t.Fatalf("write oversized: %v", err)
	}
	if _, err := readPrivateKeyFile(oversized); err == nil || !strings.Contains(err.Error(), "64KB") {
		t.Fatalf("expected 64KB rejection, got %v", err)
	}

	validKey := filepath.Join(tmpDir, "id_test")
	if err := os.WriteFile(validKey, []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----"), 0o600); err != nil {
		t.Fatalf("write valid key: %v", err)
	}

	openedDir := ""
	srv := newTestSSHServer(t, "pw")
	app, err := NewApp(Options{
		DataDir:  t.TempDir(),
		DialConn: srv.dial,
		PickKeyFn: func() (string, error) {
			return readPrivateKeyFile(validKey)
		},
		OpenDirFn: func(dir string) error {
			openedDir = dir
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	defer app.Shutdown()

	app.unlockPass = "MasterPass!2026"
	app.unlockConfirm = "MasterPass!2026"
	app.submitUnlock()

	// Create private-key host via picker.
	app.openAddHostModal()
	app.triggerPickPrivateKey()
	waitCond(t, 2*time.Second, func() bool {
		app.uiMu.Lock()
		defer app.uiMu.Unlock()
		return !app.hostModal.pickingKey && strings.Contains(app.hostModal.privateKey, "BEGIN OPENSSH")
	}, "private key populated from picker")

	app.hostModal.name = "key-host"
	app.hostModal.username = "ubuntu"
	app.hostModal.address = "10.0.0.2"
	app.hostModal.port = "22"
	app.saveHostForm()
	if app.hostModal.errMsg != "" || app.hostModal.privateKey != "" {
		t.Fatalf("expected saved & cleared key, err=%q keyLen=%d", app.hostModal.errMsg, len(app.hostModal.privateKey))
	}

	// Edit host leaving secret blank -> existing encrypted secret MUST be preserved!
	orig, err := app.store.GetHost(app.hosts[0].ID)
	if err != nil {
		t.Fatalf("GetHost: %v", err)
	}
	app.openEditHostModal(*orig)
	app.hostModal.name = "key-host-renamed"
	app.hostModal.notes = "prod"
	app.saveHostForm()
	updated, err := app.store.GetHost(orig.ID)
	if err != nil {
		t.Fatalf("GetHost after edit: %v", err)
	}
	if updated.Name != "key-host-renamed" || updated.EncSecretData != orig.EncSecretData || updated.EncSecretDEK != orig.EncSecretDEK {
		t.Fatalf("blank edit did not preserve encrypted secret")
	}

	// Deleting host closes any open tabs for that host.
	_ = app.store.UpsertKnownHost(&store.KnownHost{
		HostID:          updated.ID,
		KeyType:         srv.pubKey.Type(),
		Fingerprint:     ssh.FingerprintSHA256(srv.pubKey),
		PublicKeyBase64: base64.StdEncoding.EncodeToString(srv.pubKey.Marshal()),
	})
	app.uiMu.Lock()
	app.connectHost(updated.ID)
	openTabs := len(app.tabs)
	app.uiMu.Unlock()
	if openTabs != 1 {
		t.Fatalf("expected 1 open tab, got %d", openTabs)
	}
	app.uiMu.Lock()
	app.deleteHost(*updated)
	remTabs, remHosts := len(app.tabs), len(app.hosts)
	app.uiMu.Unlock()
	if remTabs != 0 || remHosts != 0 {
		t.Fatalf("expected tabs & host removed, tabs=%d hosts=%d", remTabs, remHosts)
	}

	// Data dir action hook check.
	if err := app.openDirFn(app.dataDir); err != nil || openedDir != app.dataDir {
		t.Fatalf("unexpected openDirFn result: err=%v dir=%q", err, openedDir)
	}
}

func TestAuditTableRecordingsTableAndStreamingReplay(t *testing.T) {
	if err := terminal.Load(); err != nil {
		t.Fatalf("terminal.Load: %v", err)
	}
	if d := computeReplayDelay(5.0, 0.0, 1.0); d != replayMaxEventGap {
		t.Fatalf("expected capped delay %v, got %v", replayMaxEventGap, d)
	}
	if d := computeReplayDelay(1.0, 0.5, 2.0); d != 250*time.Millisecond {
		t.Fatalf("expected 250ms delay at 2x, got %v", d)
	}

	app, err := NewApp(Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	defer app.Shutdown()

	app.unlockPass = "MasterPass!2026"
	app.unlockConfirm = "MasterPass!2026"
	app.submitUnlock()

	rec, err := sshproxy.NewCastRecorder(app.recordingsDir, "rec-test-1", "demo", 96, 28)
	if err != nil {
		t.Fatalf("NewCastRecorder: %v", err)
	}
	rec.RecordOutput([]byte("REPLAY-FRAME-ONE\r\n"))
	rec.RecordOutput([]byte("REPLAY-FRAME-TWO\r\n"))
	size, err := rec.Close()
	if err != nil {
		t.Fatalf("rec.Close: %v", err)
	}
	recEntry := &store.Recording{
		ID:        "rec-test-1",
		HostID:    1,
		HostName:  "demo-host",
		ClientIP:  "desktop-local",
		FilePath:  rec.FilePath(),
		StartedAt: time.Now(),
		SizeBytes: size,
	}
	if err := app.store.CreateRecording(recEntry); err != nil {
		t.Fatalf("CreateRecording: %v", err)
	}
	_ = app.store.FinishRecording(recEntry.ID, time.Now(), size)

	tt := ui.NewTester(app.View, 1280, 820)
	app.openAuditModal()
	tt.Frame()
	if !tt.HasText("安全审计日志") {
		t.Fatalf("expected audit modal, got %q", tt.Texts())
	}
	app.auditOpen = false

	app.openRecordingsModal()
	tt.Frame()
	if !tt.HasText("demo-host") {
		t.Fatalf("expected recordings modal with demo-host, got %q", tt.Texts())
	}

	app.startReplay(*recEntry)
	tt.Frame()
	if app.replay == nil || app.replay.width != 96 || app.replay.height != 28 {
		t.Fatalf("unexpected replay state: %+v", app.replay)
	}
	cols, rows := app.replay.term.Size()
	if cols != 96 || rows != 28 {
		t.Fatalf("expected replay terminal sized 96x28, got %dx%d", cols, rows)
	}

	waitCond(t, 3*time.Second, func() bool {
		return strings.Contains(app.replay.term.Text(), "REPLAY-FRAME-TWO")
	}, "replay rendered frames")

	app.stopReplay()
	if app.replay != nil || app.replayOpen {
		t.Fatal("expected replay stopped and cleared")
	}
}

func TestEnsurePortableNativeLibExtractionCacheHitCorruptionAndEnvOverride(t *testing.T) {
	// Use > 32 KB payload so streaming io.CopyBuffer in fileMatchesSHA256 exercises multiple chunks.
	payload := bytes.Repeat([]byte("FAKE-GHOSTTY-VT-DLL-BINARY-PAYLOAD-FOR-TEST-"), 2048)
	baseDir := t.TempDir()

	// Clear env var for clean test.
	t.Setenv(EnvGhosttyVTPath, "")

	// 1. First launch extracts atomically and sets MYGO_GHOSTTY_VT.
	path1, extracted1, err := EnsurePortableNativeLib(payload, baseDir)
	if err != nil || !extracted1 || path1 == "" {
		t.Fatalf("first extract failed: path=%q extracted=%v err=%v", path1, extracted1, err)
	}
	if gotEnv := os.Getenv(EnvGhosttyVTPath); gotEnv != path1 {
		t.Fatalf("expected env %q, got %q", path1, gotEnv)
	}

	// 2. Second launch (with env cleared to simulate fresh process) hits cache without rewriting.
	t.Setenv(EnvGhosttyVTPath, "")
	path2, extracted2, err := EnsurePortableNativeLib(payload, baseDir)
	if err != nil || extracted2 || path2 != path1 {
		t.Fatalf("expected cache hit on %q, got path=%q extracted=%v err=%v", path1, path2, extracted2, err)
	}

	// 3. Corrupted cached file triggers SHA-256 mismatch and atomic rewrite.
	if err := os.WriteFile(path1, []byte("CORRUPTED"), 0o600); err != nil {
		t.Fatalf("corrupt cache file: %v", err)
	}
	t.Setenv(EnvGhosttyVTPath, "")
	path3, extracted3, err := EnsurePortableNativeLib(payload, baseDir)
	if err != nil || !extracted3 || path3 != path1 {
		t.Fatalf("expected rewrite on corruption, got path=%q extracted=%v err=%v", path3, extracted3, err)
	}
	restored, _ := os.ReadFile(path3)
	if !bytes.Equal(restored, payload) {
		t.Fatalf("restored payload mismatch")
	}

	// 4. Explicit MYGO_GHOSTTY_VT override takes precedence.
	t.Setenv(EnvGhosttyVTPath, "/custom/override/ghostty-vt.dll")
	path4, extracted4, err := EnsurePortableNativeLib(payload, baseDir)
	if err != nil || extracted4 || path4 != "/custom/override/ghostty-vt.dll" {
		t.Fatalf("expected env override precedence, got path=%q extracted=%v err=%v", path4, extracted4, err)
	}
}

func TestLazyTerminalRuntimeLoadingAndRetry(t *testing.T) {
	srv := newTestSSHServer(t, "pass123")
	app, err := NewApp(Options{
		DataDir:  t.TempDir(),
		DialConn: srv.dial,
	})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	defer app.Shutdown()

	loadCalls := 0
	shouldFail := true
	app.loadTerminalFn = func() error {
		loadCalls++
		if shouldFail {
			return errors.New("simulated transient dll load failure")
		}
		return terminal.Load()
	}

	// Startup, unlock screen rendering, and vault unlock must NOT load libghostty-vt.
	tt := ui.NewTester(app.View, 1280, 820)
	app.unlockPass = "MasterPass!2026"
	app.unlockConfirm = "MasterPass!2026"
	app.submitUnlock()
	tt.Frame()
	if loadCalls != 0 {
		t.Fatalf("expected 0 terminal load calls before first terminal use, got %d", loadCalls)
	}

	app.openAddHostModal()
	app.hostModal.name = "lazy-host"
	app.hostModal.username = "ops"
	app.hostModal.address = "127.0.0.1"
	app.hostModal.port = "22"
	app.hostModal.authType = store.AuthPassword
	app.hostModal.password = "pass123"
	app.saveHostForm()
	hostID := app.hosts[0].ID

	// 1. First connect hits simulated transient failure -> error toast, no tab, retryable.
	app.uiMu.Lock()
	app.connectHost(hostID)
	tabsAfterFail, toastsAfterFail := len(app.tabs), len(app.pendingToasts)
	app.uiMu.Unlock()
	if loadCalls != 1 || tabsAfterFail != 0 || toastsAfterFail == 0 {
		t.Fatalf("expected failed lazy load toast & no tab: calls=%d tabs=%d toasts=%d", loadCalls, tabsAfterFail, toastsAfterFail)
	}

	// 2. Retry succeeds -> loads once and caches termLoaded=true.
	shouldFail = false
	app.uiMu.Lock()
	app.connectHost(hostID)
	tabsAfterRetry := len(app.tabs)
	app.uiMu.Unlock()
	if loadCalls != 2 || tabsAfterRetry != 1 {
		t.Fatalf("expected successful retry load: calls=%d tabs=%d", loadCalls, tabsAfterRetry)
	}

	// 3. Subsequent connectHost and startReplay reuse cached runtime without reloading.
	app.uiMu.Lock()
	app.connectHost(hostID)
	tabsAfterSecond := len(app.tabs)
	app.uiMu.Unlock()
	if loadCalls != 2 || tabsAfterSecond != 2 {
		t.Fatalf("expected cached terminal runtime on second connect: calls=%d tabs=%d", loadCalls, tabsAfterSecond)
	}

	rec, err := sshproxy.NewCastRecorder(app.recordingsDir, "lazy-rec", "demo", 80, 24)
	if err != nil {
		t.Fatalf("NewCastRecorder: %v", err)
	}
	rec.RecordOutput([]byte("hi\r\n"))
	_, _ = rec.Close()
	app.uiMu.Lock()
	app.startReplay(store.Recording{ID: "lazy-rec", HostName: "demo", FilePath: rec.FilePath()})
	hasReplay := app.replay != nil
	app.uiMu.Unlock()
	if loadCalls != 2 || !hasReplay {
		t.Fatalf("expected cached terminal runtime on replay: calls=%d replay=%v", loadCalls, hasReplay)
	}
	app.uiMu.Lock()
	app.stopReplay()
	app.uiMu.Unlock()

	// 4. Options.SkipLoadVT short-circuits ensureTerminalRuntime completely.
	skipApp, err := NewApp(Options{DataDir: t.TempDir(), SkipLoadVT: true})
	if err != nil {
		t.Fatalf("NewApp(SkipLoadVT): %v", err)
	}
	defer skipApp.Shutdown()
	skipCalls := 0
	skipApp.loadTerminalFn = func() error {
		skipCalls++
		return errors.New("must not be called")
	}
	if err := skipApp.ensureTerminalRuntime(); err != nil || skipCalls != 0 {
		t.Fatalf("expected SkipLoadVT to bypass loadTerminalFn: err=%v calls=%d", err, skipCalls)
	}
}

func TestDesktopUIMemoryOptimizations(t *testing.T) {
	if DefaultScrollbackBytes != 256*1024 {
		t.Fatalf("expected DefaultScrollbackBytes=256 KiB, got %d", DefaultScrollbackBytes)
	}

	// 1. TabConn.outBuf must shrink after a burst exceeding tabConnShrinkThreshold is drained.
	tc := newTabConn(nil)
	defer tc.Close()
	burst := strings.Repeat("ABCDEFGH", 6*1024) // 48 KiB (> 16 KiB shrink threshold)
	tc.WriteLocal(burst)

	tc.outMu.Lock()
	peakCap := tc.outBuf.Cap()
	tc.outMu.Unlock()
	if peakCap <= tabConnShrinkThreshold {
		t.Fatalf("expected TabConn.outBuf peak cap > %d, got %d", tabConnShrinkThreshold, peakCap)
	}

	buf := make([]byte, 4096)
	readTotal := 0
	for readTotal < len(burst) {
		n, err := tc.Read(buf)
		if err != nil {
			t.Fatalf("TabConn.Read failed at %d bytes: %v", readTotal, err)
		}
		readTotal += n
	}
	tc.outMu.Lock()
	afterLen, afterCap := tc.outBuf.Len(), tc.outBuf.Cap()
	tc.outMu.Unlock()
	if afterLen != 0 || afterCap > tabConnShrinkThreshold {
		t.Fatalf("expected drained TabConn.outBuf to shrink (len=0, cap<=%d), got len=%d cap=%d", tabConnShrinkThreshold, afterLen, afterCap)
	}

	// 2. App.theme is cached once in NewApp() and reused across frames.
	app, err := NewApp(Options{DataDir: t.TempDir(), SkipLoadVT: true})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	defer app.Shutdown()

	firstTheme := app.theme
	if firstTheme == nil {
		t.Fatal("expected App.theme to be pre-built in NewApp")
	}
	app.unlockPass = "MasterPass!2026"
	app.unlockConfirm = "MasterPass!2026"
	app.submitUnlock()

	t.Setenv("MYGO_GPU", "0")
	tt := ui.NewTester(app.View, 1280, 820)
	if app.theme != firstTheme {
		t.Fatal("expected App.theme pointer to remain cached after View frame")
	}
	if !tt.HasText("Ghostty CPU") {
		t.Fatalf("expected workspace header to show Ghostty CPU when MYGO_GPU=0, got %q", tt.Texts())
	}

	t.Setenv("MYGO_GPU", "1")
	tt.Frame()
	if app.theme != firstTheme {
		t.Fatal("expected App.theme pointer to remain cached across frames")
	}
	if !tt.HasText("Ghostty GPU") {
		t.Fatalf("expected workspace header to show Ghostty GPU when MYGO_GPU=1, got %q", tt.Texts())
	}

	// 3. LockVault invokes store.ShrinkMemory() and leaves store usable.
	app.LockVault("memory shrink check")
	if _, err := app.store.GetVaultMeta(); err != nil {
		t.Fatalf("store unusable after LockVault ShrinkMemory: %v", err)
	}
}

func TestTenGhosttyTerminalsBoundedScrollbackMemory(t *testing.T) {
	if err := terminal.Load(); err != nil {
		t.Fatalf("terminal.Load: %v", err)
	}
	runtime.GC()
	debug.FreeOSMemory()
	memLabel, beforePhys, beforeErr := currentProcessPhysicalMemory()

	terms := make([]*terminal.Terminal, 0, 10)
	line := strings.Repeat("0123456789abcdef", 6) + "\r\n"
	for i := 0; i < 10; i++ {
		term, err := terminal.New(terminal.Options{
			Conn:          newBlockedReplayConn(),
			Scrollback:    DefaultScrollbackBytes,
			ImageLimit:    -1,
			NoBlink:       true,
			NoExitMessage: true,
		})
		if err != nil {
			t.Fatalf("terminal.New #%d: %v", i, err)
		}
		terms = append(terms, term)
		for j := 0; j < 200; j++ {
			term.Feed([]byte(line))
		}
	}
	var ms runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms)
	_, activePhys, activeErr := currentProcessPhysicalMemory()

	for _, term := range terms {
		_ = term.Close()
	}
	runtime.GC()
	debug.FreeOSMemory()
	_, afterClosePhys, afterErr := currentProcessPhysicalMemory()

	if beforeErr == nil && activeErr == nil && afterErr == nil {
		deltaMiB := float64(int64(activePhys)-int64(beforePhys)) / (1024 * 1024)
		t.Logf("10 active Ghostty terminals (Scrollback=%d KiB each, NoBlink=true): Go HeapAlloc=%.2f MiB, OS %s before=%.2f MiB active=%.2f MiB (delta=%+.2f MiB) afterClose=%.2f MiB",
			DefaultScrollbackBytes/1024,
			float64(ms.HeapAlloc)/(1024*1024),
			memLabel,
			float64(beforePhys)/(1024*1024),
			float64(activePhys)/(1024*1024),
			deltaMiB,
			float64(afterClosePhys)/(1024*1024),
		)
		if activePhys == 0 {
			t.Fatalf("expected positive OS %s measurement", memLabel)
		}
	} else {
		if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
			t.Fatalf("OS physical memory helper failed on %s: before=%v active=%v after=%v", runtime.GOOS, beforeErr, activeErr, afterErr)
		}
		t.Logf("10 active Ghostty terminals (Scrollback=%d KiB each, NoBlink=true): Go HeapAlloc=%.2f MiB (OS metric unavailable: %v)",
			DefaultScrollbackBytes/1024, float64(ms.HeapAlloc)/(1024*1024), activeErr)
	}
}

func waitCond(t *testing.T, timeout time.Duration, fn func() bool, desc string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", desc)
}

func TestTerminalMenuActions(t *testing.T) {
	app, err := NewApp(Options{DataDir: t.TempDir(), SkipLoadVT: true})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	defer app.Shutdown()

	if got := app.terminalFontSize(); got != DefaultTerminalFontSize {
		t.Fatalf("expected default font size %v, got %v", DefaultTerminalFontSize, got)
	}

	// Locked state: zoom, reset, clear, reconnect, close must be safe no-ops.
	app.zoomTerminalFont(2)
	app.resetTerminalFont()
	app.clearActiveTerminalScreen()
	app.clearActiveTerminalScrollback()
	app.reconnectOrRestartActiveTab()
	app.closeActiveTab()
	if got := app.terminalFontSize(); got != DefaultTerminalFontSize {
		t.Fatalf("expected locked zoom to be a no-op, got %v", got)
	}

	// Unlock vault.
	app.unlockPass = "MasterPass!2026"
	app.unlockConfirm = "MasterPass!2026"
	app.submitUnlock()
	if !app.unlocked {
		t.Fatal("expected vault to unlock")
	}

	// Unlocked without tabs: clear/reconnect/close must remain safe no-ops.
	app.clearActiveTerminalScreen()
	app.clearActiveTerminalScrollback()
	app.reconnectOrRestartActiveTab()
	app.closeActiveTab()

	// Font zoom bounds and reset.
	app.zoomTerminalFont(1)
	if got := app.terminalFontSize(); got != DefaultTerminalFontSize+1 {
		t.Fatalf("expected font size %v after +1, got %v", DefaultTerminalFontSize+1, got)
	}
	app.zoomTerminalFont(100)
	if got := app.terminalFontSize(); got != MaxTerminalFontSize {
		t.Fatalf("expected font size clamped at %v, got %v", MaxTerminalFontSize, got)
	}
	app.zoomTerminalFont(-100)
	if got := app.terminalFontSize(); got != MinTerminalFontSize {
		t.Fatalf("expected font size clamped at %v, got %v", MinTerminalFontSize, got)
	}
	app.resetTerminalFont()
	if got := app.terminalFontSize(); got != DefaultTerminalFontSize {
		t.Fatalf("expected font size reset to %v, got %v", DefaultTerminalFontSize, got)
	}

	// With a live terminal instance, clear screen, clear scrollback, zoom, and close work cleanly.
	if err := terminal.Load(); err == nil {
		term, err := terminal.New(terminal.Options{
			Conn:          newBlockedReplayConn(),
			Scrollback:    DefaultScrollbackBytes,
			ImageLimit:    -1,
			NoBlink:       true,
			NoExitMessage: true,
			Font:          terminal.Font{Size: app.terminalFontSize()},
			Theme:         terminal.DarkTheme(),
		})
		if err != nil {
			t.Fatalf("terminal.New: %v", err)
		}
		term.Feed([]byte("hello ssh-guard\r\nsecond line\r\n"))
		app.tabs = append(app.tabs, &SessionTab{
			id:     1,
			kind:   tabKindLocal,
			title:  "本地终端 #1",
			term:   term,
			status: "connected",
		})
		app.activeTab = 0

		app.zoomTerminalFont(2)
		if got := app.terminalFontSize(); got != DefaultTerminalFontSize+2 {
			t.Fatalf("expected font size %v with active tab, got %v", DefaultTerminalFontSize+2, got)
		}
		app.clearActiveTerminalScreen()
		app.clearActiveTerminalScrollback()
		app.closeActiveTab()
		if len(app.tabs) != 0 {
			t.Fatalf("expected closeActiveTab to remove active tab, got %d remaining", len(app.tabs))
		}
	}
}

func TestSidebarToggle(t *testing.T) {
	app, err := NewApp(Options{DataDir: t.TempDir(), SkipLoadVT: true})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	defer app.Shutdown()

	// Locked toggle must be ignored.
	app.toggleSidebar()
	if app.sidebarHidden {
		t.Fatal("expected toggleSidebar to be a no-op while vault is locked")
	}

	app.unlockPass = "MasterPass!2026"
	app.unlockConfirm = "MasterPass!2026"
	app.submitUnlock()

	tt := ui.NewTester(app.View, 1280, 820)
	if !tt.HasText("+ 主机") {
		t.Fatalf("expected sidebar to be visible by default, got %q", tt.Texts())
	}

	app.toggleSidebar()
	tt.Frame()
	if !app.sidebarHidden {
		t.Fatal("expected sidebarHidden=true after first toggle")
	}
	if tt.HasText("+ 主机") {
		t.Fatalf("expected sidebar controls hidden when collapsed, got %q", tt.Texts())
	}

	app.toggleSidebar()
	tt.Frame()
	if app.sidebarHidden {
		t.Fatal("expected sidebarHidden=false after second toggle")
	}
	if !tt.HasText("+ 主机") {
		t.Fatalf("expected sidebar controls restored after second toggle, got %q", tt.Texts())
	}
}

func TestAboutModal(t *testing.T) {
	dataDir := t.TempDir()
	app, err := NewApp(Options{DataDir: dataDir, SkipLoadVT: true})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	defer app.Shutdown()

	// 1. Works while locked.
	tt := ui.NewTester(app.View, 1280, 820)
	if tt.HasText("关于 SSH-Guard") {
		t.Fatal("about modal should start closed")
	}
	app.openAboutModal()
	tt.Frame()
	if !tt.HasText("关于 SSH-Guard") || !tt.HasText("v"+appDisplayVersion()) || !tt.HasText(dataDir) || !tt.HasText(app.platform) {
		t.Fatalf("expected about modal metadata while locked, got %q", tt.Texts())
	}
	if err := tt.Click("关闭"); err != nil {
		t.Fatalf("click 关闭 (locked): %v", err)
	}
	tt.Frame()
	if app.aboutOpen || tt.HasText("关于 SSH-Guard") {
		t.Fatalf("expected about modal dismissed after clicking 关闭, open=%v", app.aboutOpen)
	}

	// 2. Works while unlocked.
	app.unlockPass = "MasterPass!2026"
	app.unlockConfirm = "MasterPass!2026"
	app.submitUnlock()
	app.openAboutModal()
	tt.Frame()
	if !tt.HasText("关于 SSH-Guard") || !tt.HasText(renderBackendLabel()) {
		t.Fatalf("expected about modal metadata while unlocked, got %q", tt.Texts())
	}
	if err := tt.Click("关闭"); err != nil {
		t.Fatalf("click 关闭 (unlocked): %v", err)
	}
	tt.Frame()
	if app.aboutOpen {
		t.Fatal("expected about modal dismissed while unlocked")
	}
}

func collectMenuAccelerators(items []*mygo.MenuItem, out map[string]string) {
	for _, it := range items {
		if it.Accelerator != "" {
			key := it.ID
			if key == "" {
				key = it.Label
			}
			out[key] = it.Accelerator
		}
		if len(it.Submenu) > 0 {
			collectMenuAccelerators(it.Submenu, out)
		}
	}
}

func TestBuildAppMenu(t *testing.T) {
	app, err := NewApp(Options{DataDir: t.TempDir(), SkipLoadVT: true})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	defer app.Shutdown()

	// 1. macOS menu layout
	macMenu := app.buildAppMenu("darwin")
	macTop := macMenu.Items()
	wantMacTop := []string{"SSH-Guard", "会话", "编辑", "终端", "视图", "安全", "窗口", "帮助"}
	if len(macTop) != len(wantMacTop) {
		t.Fatalf("expected %d macOS top menus, got %d", len(wantMacTop), len(macTop))
	}
	for i, want := range wantMacTop {
		if macTop[i].Label != want {
			t.Fatalf("macOS top menu[%d]: expected %q, got %q", i, want, macTop[i].Label)
		}
	}
	if macMenu.ItemByID(menuIDAppAbout) == nil || macMenu.ItemByID(menuIDHelpAbout) == nil {
		t.Fatal("expected About items on macOS menu")
	}
	if macMenu.ItemByID(menuIDSessionQuit) != nil {
		t.Fatal("expected Quit under macOS app menu, not Session menu")
	}
	if got := macMenu.ItemByID(menuIDNewLocal).Accelerator; got != "Cmd+T" {
		t.Fatalf("expected macOS new local accelerator Cmd+T, got %q", got)
	}

	// 2. Windows menu layout & conflict-free accelerators
	winMenu := app.buildAppMenu("windows")
	winTop := winMenu.Items()
	wantWinTop := []string{"会话", "编辑", "终端", "视图", "安全", "帮助"}
	if len(winTop) != len(wantWinTop) {
		t.Fatalf("expected %d Windows top menus, got %d", len(wantWinTop), len(winTop))
	}
	for i, want := range wantWinTop {
		if winTop[i].Label != want {
			t.Fatalf("Windows top menu[%d]: expected %q, got %q", i, want, winTop[i].Label)
		}
	}
	if winMenu.ItemByID(menuIDSessionQuit) == nil || winMenu.ItemByID(menuIDSessionQuit).Role != mygo.RoleQuit {
		t.Fatal("expected RoleQuit under Session menu on Windows")
	}
	if winMenu.ItemByID(menuIDAppAbout) != nil {
		t.Fatal("did not expect macOS app menu About item on Windows")
	}

	winAccels := map[string]string{}
	collectMenuAccelerators(winTop, winAccels)
	forbidden := map[string]bool{
		"ctrl+c": true,
		"ctrl+w": true,
		"ctrl+r": true,
		"ctrl+k": true,
		"ctrl+b": true,
		"ctrl+t": true,
		"ctrl+n": true,
		"ctrl+l": true,
	}
	for id, accel := range winAccels {
		if forbidden[strings.ToLower(strings.TrimSpace(accel))] {
			t.Fatalf("Windows custom menu item %q binds forbidden bare terminal control key %q", id, accel)
		}
	}
	if got := winMenu.ItemByID(menuIDNewLocal).Accelerator; got != "Ctrl+Shift+T" {
		t.Fatalf("expected Windows new local accelerator Ctrl+Shift+T, got %q", got)
	}
	if got := winMenu.ItemByID(menuIDZoomIn).Accelerator; got != "Ctrl+=" {
		t.Fatalf("expected Windows zoom in accelerator Ctrl+=, got %q", got)
	}
}

func TestAppMenuStateSync(t *testing.T) {
	app, err := NewApp(Options{DataDir: t.TempDir(), SkipLoadVT: true})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	defer app.Shutdown()

	app.appMenu = app.buildAppMenu("windows")
	if !app.syncAppMenu() {
		t.Fatal("expected initial syncAppMenu to apply state")
	}
	if app.syncAppMenu() {
		t.Fatal("expected repeated syncAppMenu with unchanged state to be a no-op")
	}

	assertEnabled := func(stage string, id string, want bool) {
		t.Helper()
		it := app.appMenu.ItemByID(id)
		if it == nil {
			t.Fatalf("%s: missing menu item %q", stage, id)
		}
		if got := it.IsEnabled(); got != want {
			t.Fatalf("%s: item %q enabled=%v, want %v", stage, id, got, want)
		}
	}

	// 1. Locked state: privileged items disabled, About stays enabled.
	for _, id := range []string{
		menuIDNewLocal, menuIDNewHost, menuIDReconnect, menuIDCloseTab, menuIDCloseAllTabs,
		menuIDOpenDataDir, menuIDClearScreen, menuIDClearScrollback,
		menuIDZoomIn, menuIDZoomOut, menuIDZoomReset, menuIDToggleSidebar,
		menuIDLockVault, menuIDRecordings, menuIDAuditLogs,
	} {
		assertEnabled("locked", id, false)
	}
	assertEnabled("locked", menuIDHelpAbout, true)

	// 2. Unlocked with no tabs: session/host/security/zoom-in/out/sidebar enabled; tab-scoped & reset disabled.
	app.unlockPass = "MasterPass!2026"
	app.unlockConfirm = "MasterPass!2026"
	app.submitUnlock()
	if !app.syncAppMenu() {
		t.Fatal("expected syncAppMenu to update after unlock")
	}
	for _, id := range []string{
		menuIDNewLocal, menuIDNewHost, menuIDOpenDataDir,
		menuIDZoomIn, menuIDZoomOut, menuIDToggleSidebar,
		menuIDLockVault, menuIDRecordings, menuIDAuditLogs,
	} {
		assertEnabled("unlocked-empty", id, true)
	}
	for _, id := range []string{
		menuIDReconnect, menuIDCloseTab, menuIDCloseAllTabs,
		menuIDClearScreen, menuIDClearScrollback, menuIDZoomReset,
	} {
		assertEnabled("unlocked-empty", id, false)
	}
	if !app.appMenu.ItemByID(menuIDToggleSidebar).IsChecked() {
		t.Fatal("expected sidebar menu item checked by default")
	}

	// 3. Connected tab: tab close & clear enabled, reconnect still disabled.
	app.tabs = append(app.tabs, &SessionTab{id: 1, kind: tabKindSSH, status: "connected"})
	app.activeTab = 0
	if !app.syncAppMenu() {
		t.Fatal("expected syncAppMenu to update after adding connected tab")
	}
	assertEnabled("connected-tab", menuIDCloseTab, true)
	assertEnabled("connected-tab", menuIDCloseAllTabs, true)
	assertEnabled("connected-tab", menuIDClearScreen, true)
	assertEnabled("connected-tab", menuIDClearScrollback, true)
	assertEnabled("connected-tab", menuIDReconnect, false)

	// 4. Disconnected / error tab: reconnect enabled.
	app.tabs[0].status = "error"
	if !app.syncAppMenu() {
		t.Fatal("expected syncAppMenu to update after tab enters error state")
	}
	assertEnabled("error-tab", menuIDReconnect, true)

	// 5. Font zoom bounds & sidebar toggle.
	app.zoomTerminalFont(100)
	if !app.syncAppMenu() {
		t.Fatal("expected syncAppMenu to update after zooming to max font size")
	}
	assertEnabled("max-font", menuIDZoomIn, false)
	assertEnabled("max-font", menuIDZoomOut, true)
	assertEnabled("max-font", menuIDZoomReset, true)

	app.zoomTerminalFont(-100)
	if !app.syncAppMenu() {
		t.Fatal("expected syncAppMenu to update after zooming to min font size")
	}
	assertEnabled("min-font", menuIDZoomIn, true)
	assertEnabled("min-font", menuIDZoomOut, false)
	assertEnabled("min-font", menuIDZoomReset, true)

	app.toggleSidebar()
	if !app.syncAppMenu() {
		t.Fatal("expected syncAppMenu to update after hiding sidebar")
	}
	if app.appMenu.ItemByID(menuIDToggleSidebar).IsChecked() {
		t.Fatal("expected sidebar menu item unchecked when sidebar is hidden")
	}

	// 6. Lock vault disables privileged actions again.
	app.LockVault("test")
	if !app.syncAppMenu() {
		t.Fatal("expected syncAppMenu to update after LockVault")
	}
	assertEnabled("relocked", menuIDNewLocal, false)
	assertEnabled("relocked", menuIDCloseTab, false)
	assertEnabled("relocked", menuIDLockVault, false)
	assertEnabled("relocked", menuIDHelpAbout, true)
}
