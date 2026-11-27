package desktopui

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"testing"
	"time"

	"ssh-guard/internal/sshproxy"
	"ssh-guard/internal/store"

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

func (c *testPipeConn) Read(b []byte) (int, error)         { return c.r.Read(b) }
func (c *testPipeConn) Write(b []byte) (int, error)        { return c.w.Write(b) }
func (c *testPipeConn) Close() error                       { _ = c.w.Close(); return c.r.Close() }
func (c *testPipeConn) LocalAddr() net.Addr                { return testPipeAddr("local") }
func (c *testPipeConn) RemoteAddr() net.Addr               { return testPipeAddr("remote") }
func (c *testPipeConn) SetDeadline(time.Time) error        { return nil }
func (c *testPipeConn) SetReadDeadline(time.Time) error    { return nil }
func (c *testPipeConn) SetWriteDeadline(time.Time) error   { return nil }

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
	if !tt.HasText("零凭据暴露 SSH 原生安全终端") {
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
	app.connectHost(id1)
	app.connectHost(id2)

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
	app.connectHost(updated.ID)
	if len(app.tabs) != 1 {
		t.Fatalf("expected 1 open tab, got %d", len(app.tabs))
	}
	app.deleteHost(*updated)
	if len(app.tabs) != 0 || len(app.hosts) != 0 {
		t.Fatalf("expected tabs & host removed, tabs=%d hosts=%d", len(app.tabs), len(app.hosts))
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
	app.connectHost(hostID)
	if loadCalls != 1 || len(app.tabs) != 0 || len(app.pendingToasts) == 0 {
		t.Fatalf("expected failed lazy load toast & no tab: calls=%d tabs=%d toasts=%d", loadCalls, len(app.tabs), len(app.pendingToasts))
	}

	// 2. Retry succeeds -> loads once and caches termLoaded=true.
	shouldFail = false
	app.connectHost(hostID)
	if loadCalls != 2 || len(app.tabs) != 1 {
		t.Fatalf("expected successful retry load: calls=%d tabs=%d", loadCalls, len(app.tabs))
	}

	// 3. Subsequent connectHost and startReplay reuse cached runtime without reloading.
	app.connectHost(hostID)
	if loadCalls != 2 || len(app.tabs) != 2 {
		t.Fatalf("expected cached terminal runtime on second connect: calls=%d tabs=%d", loadCalls, len(app.tabs))
	}

	rec, err := sshproxy.NewCastRecorder(app.recordingsDir, "lazy-rec", "demo", 80, 24)
	if err != nil {
		t.Fatalf("NewCastRecorder: %v", err)
	}
	rec.RecordOutput([]byte("hi\r\n"))
	_, _ = rec.Close()
	app.startReplay(store.Recording{ID: "lazy-rec", HostName: "demo", FilePath: rec.FilePath()})
	if loadCalls != 2 || app.replay == nil {
		t.Fatalf("expected cached terminal runtime on replay: calls=%d replay=%v", loadCalls, app.replay)
	}
	app.stopReplay()

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
