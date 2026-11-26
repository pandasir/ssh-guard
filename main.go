package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"ssh-guard/internal/api"
	"ssh-guard/internal/auth"
	"ssh-guard/internal/crypto"
	"ssh-guard/internal/sshproxy"
	"ssh-guard/internal/store"
	"ssh-guard/web"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// RuntimeConfig is exposed to the WebView strictly over Wails IPC.
type RuntimeConfig struct {
	Mode     string `json:"mode"`
	Platform string `json:"platform"`
	DataDir  string `json:"data_dir"`
}

// APIResponse carries in-process HTTP responses over Wails IPC (no CORS / ATS / loopback port needed).
type APIResponse struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
}

const (
	ipcFlushInterval = 16 * time.Millisecond
	ipcFlushMaxBytes = 32 * 1024
)

// ipcTransport bridges sshproxy.ServeTransport directly to Wails native events.
// Terminal output chunks are coalesced over a short frame window to prevent
// per-read ExecuteScript/evaluateJavaScript storms and WebView memory bloat.
type ipcTransport struct {
	ctx    context.Context
	sessID string
	inCh   chan *sshproxy.WSMessage
	closed chan struct{}
	once   sync.Once
	emitFn func(payload map[string]any) // optional override for tests

	emitMu    sync.Mutex // serializes event emission; lock order: emitMu -> outMu
	outMu     sync.Mutex
	outBuf    strings.Builder
	flushTick *time.Timer
	isClosed  bool
}

func newIPCTransport(ctx context.Context, sessID string) *ipcTransport {
	return &ipcTransport{
		ctx:    ctx,
		sessID: sessID,
		inCh:   make(chan *sshproxy.WSMessage, 128),
		closed: make(chan struct{}),
	}
}

func (t *ipcTransport) emit(payload map[string]any) {
	if t.emitFn != nil {
		t.emitFn(payload)
		return
	}
	if t.ctx != nil {
		wailsRuntime.EventsEmit(t.ctx, "ssh:event", payload)
	}
}

func (t *ipcTransport) drainOutputLocked() string {
	if t.flushTick != nil {
		t.flushTick.Stop()
		t.flushTick = nil
	}
	if t.outBuf.Len() == 0 {
		return ""
	}
	data := t.outBuf.String()
	t.outBuf.Reset()
	return data
}

func (t *ipcTransport) flushOutput() {
	t.emitMu.Lock()
	defer t.emitMu.Unlock()

	t.outMu.Lock()
	if t.isClosed {
		t.outMu.Unlock()
		return
	}
	data := t.drainOutputLocked()
	t.outMu.Unlock()

	if data != "" {
		t.emit(map[string]any{
			"session_id": t.sessID,
			"event":      "msg",
			"msg":        sshproxy.WSMessage{Type: "output", Data: data},
		})
	}
}

func (t *ipcTransport) Send(msg sshproxy.WSMessage) error {
	if msg.Type == "output" {
		if msg.Data == "" {
			return nil
		}
		t.outMu.Lock()
		if t.isClosed {
			t.outMu.Unlock()
			return io.EOF
		}
		t.outBuf.WriteString(msg.Data)
		shouldFlushNow := t.outBuf.Len() >= ipcFlushMaxBytes
		if !shouldFlushNow && t.flushTick == nil {
			t.flushTick = time.AfterFunc(ipcFlushInterval, t.flushOutput)
		}
		t.outMu.Unlock()

		if shouldFlushNow {
			t.flushOutput()
		}
		return nil
	}

	t.emitMu.Lock()
	defer t.emitMu.Unlock()

	t.outMu.Lock()
	if t.isClosed {
		t.outMu.Unlock()
		return io.EOF
	}
	pending := t.drainOutputLocked()
	t.outMu.Unlock()

	if pending != "" {
		t.emit(map[string]any{
			"session_id": t.sessID,
			"event":      "msg",
			"msg":        sshproxy.WSMessage{Type: "output", Data: pending},
		})
	}
	t.emit(map[string]any{
		"session_id": t.sessID,
		"event":      "msg",
		"msg":        msg,
	})
	return nil
}

func (t *ipcTransport) Recv() (*sshproxy.WSMessage, error) {
	select {
	case <-t.closed:
		return nil, io.EOF
	case msg, ok := <-t.inCh:
		if !ok {
			return nil, io.EOF
		}
		return msg, nil
	}
}

func (t *ipcTransport) Push(msg *sshproxy.WSMessage) error {
	select {
	case <-t.closed:
		return io.EOF
	case t.inCh <- msg:
		return nil
	}
}

func (t *ipcTransport) Close() error {
	t.once.Do(func() {
		t.emitMu.Lock()
		defer t.emitMu.Unlock()

		t.outMu.Lock()
		pending := t.drainOutputLocked()
		t.isClosed = true
		t.outMu.Unlock()

		close(t.closed)
		if pending != "" {
			t.emit(map[string]any{
				"session_id": t.sessID,
				"event":      "msg",
				"msg":        sshproxy.WSMessage{Type: "output", Data: pending},
			})
		}
		t.emit(map[string]any{
			"session_id": t.sessID,
			"event":      "closed",
		})
	})
	return nil
}

// DesktopApp runs the entire API & SSH engine strictly in-process over Wails IPC.
type DesktopApp struct {
	ctx           context.Context
	store         *store.Store
	vault         *crypto.Vault
	sessions      *auth.Manager
	handler       http.Handler
	desktopSecret string
	dataDir       string
	recordingsDir string

	sessMu         sync.Mutex
	activeSessions map[string]*ipcTransport
}

func newDesktopApp() (*DesktopApp, error) {
	dataDir, err := resolveDesktopDataDir()
	if err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dataDir, "ssh_guard.db")
	recordingsDir := filepath.Join(dataDir, "recordings")

	st, err := store.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	vault := crypto.NewVault()

	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("generate desktop secret: %w", err)
	}
	desktopSecret := hex.EncodeToString(secretBytes)

	sessionMgr := auth.NewManager(false, true)
	handler := api.NewServer(st, vault, sessionMgr, recordingsDir, api.ServerOptions{
		DesktopMode:   true,
		DesktopSecret: desktopSecret,
	})

	return &DesktopApp{
		store:          st,
		vault:          vault,
		sessions:       sessionMgr,
		handler:        handler,
		desktopSecret:  desktopSecret,
		dataDir:        dataDir,
		recordingsDir:  recordingsDir,
		activeSessions: make(map[string]*ipcTransport),
	}, nil
}

func (a *DesktopApp) startup(ctx context.Context) {
	a.ctx = ctx
}

func (a *DesktopApp) shutdown(ctx context.Context) {
	log.Println("SSH-Guard Desktop shutting down: closing SSH sessions and wiping in-memory KEK...")
	a.sessMu.Lock()
	toClose := make([]*ipcTransport, 0, len(a.activeSessions))
	for id, tr := range a.activeSessions {
		toClose = append(toClose, tr)
		delete(a.activeSessions, id)
	}
	a.sessMu.Unlock()
	for _, tr := range toClose {
		_ = tr.Close()
	}

	if a.vault != nil {
		a.vault.Lock()
	}
	if a.store != nil {
		_ = a.store.Close()
	}
}

// GetRuntimeConfig returns desktop runtime metadata over Wails IPC.
func (a *DesktopApp) GetRuntimeConfig() RuntimeConfig {
	return RuntimeConfig{
		Mode:     "desktop",
		Platform: goruntime.GOOS + "/" + goruntime.GOARCH,
		DataDir:  a.dataDir,
	}
}

// APIRequest executes REST API calls directly in memory without opening any TCP port.
// Eliminates macOS WKWebView CORS, Origin: null, ATS, and local proxy (Clash) interference.
func (a *DesktopApp) APIRequest(method, path, body, sessionToken string) APIResponse {
	if !strings.HasPrefix(path, "/api/") {
		return APIResponse{Status: http.StatusForbidden, Body: `{"error":"invalid api path"}`}
	}
	if method == "" {
		method = http.MethodGet
	}

	req := httptest.NewRequest(method, "http://desktop.local"+path, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:0"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Requested-With", "SSHGuard")
	req.Header.Set("X-Desktop-Secret", a.desktopSecret)
	if sessionToken != "" {
		req.Header.Set(auth.HeaderName, sessionToken)
	}

	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)

	return APIResponse{
		Status: rec.Code,
		Body:   rec.Body.String(),
	}
}

// OpenSSHSession starts an in-process SSH session bound to Wails native events.
func (a *DesktopApp) OpenSSHSession(sessID string, hostID int64, cols, rows int, sessionToken string) error {
	if sessID == "" {
		return errors.New("empty session id")
	}
	if !a.vault.IsUnlocked() || !a.sessions.ValidateToken(sessionToken) {
		return errors.New("vault locked or session expired")
	}
	host, err := a.store.GetHost(hostID)
	if err != nil {
		return err
	}

	tr := newIPCTransport(a.ctx, sessID)
	a.sessMu.Lock()
	old, exists := a.activeSessions[sessID]
	a.activeSessions[sessID] = tr
	a.sessMu.Unlock()
	if exists {
		_ = old.Close()
	}

	go func() {
		defer func() {
			a.sessMu.Lock()
			if cur, ok := a.activeSessions[sessID]; ok && cur == tr {
				delete(a.activeSessions, sessID)
			}
			a.sessMu.Unlock()
			debug.FreeOSMemory()
		}()
		sshproxy.ServeTransport(tr, sshproxy.BridgeConfig{
			Store:         a.store,
			Vault:         a.vault,
			Host:          host,
			ClientIP:      "desktop-local",
			RecordingsDir: a.recordingsDir,
			InitialCols:   cols,
			InitialRows:   rows,
		})
	}()
	return nil
}

// SendSSHMessage forwards terminal keystrokes or resize events to an active SSH session.
func (a *DesktopApp) SendSSHMessage(sessID, rawJSON string) error {
	a.sessMu.Lock()
	tr, ok := a.activeSessions[sessID]
	a.sessMu.Unlock()
	if !ok {
		return errors.New("session not found")
	}
	var msg sshproxy.WSMessage
	if err := json.Unmarshal([]byte(rawJSON), &msg); err != nil {
		return err
	}
	return tr.Push(&msg)
}

// CloseSSHSession terminates an active in-process SSH session.
func (a *DesktopApp) CloseSSHSession(sessID string) {
	a.sessMu.Lock()
	tr, ok := a.activeSessions[sessID]
	if ok {
		delete(a.activeSessions, sessID)
	}
	a.sessMu.Unlock()
	if ok {
		_ = tr.Close()
	}
}

// PickPrivateKeyFile opens the native OS file picker (defaults to ~/.ssh) and returns key text.
func (a *DesktopApp) PickPrivateKeyFile() (string, error) {
	if a.ctx == nil {
		return "", errors.New("desktop context not ready")
	}
	defaultDir := ""
	if home, err := os.UserHomeDir(); err == nil {
		sshDir := filepath.Join(home, ".ssh")
		if info, err := os.Stat(sshDir); err == nil && info.IsDir() {
			defaultDir = sshDir
		} else {
			defaultDir = home
		}
	}

	selected, err := wailsRuntime.OpenFileDialog(a.ctx, wailsRuntime.OpenDialogOptions{
		Title:            "选择 SSH 私钥文件 (如 id_ed25519 / id_rsa / *.pem)",
		DefaultDirectory: defaultDir,
		ShowHiddenFiles:  true,
	})
	if err != nil {
		return "", err
	}
	if selected == "" {
		return "", nil
	}

	info, err := os.Stat(selected)
	if err != nil {
		return "", err
	}
	if info.Size() > 64*1024 {
		return "", errors.New("文件超过 64KB，请确认选择的是有效的 SSH 私钥文件")
	}
	data, err := os.ReadFile(selected)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// OpenDataDir reveals the encrypted database & recordings directory in Finder / Explorer.
func (a *DesktopApp) OpenDataDir() error {
	if err := os.MkdirAll(a.dataDir, 0o700); err != nil {
		return err
	}
	switch goruntime.GOOS {
	case "darwin":
		return exec.Command("open", a.dataDir).Start()
	case "windows":
		return exec.Command("explorer", a.dataDir).Start()
	default:
		return exec.Command("xdg-open", a.dataDir).Start()
	}
}

func resolveDesktopDataDir() (string, error) {
	if custom := os.Getenv("SSH_GUARD_DATA_DIR"); custom != "" {
		return custom, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "./data", nil
	}
	return filepath.Join(base, "SSH-Guard"), nil
}

func main() {
	// Keep Go heap footprint tight on desktop while leaving headroom for the
	// transient 64 MiB Argon2id unlock buffer (reclaimed immediately after KEK derivation).
	if os.Getenv("GOGC") == "" {
		debug.SetGCPercent(50)
	}
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(96 << 20)
	}

	app, err := newDesktopApp()
	if err != nil {
		log.Fatalf("failed to initialize SSH-Guard Desktop: %v", err)
	}

	staticFS, err := fs.Sub(web.StaticFiles, "static")
	if err != nil {
		log.Fatalf("failed to mount embedded assets: %v", err)
	}

	// Register native Edit menu on macOS so Cmd+C / Cmd+V / Cmd+A work seamlessly in WebView
	var appMenu *menu.Menu
	if goruntime.GOOS == "darwin" {
		appMenu = menu.NewMenu()
		appMenu.Append(menu.AppMenu())
		appMenu.Append(menu.EditMenu())
		appMenu.Append(menu.WindowMenu())
	}

	err = wails.Run(&options.App{
		Title:            "SSH-Guard · 轻量零信任 SSH 桌面端",
		Width:            1280,
		Height:           820,
		MinWidth:         960,
		MinHeight:        640,
		BackgroundColour: &options.RGBA{R: 13, G: 17, B: 23, A: 255},
		AssetServer: &assetserver.Options{
			Assets: staticFS,
		},
		Menu:       appMenu,
		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
		Bind: []interface{}{
			app,
		},
		Mac: &mac.Options{
			TitleBar:             mac.TitleBarDefault(),
			Appearance:           mac.NSAppearanceNameDarkAqua,
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			About: &mac.AboutInfo{
				Title:   "SSH-Guard Desktop v0.4",
				Message: "轻量级零信任 SSH 管理面板 · 纯进程内 IPC 零端口暴露 · 纯离线内置终端 · 信封加密与 TOFU 防 MITM",
			},
		},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			Theme:                windows.Dark,
			WebviewGpuIsDisabled: true,
		},
	})
	if err != nil {
		log.Fatalf("wails runtime error: %v", err)
	}
}
