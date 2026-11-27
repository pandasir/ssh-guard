package desktopui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime/debug"
	"sync"
	"time"

	"ssh-guard/internal/sshproxy"
	"ssh-guard/internal/store"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

const (
	replayMaxEventGap = 1500 * time.Millisecond
	replayUIInterval  = 200 * time.Millisecond
)

// blockedReplayConn satisfies terminal.Options.Conn for read-only .cast replay.
// Read blocks until Close; Write discards input; Resize is a no-op so
// ResizeByConn=true locks the terminal to the recording header's width/height.
type blockedReplayConn struct {
	closed chan struct{}
	once   sync.Once
}

func newBlockedReplayConn() *blockedReplayConn {
	return &blockedReplayConn{closed: make(chan struct{})}
}

func (c *blockedReplayConn) Read([]byte) (int, error) {
	<-c.closed
	return 0, io.EOF
}
func (c *blockedReplayConn) Write(p []byte) (int, error) { return len(p), nil }
func (c *blockedReplayConn) Resize(int, int) error       { return nil }
func (c *blockedReplayConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

type replayController struct {
	recID    string
	hostName string
	width    int
	height   int

	reader *sshproxy.CastReader
	term   *terminal.Terminal
	cancel context.CancelFunc

	mu       sync.Mutex
	paused   bool
	speed    float64
	speedStr string
	progress float64
	finished bool
	pauseCh  chan struct{}
}

func computeReplayDelay(eventTime, elapsed, speed float64) time.Duration {
	if speed <= 0 {
		speed = 1
	}
	delta := (eventTime - elapsed) / speed
	if delta <= 0 {
		return 0
	}
	d := time.Duration(delta * float64(time.Second))
	if d > replayMaxEventGap {
		return replayMaxEventGap
	}
	return d
}

func parseSpeedLabel(label string) float64 {
	switch label {
	case "1x":
		return 1
	case "4x":
		return 4
	default:
		return 2
	}
}

func (rc *replayController) setPaused(paused bool) {
	rc.mu.Lock()
	rc.paused = paused
	if !paused {
		select {
		case rc.pauseCh <- struct{}{}:
		default:
		}
	}
	rc.mu.Unlock()
}

func (rc *replayController) togglePaused() bool {
	rc.mu.Lock()
	rc.paused = !rc.paused
	paused := rc.paused
	if !paused {
		select {
		case rc.pauseCh <- struct{}{}:
		default:
		}
	}
	rc.mu.Unlock()
	return paused
}

func (rc *replayController) setSpeedLabel(label string) {
	rc.mu.Lock()
	rc.speedStr = label
	rc.speed = parseSpeedLabel(label)
	rc.mu.Unlock()
}

func (rc *replayController) snapshot() (paused bool, speedStr string, progress float64, finished bool) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.paused, rc.speedStr, rc.progress, rc.finished
}

func (rc *replayController) close() {
	if rc == nil {
		return
	}
	if rc.cancel != nil {
		rc.cancel()
	}
	if rc.reader != nil {
		_ = rc.reader.Close()
	}
	if rc.term != nil {
		_ = rc.term.Close()
		rc.term = nil
	}
}

func (a *App) openAuditModal() {
	logs, err := a.store.ListAuditLogs(150)
	if err != nil {
		a.pushToast("读取审计日志失败", err.Error(), "error")
		return
	}
	a.auditLogs = logs
	a.auditOpen = true
}

func (a *App) openRecordingsModal() {
	recs, err := a.store.ListRecordings(100)
	if err != nil {
		a.pushToast("读取录像列表失败", err.Error(), "error")
		return
	}
	a.recordings = recs
	a.recordingsOpen = true
}

func (a *App) startReplay(rec store.Recording) {
	a.touchActivity()
	a.stopReplay()
	a.recordingsOpen = false

	cr, err := sshproxy.OpenCastReader(rec.FilePath)
	if err != nil {
		a.pushToast("打开录像失败", err.Error(), "error")
		return
	}
	if err := a.ensureTerminalRuntime(); err != nil {
		_ = cr.Close()
		a.pushToast("终端运行库初始化失败", err.Error(), "error")
		return
	}
	hdr := cr.Header()

	term, err := terminal.New(terminal.Options{
		Conn:          newBlockedReplayConn(),
		ResizeByConn:  true,
		NoBlink:       true,
		NoExitMessage: true,
		Scrollback:    a.scrollbackBytes,
		ImageLimit:    -1,
		Font:          terminal.Font{Size: 13},
		Theme:         terminal.DarkTheme(),
	})
	if err != nil {
		_ = cr.Close()
		a.pushToast("初始化回放终端失败", err.Error(), "error")
		return
	}
	term.Resize(hdr.Width, hdr.Height)

	ctx, cancel := context.WithCancel(context.Background())
	rc := &replayController{
		recID:    rec.ID,
		hostName: rec.HostName,
		width:    hdr.Width,
		height:   hdr.Height,
		reader:   cr,
		term:     term,
		cancel:   cancel,
		speed:    2,
		speedStr: "2x",
		pauseCh:  make(chan struct{}, 1),
	}
	a.replay = rc
	a.replayOpen = true

	go a.runReplayLoop(ctx, rc)
}

func (a *App) runReplayLoop(ctx context.Context, rc *replayController) {
	elapsed := 0.0
	var lastUIRefresh time.Time
	for {
		if ctx.Err() != nil {
			return
		}
		ev, err := rc.reader.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				rc.mu.Lock()
				rc.finished = true
				rc.mu.Unlock()
				a.runUI(func() {})
			}
			return
		}

		for {
			rc.mu.Lock()
			paused := rc.paused
			speed := rc.speed
			rc.mu.Unlock()

			if paused {
				select {
				case <-ctx.Done():
					return
				case <-rc.pauseCh:
					continue
				}
			}

			delay := computeReplayDelay(ev.Time, elapsed, speed)
			if delay > 0 {
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}

			// Re-check paused right after sleep before feeding frame.
			rc.mu.Lock()
			stillPaused := rc.paused
			rc.mu.Unlock()
			if stillPaused {
				continue
			}
			break
		}

		elapsed = ev.Time
		rc.term.Feed([]byte(ev.Data))
		rc.mu.Lock()
		rc.progress = ev.Time
		rc.mu.Unlock()
		now := time.Now()
		if lastUIRefresh.IsZero() || now.Sub(lastUIRefresh) >= replayUIInterval {
			lastUIRefresh = now
			a.runUI(func() {})
		}
	}
}

func (a *App) stopReplay() {
	hadReplay := a.replay != nil
	if a.replay != nil {
		a.replay.close()
		a.replay = nil
	}
	a.replayOpen = false
	if hadReplay {
		debug.FreeOSMemory()
	}
}

func (a *App) renderAuditModal(c *ui.Context) {
	if !a.auditOpen {
		return
	}
	cols := []ui.TableColumn{
		{Title: "时间", Width: 155},
		{Title: "级别", Width: 82},
		{Title: "事件", Width: 145},
		{Title: "主机", Width: 120},
		{Title: "来源", Width: 110},
		{Title: "详情"},
	}

	m := ui.DialogBase(c, &a.auditOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.72))
		panel.Width(920).Height(560).Padding(18).Gap(12).Radius(10).
			Background(ui.Hex("#161b22")).Border(1, ui.Hex("#30363d"))

		ui.Row(c).Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
			ui.Text(c, fmt.Sprintf("安全审计日志 (%d 条)", len(a.auditLogs))).FontSize(16).Bold()
			if ui.Button(c, "关闭").Padding(4, 10).Clicked() {
				a.auditOpen = false
				a.auditLogs = nil
				debug.FreeOSMemory()
			}
		})

		if len(a.auditLogs) == 0 {
			ui.Column(c).Grow(1).Center().Children(func() {
				ui.Text(c, "暂无审计日志").FontSize(13).TextColor(ui.Hex("#8b949e"))
			})
		} else {
			a.auditState.RowHeight = 30
			ui.Table(c.Key("audit-table"), &a.auditState, cols, len(a.auditLogs), func(row, col int) {
				l := a.auditLogs[row]
				switch col {
				case 0:
					ui.Text(c, l.CreatedAt.Local().Format("2006-01-02 15:04:05")).FontSize(12).SingleLine()
				case 1:
					colColor := ui.Hex("#79c0ff")
					if l.Severity == "WARN" {
						colColor = ui.Hex("#d29922")
					} else if l.Severity == "CRITICAL" {
						colColor = ui.Hex("#f85149")
					}
					ui.Text(c, l.Severity).FontSize(12).Bold().TextColor(colColor).SingleLine()
				case 2:
					ui.Text(c, l.EventType).FontSize(12).SingleLine()
				case 3:
					ui.Text(c, l.HostName).FontSize(12).SingleLine()
				case 4:
					ui.Text(c, l.ClientIP).FontSize(12).TextColor(ui.Hex("#8b949e")).SingleLine()
				case 5:
					ui.Text(c, l.Detail).FontSize(12).SingleLine()
				}
			}).Grow(1)
		}
	})

	if m.Dismissed() {
		a.auditLogs = nil
		debug.FreeOSMemory()
	}
}

func (a *App) renderRecordingsModal(c *ui.Context) {
	if !a.recordingsOpen {
		return
	}
	cols := []ui.TableColumn{
		{Title: "会话 ID", Width: 240},
		{Title: "主机", Width: 150},
		{Title: "开始时间", Width: 165},
		{Title: "大小", Width: 90, Align: ui.End},
		{Title: "操作", Width: 100},
	}

	m := ui.DialogBase(c, &a.recordingsOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.72))
		panel.Width(860).Height(520).Padding(18).Gap(12).Radius(10).
			Background(ui.Hex("#161b22")).Border(1, ui.Hex("#30363d"))

		ui.Row(c).Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
			ui.Text(c, fmt.Sprintf("终端会话录像 (asciinema .cast · %d 条)", len(a.recordings))).FontSize(16).Bold()
			if ui.Button(c, "关闭").Padding(4, 10).Clicked() {
				a.recordingsOpen = false
				a.recordings = nil
				debug.FreeOSMemory()
			}
		})

		if len(a.recordings) == 0 {
			ui.Column(c).Grow(1).Center().Children(func() {
				ui.Text(c, "暂无会话录像").FontSize(13).TextColor(ui.Hex("#8b949e"))
			})
		} else {
			a.recState.RowHeight = 34
			ui.Table(c.Key("recordings-table"), &a.recState, cols, len(a.recordings), func(row, col int) {
				r := a.recordings[row]
				switch col {
				case 0:
					ui.Text(c, r.ID).FontSize(12).SingleLine()
				case 1:
					ui.Text(c, r.HostName).FontSize(12).SingleLine()
				case 2:
					ui.Text(c, r.StartedAt.Local().Format("2006-01-02 15:04:05")).FontSize(12).SingleLine()
				case 3:
					ui.Text(c, fmt.Sprintf("%.1f KB", float64(r.SizeBytes)/1024)).FontSize(12).SingleLine()
				case 4:
					rec := r
					ui.PrimaryButton(c, "回放").Padding(3, 10).OnClick(func() {
						a.startReplay(rec)
					})
				}
			}).Grow(1)
		}
	})

	if m.Dismissed() {
		a.recordings = nil
		debug.FreeOSMemory()
	}
}

func (a *App) renderReplayModal(c *ui.Context) {
	if !a.replayOpen || a.replay == nil || a.replay.term == nil {
		return
	}
	rc := a.replay
	paused, speedStr, progress, finished := rc.snapshot()

	m := ui.DialogBase(c, &a.replayOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.72))
		panel.Width(940).Height(600).Padding(18).Gap(10).Radius(10).
			Background(ui.Hex("#161b22")).Border(1, ui.Hex("#30363d"))

		ui.Row(c).Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
			ui.Text(c, fmt.Sprintf("会话回放 · %s (%s · %dx%d)", rc.hostName, rc.recID, rc.width, rc.height)).
				FontSize(15).Bold().SingleLine()
			if ui.Button(c, "关闭").Padding(4, 10).Clicked() {
				a.stopReplay()
			}
		})

		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			toggleLabel := "暂停"
			if paused {
				toggleLabel = "继续"
			}
			if ui.PrimaryButton(c, toggleLabel).Disabled(finished).Padding(5, 12).Clicked() {
				rc.togglePaused()
			}
			curSpeed := speedStr
			if ui.Select(c.Key("replay-speed"), &curSpeed, []string{"1x", "2x", "4x"}).Label("回放倍速").Changed() {
				rc.setSpeedLabel(curSpeed)
			}
			progText := fmt.Sprintf("%.1fs", progress)
			if finished {
				progText += " · 已播完"
			}
			ui.Text(c, progText).FontSize(12).TextColor(ui.Hex("#8b949e"))
		})

		ui.Box(c).Grow(1).FillWidth().Padding(6).Radius(6).
			Background(ui.Hex("#0a0d12")).Border(1, ui.Hex("#30363d")).Children(func() {
			terminal.View(c.Key("replay-term"), rc.term).Fill()
		})
	})

	if m.Dismissed() {
		a.stopReplay()
	}
}
