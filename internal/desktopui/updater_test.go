package desktopui

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type fakeUpdateBackend struct {
	checkCalls atomic.Int32
	checkFn    func(ctx context.Context) (*updateOffer, error)
}

func (f *fakeUpdateBackend) Check(ctx context.Context) (*updateOffer, error) {
	f.checkCalls.Add(1)
	if f.checkFn != nil {
		return f.checkFn(ctx)
	}
	return nil, nil
}

func waitForCondition(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", desc)
}

func TestUpdaterCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.6.0", "0.6.0", 0},
		{"v0.7.0", "0.6.0", 1},
		{"0.6.0", "0.7.0", -1},
		{"0.10.0", "0.2.0", 1},
		{"1.0.0-beta.2", "1.0.0-beta.10", -1},
		{"1.0.0-beta.10", "1.0.0", -1},
		{"1.0.0", "1.0.0-rc.1", 1},
		{"1.0.0+build.1", "1.0.0+build.2", 0},
	}
	for _, tc := range cases {
		if got := compareSemver(tc.a, tc.b); got != tc.want {
			t.Errorf("compareSemver(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestUpdaterDefaultBackendSelfUpdate(t *testing.T) {
	var checkCalled bool
	b := &defaultUpdateBackend{
		isPortable:     func() bool { return false },
		updaterEnabled: func() bool { return true },
		updaterCheck: func(ctx context.Context) (*mygo.Update, error) {
			checkCalled = true
			return &mygo.Update{
				Version: "0.7.0",
				Notes:   "- Signed update support",
				Date:    time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC),
			}, nil
		},
	}
	offer, err := b.Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !checkCalled {
		t.Fatal("expected mygo.Updater.Check path to be invoked")
	}
	if offer == nil || offer.Version != "0.7.0" || !offer.CanInstall || offer.Install == nil {
		t.Fatalf("unexpected offer: %+v", offer)
	}
	if offer.ReleaseURL != "https://github.com/pandasir/ssh-guard/releases/tag/v0.7.0" {
		t.Fatalf("unexpected release URL: %q", offer.ReleaseURL)
	}

	// Up-to-date path
	b.updaterCheck = func(ctx context.Context) (*mygo.Update, error) { return nil, nil }
	offer, err = b.Check(context.Background())
	if err != nil || offer != nil {
		t.Fatalf("expected nil offer when up to date, got offer=%+v err=%v", offer, err)
	}
}

func TestUpdaterDefaultBackendManifestFallback(t *testing.T) {
	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			var body string
			status := http.StatusOK
			switch r.URL.Path {
			case "/releases/latest/download/update-windows-amd64.json":
				body = `{"version":"0.7.0","notes":"- Portable check works","date":"2026-11-28T00:00:00Z"}`
			case "/releases/latest/download/update-darwin-arm64.json":
				body = `{"version":"0.6.0","notes":"same","date":"2026-11-28T00:00:00Z"}`
			default:
				status = http.StatusNotFound
				body = "not found"
			}
			return &http.Response{
				StatusCode: status,
				Status:     http.StatusText(status),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(body)),
				Request:    r,
			}, nil
		}),
	}

	// 1. Windows portable build degrades to browser download even if updaterEnabled would report true.
	b := &defaultUpdateBackend{
		isPortable:      func() bool { return true },
		isDev:           func() bool { return false },
		updaterEnabled:  func() bool { return true },
		currentVersion:  func() string { return "0.6.0" },
		platformTarget:  func() string { return "windows-amd64" },
		manifestBaseURL: "https://example.invalid/releases/latest/download",
		httpClient:      client,
	}
	offer, err := b.Check(context.Background())
	if err != nil {
		t.Fatalf("portable Check: %v", err)
	}
	if offer == nil || offer.Version != "0.7.0" || offer.CanInstall {
		t.Fatalf("expected non-installable fallback offer, got %+v", offer)
	}
	if !strings.Contains(offer.FallbackReason, "便携版") {
		t.Fatalf("expected portable fallback reason, got %q", offer.FallbackReason)
	}
	if offer.Notes != "- Portable check works" {
		t.Fatalf("unexpected notes: %q", offer.Notes)
	}

	// 2. Non-writable non-portable installation gets read-only fallback reason.
	b.isPortable = func() bool { return false }
	b.updaterEnabled = func() bool { return false }
	offer, err = b.Check(context.Background())
	if err != nil {
		t.Fatalf("read-only Check: %v", err)
	}
	if offer == nil || offer.CanInstall || !strings.Contains(offer.FallbackReason, "不可写") {
		t.Fatalf("expected read-only fallback reason, got %+v", offer)
	}

	// 3. Up-to-date manifest returns nil offer.
	b.platformTarget = func() string { return "darwin-arm64" }
	offer, err = b.Check(context.Background())
	if err != nil || offer != nil {
		t.Fatalf("expected nil offer for matching version, got offer=%+v err=%v", offer, err)
	}

	// 4. Reject non-HTTPS non-loopback manifest URLs.
	b.manifestBaseURL = "http://example.com/releases"
	if _, err := b.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("expected non-HTTPS rejection error, got %v", err)
	}
}

func TestUpdaterNoBackgroundNetworkOnInit(t *testing.T) {
	backend := &fakeUpdateBackend{}
	app, err := NewApp(Options{
		DataDir:       t.TempDir(),
		SkipLoadVT:    true,
		UpdateBackend: backend,
	})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	defer app.Shutdown()

	tt := ui.NewTester(app.View, 1280, 820)
	tt.Frame()
	app.unlockPass = "MasterPass!2026"
	app.unlockConfirm = "MasterPass!2026"
	app.submitUnlock()
	tt.Frame()
	time.Sleep(30 * time.Millisecond)

	if got := backend.checkCalls.Load(); got != 0 {
		t.Fatalf("expected zero automatic update checks on launch/unlock, got %d", got)
	}
}

func TestAppUpdateCheckUpToDateAndErrorRetry(t *testing.T) {
	var attempt atomic.Int32
	backend := &fakeUpdateBackend{
		checkFn: func(ctx context.Context) (*updateOffer, error) {
			if attempt.Add(1) == 1 {
				return nil, errors.New("network unreachable")
			}
			return nil, nil
		},
	}

	app, err := NewApp(Options{
		DataDir:       t.TempDir(),
		SkipLoadVT:    true,
		UpdateBackend: backend,
	})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	defer app.Shutdown()

	tt := ui.NewTester(app.View, 1280, 820)
	app.openAboutModal()
	tt.Frame()
	if !tt.HasText("检查更新") {
		t.Fatalf("expected 检查更新 button in About modal, got %q", tt.Texts())
	}
	if err := tt.Click("检查更新"); err != nil {
		t.Fatalf("click 检查更新: %v", err)
	}

	waitForCondition(t, 2*time.Second, "error state", func() bool {
		app.uiMu.Lock()
		defer app.uiMu.Unlock()
		return app.update.phase == updateError
	})
	tt.Frame()
	if !tt.HasText("network unreachable") || !tt.HasText("重试") {
		t.Fatalf("expected error message and 重试 button, got %q", tt.Texts())
	}

	if err := tt.Click("重试"); err != nil {
		t.Fatalf("click 重试: %v", err)
	}
	waitForCondition(t, 2*time.Second, "up-to-date state", func() bool {
		app.uiMu.Lock()
		defer app.uiMu.Unlock()
		return app.update.phase == updateUpToDate
	})
	tt.Frame()
	if !tt.HasText("当前已是最新版本") {
		t.Fatalf("expected up-to-date message, got %q", tt.Texts())
	}
}

func TestAppUpdateInstallProgressCancelAndReady(t *testing.T) {
	installStarted := make(chan struct{}, 2)
	allowFinish := make(chan struct{})
	var installAttempt atomic.Int32

	backend := &fakeUpdateBackend{
		checkFn: func(ctx context.Context) (*updateOffer, error) {
			return &updateOffer{
				Version:    "0.7.0",
				Notes:      "- 新增软件内在线更新",
				Date:       time.Date(2026, 11, 28, 0, 0, 0, 0, time.UTC),
				ReleaseURL: "https://github.com/pandasir/ssh-guard/releases/tag/v0.7.0",
				CanInstall: true,
				Install: func(ctx context.Context, progress func(downloaded, total int64)) error {
					n := installAttempt.Add(1)
					progress(512*1024, 2*1024*1024)
					installStarted <- struct{}{}
					if n == 1 {
						<-ctx.Done()
						return ctx.Err()
					}
					<-allowFinish
					progress(2*1024*1024, 2*1024*1024)
					return nil
				},
			}, nil
		},
	}

	app, err := NewApp(Options{
		DataDir:       t.TempDir(),
		SkipLoadVT:    true,
		UpdateBackend: backend,
	})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	defer app.Shutdown()

	tt := ui.NewTester(app.View, 1280, 820)
	app.openAboutAndCheckUpdates()
	waitForCondition(t, 2*time.Second, "available state", func() bool {
		app.uiMu.Lock()
		defer app.uiMu.Unlock()
		return app.update.phase == updateAvailable
	})
	tt.Frame()
	if !tt.HasText("发现新版本 v0.7.0") || !tt.HasText("新增软件内在线更新") || !tt.HasText("立即更新") {
		t.Fatalf("expected available update card and button, got %q", tt.Texts())
	}

	// 1. Start install, observe progress, then cancel back to available.
	if err := tt.Click("立即更新"); err != nil {
		t.Fatalf("click 立即更新: %v", err)
	}
	<-installStarted
	waitForCondition(t, 2*time.Second, "download progress", func() bool {
		app.uiMu.Lock()
		defer app.uiMu.Unlock()
		return app.update.phase == updateDownloading && app.update.downloaded > 0
	})
	tt.Frame()
	if !tt.HasText("正在下载并校验 v0.7.0") || !tt.HasText("取消下载") {
		t.Fatalf("expected downloading UI, got %q", tt.Texts())
	}
	if err := tt.Click("取消下载"); err != nil {
		t.Fatalf("click 取消下载: %v", err)
	}
	tt.Frame()
	app.uiMu.Lock()
	phaseAfterCancel := app.update.phase
	app.uiMu.Unlock()
	if phaseAfterCancel != updateAvailable {
		t.Fatalf("expected phase updateAvailable after cancel, got %v", phaseAfterCancel)
	}

	// 2. Start install again and let it complete to ready state.
	if err := tt.Click("立即更新"); err != nil {
		t.Fatalf("click 立即更新 (second): %v", err)
	}
	<-installStarted
	close(allowFinish)
	waitForCondition(t, 2*time.Second, "ready state", func() bool {
		app.uiMu.Lock()
		defer app.uiMu.Unlock()
		return app.update.phase == updateReady
	})
	tt.Frame()
	if !tt.HasText("已下载并通过签名校验") || !tt.HasText("立即重启并更新") {
		t.Fatalf("expected ready UI, got %q", tt.Texts())
	}
}

func TestAppUpdateActiveSessionWarningAndShutdownBeforeRelaunch(t *testing.T) {
	var (
		mu             sync.Mutex
		relaunchCalled bool
		vaultLockedAt  bool
		tabsCountAt    int
	)

	backend := &fakeUpdateBackend{
		checkFn: func(ctx context.Context) (*updateOffer, error) {
			return &updateOffer{
				Version:    "0.7.0",
				CanInstall: true,
				Install: func(ctx context.Context, progress func(downloaded, total int64)) error {
					progress(100, 100)
					return nil
				},
			}, nil
		},
	}

	var app *App
	var err error
	app, err = NewApp(Options{
		DataDir:       t.TempDir(),
		SkipLoadVT:    true,
		UpdateBackend: backend,
		RelaunchFn: func() {
			mu.Lock()
			defer mu.Unlock()
			relaunchCalled = true
			vaultLockedAt = !app.vault.IsUnlocked()
			tabsCountAt = len(app.tabs)
		},
	})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	defer app.Shutdown()

	// Unlock vault and add an active tab so the active-session warning must appear.
	app.unlockPass = "MasterPass!2026"
	app.unlockConfirm = "MasterPass!2026"
	app.submitUnlock()
	app.tabs = append(app.tabs, &SessionTab{id: 1, kind: tabKindSSH, status: "connected"})
	app.activeTab = 0

	tt := ui.NewTester(app.View, 1280, 820)
	app.openAboutAndCheckUpdates()
	waitForCondition(t, 2*time.Second, "available state", func() bool {
		app.uiMu.Lock()
		defer app.uiMu.Unlock()
		return app.update.phase == updateAvailable
	})
	tt.Frame()
	if err := tt.Click("立即更新"); err != nil {
		t.Fatalf("click 立即更新: %v", err)
	}
	waitForCondition(t, 2*time.Second, "ready state", func() bool {
		app.uiMu.Lock()
		defer app.uiMu.Unlock()
		return app.update.phase == updateReady
	})
	tt.Frame()
	if !tt.HasText("1 个活跃终端会话") {
		t.Fatalf("expected active-session warning in ready modal, got %q", tt.Texts())
	}

	if err := tt.Click("立即重启并更新"); err != nil {
		t.Fatalf("click 立即重启并更新: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !relaunchCalled {
		t.Fatal("expected relaunchFn to be invoked")
	}
	if !vaultLockedAt {
		t.Fatal("expected vault KEK to be locked/zeroized before relaunchFn")
	}
	if tabsCountAt != 0 {
		t.Fatalf("expected active tabs closed before relaunchFn, got %d", tabsCountAt)
	}
}

func TestAppUpdatePortableFallbackOpensReleasePage(t *testing.T) {
	openedURL := make(chan string, 1)
	backend := &fakeUpdateBackend{
		checkFn: func(ctx context.Context) (*updateOffer, error) {
			return &updateOffer{
				Version:        "0.7.0",
				Notes:          "- 便携版说明",
				ReleaseURL:     "https://github.com/pandasir/ssh-guard/releases/tag/v0.7.0",
				CanInstall:     false,
				FallbackReason: "当前为 Windows 单文件便携版，不支持应用内原地替换，请前往 GitHub 下载最新便携版。",
			}, nil
		},
	}

	app, err := NewApp(Options{
		DataDir:       t.TempDir(),
		SkipLoadVT:    true,
		UpdateBackend: backend,
		OpenURLFn: func(rawURL string) error {
			openedURL <- rawURL
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	defer app.Shutdown()

	tt := ui.NewTester(app.View, 1280, 820)
	app.openAboutAndCheckUpdates()
	waitForCondition(t, 2*time.Second, "available portable fallback state", func() bool {
		app.uiMu.Lock()
		defer app.uiMu.Unlock()
		return app.update.phase == updateAvailable
	})
	tt.Frame()
	if !tt.HasText("Windows 单文件便携版") || !tt.HasText("前往 GitHub 下载") {
		t.Fatalf("expected portable fallback message and browser button, got %q", tt.Texts())
	}
	if tt.HasText("立即更新") {
		t.Fatalf("did not expect 立即更新 button in portable fallback mode, got %q", tt.Texts())
	}

	if err := tt.Click("前往 GitHub 下载"); err != nil {
		t.Fatalf("click 前往 GitHub 下载: %v", err)
	}
	select {
	case got := <-openedURL:
		if got != "https://github.com/pandasir/ssh-guard/releases/tag/v0.7.0" {
			t.Fatalf("opened unexpected URL: %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for OpenURLFn")
	}
}
