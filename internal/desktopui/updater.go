package desktopui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/netproxy"
)

const (
	defaultUpdateGitHubRepo   = "pandasir/ssh-guard"
	defaultManifestBaseURL    = "https://github.com/" + defaultUpdateGitHubRepo + "/releases/latest/download"
	defaultReleasesPageURL    = "https://github.com/" + defaultUpdateGitHubRepo + "/releases"
	maxUpdateManifestBytes    = 1 << 20 // 1 MiB cap matching mygo.Updater
	updateCheckTimeout        = 30 * time.Second
	updateProgressMinInterval = 100 * time.Millisecond
)

type updatePhase int

const (
	updateIdle updatePhase = iota
	updateChecking
	updateUpToDate
	updateAvailable
	updateDownloading
	updateReady
	updateError
)

// updateOffer describes a newer release discovered by an updateBackend.
type updateOffer struct {
	Version        string
	Notes          string
	Date           time.Time
	ReleaseURL     string
	CanInstall     bool
	FallbackReason string
	Install        func(ctx context.Context, progress func(downloaded, total int64)) error
}

// updateBackend abstracts update checking so headless tests can inject fakes
// while production uses mygo.Updater with a static manifest fallback.
type updateBackend interface {
	Check(ctx context.Context) (*updateOffer, error)
}

type updateState struct {
	phase            updatePhase
	offer            *updateOffer
	downloaded       int64
	total            int64
	errMsg           string
	cancel           context.CancelFunc
	gen              uint64
	installedVersion string
}

type remoteUpdateManifest struct {
	Version string `json:"version"`
	Notes   string `json:"notes"`
	Date    string `json:"date"`
}

// defaultUpdateBackend wraps mygo.Updater for writable desktop installations
// and degrades to a read-only HTTPS manifest check on Windows portable builds,
// read-only mounts, or dev builds. It performs zero network activity until
// Check is explicitly invoked.
type defaultUpdateBackend struct {
	isPortable      func() bool
	isDev           func() bool
	updaterEnabled  func() bool
	updaterCheck    func(ctx context.Context) (*mygo.Update, error)
	currentVersion  func() string
	platformTarget  func() string
	manifestBaseURL string
	httpClient      *http.Client

	clientOnce sync.Once
	lazyClient *http.Client
}

func newDefaultUpdateBackend() *defaultUpdateBackend {
	return &defaultUpdateBackend{
		isPortable:      isPortableBuild,
		isDev:           mygo.IsDev,
		updaterEnabled:  mygo.Updater.Enabled,
		updaterCheck:    mygo.Updater.Check,
		currentVersion:  appDisplayVersion,
		platformTarget:  func() string { return runtime.GOOS + "-" + runtime.GOARCH },
		manifestBaseURL: defaultManifestBaseURL,
	}
}

func (b *defaultUpdateBackend) client() *http.Client {
	if b.httpClient != nil {
		return b.httpClient
	}
	b.clientOnce.Do(func() {
		tr := netproxy.Transport().Clone()
		tr.ResponseHeaderTimeout = updateCheckTimeout
		b.lazyClient = &http.Client{Transport: tr}
	})
	return b.lazyClient
}

func (b *defaultUpdateBackend) Check(ctx context.Context) (*updateOffer, error) {
	portable := b.isPortable != nil && b.isPortable()
	canSelfUpdate := !portable && b.updaterEnabled != nil && b.updaterEnabled()
	if canSelfUpdate {
		up, err := b.updaterCheck(ctx)
		if err != nil {
			return nil, err
		}
		if up == nil {
			return nil, nil
		}
		return &updateOffer{
			Version:    strings.TrimPrefix(strings.TrimSpace(up.Version), "v"),
			Notes:      strings.TrimSpace(up.Notes),
			Date:       up.Date,
			ReleaseURL: releasePageURLForVersion(up.Version),
			CanInstall: true,
			Install:    up.Install,
		}, nil
	}

	reason := b.fallbackReason(portable)
	return b.checkManifestFallback(ctx, reason)
}

func (b *defaultUpdateBackend) fallbackReason(portable bool) string {
	if portable {
		return "当前为 Windows 单文件便携版，不支持应用内原地替换，请前往 GitHub 下载最新便携版。"
	}
	if b.isDev != nil && b.isDev() {
		return "当前为开发模式构建，不支持应用内原地替换，请前往 GitHub 下载正式发布包。"
	}
	return "当前安装目录不可写（例如只读磁盘镜像或受限目录），不支持应用内原地替换，请前往 GitHub 下载安装包。"
}

func (b *defaultUpdateBackend) checkManifestFallback(ctx context.Context, reason string) (*updateOffer, error) {
	target := runtime.GOOS + "-" + runtime.GOARCH
	if b.platformTarget != nil {
		target = b.platformTarget()
	}
	baseURL := b.manifestBaseURL
	if baseURL == "" {
		baseURL = defaultManifestBaseURL
	}
	manifestURL := strings.TrimRight(baseURL, "/") + "/update-" + url.PathEscape(target) + ".json"

	u, err := url.Parse(manifestURL)
	if err != nil {
		return nil, fmt.Errorf("解析更新清单地址失败: %w", err)
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && isLoopbackHost(u.Hostname())) {
		return nil, fmt.Errorf("拒绝通过非 HTTPS 连接检查更新: %s", manifestURL)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ssh-guard-updater")

	resp, err := b.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("连接更新源失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, errors.New("当前发布版本尚未提供该平台的更新清单")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("获取更新清单失败 (%s)", resp.Status)
	}

	var m remoteUpdateManifest
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxUpdateManifestBytes)).Decode(&m); err != nil {
		return nil, fmt.Errorf("解析更新清单失败: %w", err)
	}
	ver := strings.TrimPrefix(strings.TrimSpace(m.Version), "v")
	if ver == "" {
		return nil, errors.New("更新清单缺少版本号")
	}

	cur := appDisplayVersion()
	if b.currentVersion != nil {
		cur = b.currentVersion()
	}
	if compareSemver(ver, cur) <= 0 {
		return nil, nil
	}

	var parsedDate time.Time
	if strings.TrimSpace(m.Date) != "" {
		parsedDate, _ = time.Parse(time.RFC3339, strings.TrimSpace(m.Date))
	}
	return &updateOffer{
		Version:        ver,
		Notes:          strings.TrimSpace(m.Notes),
		Date:           parsedDate,
		ReleaseURL:     releasePageURLForVersion(ver),
		CanInstall:     false,
		FallbackReason: reason,
	}, nil
}

func releasePageURLForVersion(version string) string {
	ver := strings.TrimPrefix(strings.TrimSpace(version), "v")
	if ver == "" {
		return defaultReleasesPageURL + "/latest"
	}
	return defaultReleasesPageURL + "/tag/v" + url.PathEscape(ver)
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// compareSemver compares semantic version strings (ignoring optional leading "v"
// and "+build" metadata), returning -1, 0, or +1.
func compareSemver(a, b string) int {
	a = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(a), "v"), "V")
	b = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(b), "v"), "V")
	a, _, _ = strings.Cut(a, "+")
	b, _, _ = strings.Cut(b, "+")

	aCore, aPre, _ := strings.Cut(a, "-")
	bCore, bPre, _ := strings.Cut(b, "-")

	aParts := strings.Split(aCore, ".")
	bParts := strings.Split(bCore, ".")
	n := len(aParts)
	if len(bParts) > n {
		n = len(bParts)
	}
	for i := 0; i < n; i++ {
		var ai, bi int
		if i < len(aParts) {
			ai, _ = strconv.Atoi(aParts[i])
		}
		if i < len(bParts) {
			bi, _ = strconv.Atoi(bParts[i])
		}
		if ai != bi {
			if ai < bi {
				return -1
			}
			return 1
		}
	}

	switch {
	case aPre == "" && bPre == "":
		return 0
	case aPre == "":
		return 1
	case bPre == "":
		return -1
	}

	ap := strings.Split(aPre, ".")
	bp := strings.Split(bPre, ".")
	m := len(ap)
	if len(bp) < m {
		m = len(bp)
	}
	for i := 0; i < m; i++ {
		if ap[i] == bp[i] {
			continue
		}
		ai, aErr := strconv.Atoi(ap[i])
		bi, bErr := strconv.Atoi(bp[i])
		switch {
		case aErr == nil && bErr == nil:
			if ai < bi {
				return -1
			}
			return 1
		case aErr == nil:
			return -1
		case bErr == nil:
			return 1
		default:
			if ap[i] < bp[i] {
				return -1
			}
			return 1
		}
	}
	if len(ap) < len(bp) {
		return -1
	}
	if len(ap) > len(bp) {
		return 1
	}
	return 0
}

func formatUpdateBytes(n int64) string {
	if n < 0 {
		n = 0
	}
	const (
		kiB = 1024
		miB = 1024 * kiB
	)
	switch {
	case n >= miB:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(miB))
	case n >= kiB:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(kiB))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func (a *App) activeSessionCount() int {
	count := 0
	for _, tab := range a.tabs {
		if tab != nil && !tab.closing {
			count++
		}
	}
	return count
}

func (a *App) cancelUpdateOp() {
	if a.update.cancel != nil {
		a.update.cancel()
		a.update.cancel = nil
	}
}

// openAboutAndCheckUpdates opens the About dialog and triggers a manual update
// check unless a check, download, or ready-to-restart state is already active.
func (a *App) openAboutAndCheckUpdates() {
	a.aboutOpen = true
	if a.update.phase == updateChecking || a.update.phase == updateDownloading || a.update.phase == updateReady {
		return
	}
	a.startUpdateCheck()
}

// closeAboutModal dismisses the About dialog and cancels any in-flight check or
// download so zero background update activity continues after dismissal.
func (a *App) closeAboutModal() {
	a.aboutOpen = false
	switch a.update.phase {
	case updateChecking:
		a.update.gen++
		a.cancelUpdateOp()
		a.update.phase = updateIdle
		a.update.errMsg = ""
	case updateDownloading:
		a.update.gen++
		a.cancelUpdateOp()
		a.update.phase = updateAvailable
		a.update.downloaded = 0
		a.update.total = 0
		a.update.errMsg = ""
	}
}

func (a *App) startUpdateCheck() {
	if a.update.phase == updateChecking || a.update.phase == updateDownloading {
		return
	}
	a.cancelUpdateOp()
	a.update.gen++
	gen := a.update.gen

	ctx, cancel := context.WithTimeout(context.Background(), updateCheckTimeout)
	a.update.cancel = cancel
	a.update.phase = updateChecking
	a.update.errMsg = ""
	a.update.downloaded = 0
	a.update.total = 0

	backend := a.updateBackend
	if backend == nil {
		backend = newDefaultUpdateBackend()
	}

	go func() {
		defer cancel()
		offer, err := backend.Check(ctx)
		a.runUI(func() {
			if a.update.gen != gen {
				return
			}
			a.update.cancel = nil
			if err != nil {
				if errors.Is(err, context.Canceled) {
					a.update.phase = updateIdle
					return
				}
				a.update.phase = updateError
				a.update.errMsg = "检查更新失败: " + err.Error()
				return
			}
			if offer == nil {
				a.update.offer = nil
				a.update.phase = updateUpToDate
				return
			}
			a.update.offer = offer
			a.update.phase = updateAvailable
		})
	}()
}

func (a *App) startUpdateInstall() {
	offer := a.update.offer
	if offer == nil || !offer.CanInstall || offer.Install == nil {
		return
	}
	if a.update.phase == updateDownloading || a.update.phase == updateReady {
		return
	}

	a.cancelUpdateOp()
	a.update.gen++
	gen := a.update.gen

	ctx, cancel := context.WithCancel(context.Background())
	a.update.cancel = cancel
	a.update.phase = updateDownloading
	a.update.downloaded = 0
	a.update.total = 0
	a.update.errMsg = ""

	installFn := offer.Install
	targetVersion := offer.Version

	go func() {
		defer cancel()
		var (
			lastEmit       time.Time
			lastPct        = -1
			lastTotal      int64
			lastDownloaded int64
		)
		progress := func(downloaded, total int64) {
			now := time.Now()
			pct := -1
			if total > 0 {
				pct = int(downloaded * 100 / total)
				if pct > 100 {
					pct = 100
				}
			}
			reset := total != lastTotal || downloaded < lastDownloaded
			finished := total > 0 && downloaded >= total
			stepOK := pct >= 0 && pct != lastPct
			timeOK := lastEmit.IsZero() || now.Sub(lastEmit) >= updateProgressMinInterval
			if !reset && !finished && !(stepOK && timeOK) {
				return
			}
			lastEmit = now
			lastPct = pct
			lastTotal = total
			lastDownloaded = downloaded

			dl, tot := downloaded, total
			a.runUI(func() {
				if a.update.gen != gen || a.update.phase != updateDownloading {
					return
				}
				a.update.downloaded = dl
				a.update.total = tot
			})
		}

		err := installFn(ctx, progress)
		a.runUI(func() {
			if a.update.gen != gen {
				return
			}
			a.update.cancel = nil
			if err != nil {
				if errors.Is(err, context.Canceled) {
					a.update.phase = updateAvailable
					a.update.downloaded = 0
					a.update.total = 0
					return
				}
				a.update.phase = updateError
				a.update.errMsg = "下载或校验更新失败: " + err.Error()
				return
			}
			a.update.phase = updateReady
			a.update.installedVersion = targetVersion
			if a.update.total > 0 {
				a.update.downloaded = a.update.total
			}
		})
	}()
}

func (a *App) cancelUpdateInstall() {
	if a.update.phase != updateDownloading {
		return
	}
	a.update.gen++
	a.cancelUpdateOp()
	a.update.phase = updateAvailable
	a.update.downloaded = 0
	a.update.total = 0
	a.update.errMsg = ""
}

func (a *App) openUpdateReleasePage() {
	targetURL := defaultReleasesPageURL + "/latest"
	if a.update.offer != nil && strings.TrimSpace(a.update.offer.ReleaseURL) != "" {
		targetURL = a.update.offer.ReleaseURL
	}
	openFn := a.openURLFn
	if openFn == nil {
		openFn = mygo.Shell.OpenExternal
	}
	go func() {
		if err := openFn(targetURL); err != nil {
			a.runUI(func() {
				a.pushToast("无法打开浏览器", err.Error(), "error")
			})
		}
	}()
}

func (a *App) restartToApplyUpdate() {
	a.restartToApplyUpdateLocked(false)
}

func (a *App) restartToApplyUpdateLocked(uiLocked bool) {
	if a.update.phase != updateReady {
		return
	}
	relaunchFn := a.relaunchFn
	if relaunchFn == nil {
		relaunchFn = func() { mygo.App.Relaunch() }
	}
	a.shutdown(uiLocked)
	relaunchFn()
}
