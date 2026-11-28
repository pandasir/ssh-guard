package desktopui

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"runtime/debug"
	"sync"

	"ssh-guard/internal/crypto"
	"ssh-guard/internal/store"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

const (
	// DefaultScrollbackBytes caps off-heap libghostty-vt scrollback per terminal tab
	// (256 KiB holds ~2,500-3,000 lines while halving per-tab off-heap memory ceiling).
	DefaultScrollbackBytes = 256 * 1024

	// MaxPrivateKeyFileBytes enforces the same 64 KB cap as the previous desktop picker.
	MaxPrivateKeyFileBytes = 64 * 1024

	// Terminal font size defaults and bounds for menu zoom actions.
	DefaultTerminalFontSize float32 = 14
	MinTerminalFontSize     float32 = 10
	MaxTerminalFontSize     float32 = 28
)

// Options configures a native SSH-Guard desktop application instance.
type Options struct {
	DataDir         string
	ScrollbackBytes int
	IdleGuard       *IdleGuard

	// Test / injection hooks (nil uses production defaults).
	DialConn      func(ctx context.Context, addr string) (net.Conn, error)
	PickKeyFn     func() (string, error)
	OpenDirFn     func(dir string) error
	OpenURLFn     func(rawURL string) error
	RelaunchFn    func()
	UpdateBackend updateBackend
	SkipLoadVT    bool
}

// App holds the full native desktop state and coordinates vault lifecycle,
// multi-tab Ghostty SSH sessions, queued TOFU/MITM dialogs, and native tables.
type App struct {
	store           *store.Store
	vault           *crypto.Vault
	dataDir         string
	recordingsDir   string
	platform        string
	scrollbackBytes int
	idle            *IdleGuard

	dialConnFn     func(ctx context.Context, addr string) (net.Conn, error)
	pickKeyFn      func() (string, error)
	openDirFn      func(dir string) error
	openURLFn      func(rawURL string) error
	relaunchFn     func()
	updateBackend  updateBackend
	skipLoadVT     bool
	loadTerminalFn func() error
	termLoadMu     sync.Mutex
	termLoaded     bool

	win            *mygo.Window
	appMenu        *mygo.Menu
	lastMenuState  menuState
	menuStateReady bool
	theme          *ui.Theme
	uiMu           sync.Mutex // synchronizes headless ui.NewTester frames with background goroutines when win == nil
	shutdownOnce   sync.Once

	// Vault / Unlock UI state
	initialized   bool
	unlocked      bool
	unlockPass    string
	unlockConfirm string
	unlockErr     string
	unlockInfo    string
	unlockGen     int

	// Host inventory & search
	hosts        []store.Host
	hostQuery    string
	hostModal    hostFormState
	hostOpen     bool
	deleteAsk    bool
	deleteTarget *store.Host

	// Multi-tab Ghostty terminal workspace
	tabs          []*SessionTab
	activeTab     int
	nextTabSeq    int
	nextLocalSeq  int
	termFontSize  float32
	sidebarHidden bool
	aboutOpen     bool
	update        updateState

	// Queued TOFU / MITM verification modal
	tofuPending *tofuItem
	tofuQueue   []*tofuItem
	tofuOpen    bool
	tofuErr     string
	tofuBusy    bool

	// Audit logs & recordings tables + native streaming .cast replay
	auditOpen      bool
	auditLogs      []store.AuditLog
	auditState     ui.ListState
	recordingsOpen bool
	recordings     []store.Recording
	recState       ui.ListState
	replay         *replayController
	replayOpen     bool

	// Thread-safe toast queue drained on the UI frame.
	pendingToasts []ui.Toast
}

// ResolveDataDir resolves the desktop data directory from SSH_GUARD_DATA_DIR
// or os.UserConfigDir()/SSH-Guard.
func ResolveDataDir() (string, error) {
	if custom := os.Getenv("SSH_GUARD_DATA_DIR"); custom != "" {
		return custom, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "./data", nil
	}
	return filepath.Join(base, "SSH-Guard"), nil
}

// NewApp initializes the SQLite store, zero-trust vault, and desktop UI model.
func NewApp(opts Options) (*App, error) {
	dataDir := opts.DataDir
	if dataDir == "" {
		var err error
		dataDir, err = ResolveDataDir()
		if err != nil {
			return nil, err
		}
	}
	dbPath := filepath.Join(dataDir, "ssh_guard.db")
	recordingsDir := filepath.Join(dataDir, "recordings")

	st, err := store.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	_, metaErr := st.GetVaultMeta()
	initialized := !errors.Is(metaErr, store.ErrNotFound)

	scrollback := opts.ScrollbackBytes
	if scrollback <= 0 {
		scrollback = DefaultScrollbackBytes
	}
	idle := opts.IdleGuard
	if idle == nil {
		idle = NewIdleGuard(DefaultIdleTimeout, DefaultMaxLifetime)
	}

	app := &App{
		store:           st,
		vault:           crypto.NewVault(),
		dataDir:         dataDir,
		recordingsDir:   recordingsDir,
		platform:        goruntime.GOOS + "/" + goruntime.GOARCH,
		scrollbackBytes: scrollback,
		idle:            idle,
		dialConnFn:      opts.DialConn,
		pickKeyFn:       opts.PickKeyFn,
		openDirFn:       opts.OpenDirFn,
		openURLFn:       opts.OpenURLFn,
		relaunchFn:      opts.RelaunchFn,
		updateBackend:   opts.UpdateBackend,
		skipLoadVT:      opts.SkipLoadVT,
		theme:           newDesktopTheme(),
		initialized:     initialized,
		termFontSize:    DefaultTerminalFontSize,
	}
	if app.openDirFn == nil {
		app.openDirFn = openPathInOS
	}
	if app.openURLFn == nil {
		app.openURLFn = mygo.Shell.OpenExternal
	}
	if app.relaunchFn == nil {
		app.relaunchFn = func() { mygo.App.Relaunch() }
	}
	if app.updateBackend == nil {
		app.updateBackend = newDefaultUpdateBackend()
	}
	if app.pickKeyFn == nil {
		app.pickKeyFn = app.pickPrivateKeyNative
	}
	app.loadTerminalFn = func() error {
		if err := EnsurePortableRuntime(); err != nil {
			return fmt.Errorf("准备便携 Ghostty 运行库失败: %w", err)
		}
		if err := terminal.Load(); err != nil {
			return fmt.Errorf("load libghostty-vt: %w", err)
		}
		return nil
	}
	return app, nil
}

// ensureTerminalRuntime lazily prepares portable native libraries and loads
// libghostty-vt on first SSH connect or .cast replay while keeping startup lean.
func (a *App) ensureTerminalRuntime() error {
	if a.skipLoadVT {
		return nil
	}
	a.termLoadMu.Lock()
	defer a.termLoadMu.Unlock()
	if a.termLoaded {
		return nil
	}
	if a.loadTerminalFn != nil {
		if err := a.loadTerminalFn(); err != nil {
			return err
		}
	}
	a.termLoaded = true
	return nil
}

// Run opens the native MyGo window and blocks until quit; libghostty-vt loads lazily on first terminal use.
func (a *App) Run() error {
	mygo.Theme.SetSource(mygo.ThemeDark)
	mygo.App.WhenReady(func() {
		a.appMenu = a.buildAppMenu(goruntime.GOOS)
		a.syncAppMenu()
		mygo.App.SetMenu(a.appMenu)

		a.win = mygo.NewWindow(mygo.WindowOptions{
			Title:           "SSH-Guard · 原生零信任 SSH 桌面端",
			Width:           1280,
			Height:          820,
			MinWidth:        960,
			MinHeight:       640,
			StateKey:        "ssh-guard-main",
			BackgroundColor: "#0d1117",
			Content:         ui.View(a.View),
		})
		a.win.OnClosed(func() {
			a.Shutdown()
		})
	})

	defer a.Shutdown()
	return mygo.App.Run()
}

// runUI dispatches a state mutation onto the main UI thread when a window exists,
// or executes synchronously under uiMu during headless ui.NewTester unit tests.
func (a *App) runUI(fn func()) {
	if a.win != nil {
		a.win.Update(fn)
		return
	}
	a.uiMu.Lock()
	defer a.uiMu.Unlock()
	fn()
}

func (a *App) pushToast(title, desc, kind string) {
	a.pendingToasts = append(a.pendingToasts, ui.Toast{
		Title:       title,
		Description: desc,
		Type:        kind,
	})
}

func (a *App) touchActivity() {
	if a.unlocked && a.idle != nil {
		a.idle.Touch()
	}
}

// LockVault tears down all active sessions and replay players, wipes UI secrets
// and the in-memory KEK, writes an audit log entry, and returns to the lock screen.
func (a *App) LockVault(reason string) {
	if a.idle != nil {
		a.idle.Stop()
	}
	a.closeAllTabs()
	a.stopReplay()

	a.tofuPending = nil
	a.tofuQueue = nil
	a.tofuOpen = false
	a.tofuErr = ""
	a.tofuBusy = false

	a.hostOpen = false
	a.hostModal.clearSecrets()
	a.deleteAsk = false
	a.deleteTarget = nil
	a.auditOpen = false
	a.auditLogs = nil
	a.recordingsOpen = false
	a.recordings = nil
	a.hosts = nil
	a.hostQuery = ""

	a.clearUnlockInputs()
	a.unlocked = false
	if reason != "" {
		a.unlockInfo = reason
	}

	if a.vault != nil && a.vault.IsUnlocked() {
		a.vault.Lock()
		if a.store != nil {
			detail := "手动锁定凭证库，内存主密钥已清零"
			if reason != "" {
				detail = reason
			}
			a.store.AddAuditLog(0, "Vault", "VAULT_LOCK", "INFO", "desktop-local", detail)
		}
	}
	if a.store != nil {
		_ = a.store.ShrinkMemory()
	}
	debug.FreeOSMemory()
}

// Shutdown cleanly closes all sessions, terminals, replay players, vault KEK, and DB once.
func (a *App) Shutdown() {
	a.shutdown(false)
}

func (a *App) shutdown(uiLocked bool) {
	a.shutdownOnce.Do(func() {
		log.Println("SSH-Guard Desktop shutting down: closing SSH sessions and wiping in-memory KEK...")
		if a.idle != nil {
			a.idle.Stop()
		}
		needLock := a.win == nil && !uiLocked
		if needLock {
			a.uiMu.Lock()
		}
		a.update.gen++
		a.cancelUpdateOp()
		a.closeAllTabs()
		a.stopReplay()
		a.clearUnlockInputs()
		a.hostModal.clearSecrets()
		if needLock {
			a.uiMu.Unlock()
		}
		if a.vault != nil {
			a.vault.Lock()
		}
		if a.store != nil {
			_ = a.store.Close()
		}
	})
}

func newDesktopTheme() *ui.Theme {
	t := *ui.DarkTheme().WithAccent(ui.Hex("#2f81f7"))
	t.Background = ui.Hex("#0d1117")
	t.Surface = ui.Hex("#161b22")
	t.Border = ui.Hex("#30363d")
	t.Text = ui.Hex("#e6edf3")
	t.TextMuted = ui.Hex("#8b949e")
	t.Danger = ui.Hex("#f85149")
	t.Inverse = ui.Hex("#1f2630")
	t.InverseText = ui.Hex("#e6edf3")
	return &t
}

// View is the top-level MyGo native UI frame builder.
func (a *App) View(c *ui.Context) {
	if a.win == nil {
		a.uiMu.Lock()
		defer a.uiMu.Unlock()
	}
	defer a.syncAppMenu()
	if a.theme == nil {
		a.theme = newDesktopTheme()
	}
	c.SetTheme(a.theme)

	if len(a.pendingToasts) > 0 {
		for _, toast := range a.pendingToasts {
			c.AddToast(toast)
		}
		a.pendingToasts = a.pendingToasts[:0]
	}

	if !a.unlocked {
		a.renderUnlockView(c)
		a.renderAboutModal(c)
		return
	}

	// Main workspace layout: optional left sidebar + expanding terminal workspace.
	ui.Row(c).Fill().AlignItems(ui.Stretch).Background(ui.Hex("#0d1117")).Children(func() {
		if !a.sidebarHidden {
			a.renderSidebar(c)
		}
		a.renderWorkspace(c)
	})

	// Native modals rendered above the workspace.
	a.renderHostModal(c)
	a.renderDeleteHostDialog(c)
	a.renderTofuModal(c)
	a.renderAuditModal(c)
	a.renderRecordingsModal(c)
	a.renderReplayModal(c)
	a.renderAboutModal(c)
}

func (a *App) toggleSidebar() {
	if !a.unlocked {
		return
	}
	a.touchActivity()
	a.sidebarHidden = !a.sidebarHidden
}

func (a *App) loadHosts() error {
	hosts, err := a.store.ListHosts()
	if err != nil {
		return err
	}
	a.hosts = hosts
	return nil
}

func openPathInOS(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	switch goruntime.GOOS {
	case "darwin":
		return exec.Command("open", dir).Start()
	case "windows":
		return exec.Command("explorer", dir).Start()
	default:
		return exec.Command("xdg-open", dir).Start()
	}
}

func (a *App) pickPrivateKeyNative() (string, error) {
	defaultDir := ""
	if home, err := os.UserHomeDir(); err == nil {
		sshDir := filepath.Join(home, ".ssh")
		if info, err := os.Stat(sshDir); err == nil && info.IsDir() {
			defaultDir = sshDir
		} else {
			defaultDir = home
		}
	}
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:          a.win,
		Title:           "选择 SSH 私钥文件 (如 id_ed25519 / id_rsa / *.pem)",
		DefaultPath:     defaultDir,
		ShowHiddenFiles: true,
	})
	if err != nil {
		return "", err
	}
	if len(paths) == 0 || paths[0] == "" {
		return "", nil
	}
	return readPrivateKeyFile(paths[0])
}

func readPrivateKeyFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.Size() > MaxPrivateKeyFileBytes {
		return "", errors.New("文件超过 64KB，请确认选择的是有效的 SSH 私钥文件")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
