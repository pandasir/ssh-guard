package desktopui

import (
	"bytes"
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
)

const (
	tabConnMaxBuf          = 64 * 1024
	tabConnShrinkThreshold = 16 * 1024
)

// TabConn implements terminal.Options.Conn (io.ReadWriteCloser + Resize) as a
// lazy adapter around sshproxy.LiveSession. It allows creating the native
// Ghostty terminal immediately so layout measures real cols/rows and handshake
// progress renders inline, and supports in-tab reconnect after TOFU/MITM trust.
type TabConn struct {
	onActivity func()

	outMu     sync.Mutex
	outCond   *sync.Cond
	outBuf    bytes.Buffer
	outClosed bool

	mu     sync.Mutex
	cols   int
	rows   int
	live   *sshproxy.LiveSession
	closed bool

	closeOnce sync.Once
}

func newTabConn(onActivity func()) *TabConn {
	tc := &TabConn{
		onActivity: onActivity,
		cols:       120,
		rows:       32,
	}
	tc.outCond = sync.NewCond(&tc.outMu)
	return tc
}

// WriteLocal injects status, error, or disconnect messages into the terminal stream.
func (tc *TabConn) WriteLocal(text string) {
	if text == "" {
		return
	}
	tc.outMu.Lock()
	if !tc.outClosed {
		_, _ = tc.outBuf.WriteString(text)
		tc.outCond.Broadcast()
	}
	tc.outMu.Unlock()
}

// Size returns the latest terminal grid dimensions reported by terminal.View.
func (tc *TabConn) Size() (int, int) {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return tc.cols, tc.rows
}

// Attach binds a newly established LiveSession to this TabConn and pumps its
// output until the remote session ends (without closing the tab's terminal so
// reconnect in-place stays possible).
func (tc *TabConn) Attach(live *sshproxy.LiveSession, onEnded func()) {
	tc.mu.Lock()
	if tc.closed {
		tc.mu.Unlock()
		_ = live.Close()
		return
	}
	old := tc.live
	tc.live = live
	cols, rows := tc.cols, tc.rows
	tc.mu.Unlock()

	if old != nil {
		_ = old.Close()
	}
	if cols > 0 && rows > 0 {
		_ = live.Resize(cols, rows)
	}

	go func() {
		buf := make([]byte, 8192)
		for {
			n, err := live.Read(buf)
			if n > 0 {
				tc.outMu.Lock()
				for tc.outBuf.Len() >= tabConnMaxBuf && !tc.outClosed {
					tc.outCond.Wait()
				}
				if tc.outClosed {
					tc.outMu.Unlock()
					_ = live.Close()
					return
				}
				_, _ = tc.outBuf.Write(buf[:n])
				tc.outCond.Broadcast()
				tc.outMu.Unlock()
			}
			if err != nil {
				break
			}
		}

		tc.mu.Lock()
		isCurrent := tc.live == live
		if isCurrent {
			tc.live = nil
		}
		closed := tc.closed
		tc.mu.Unlock()

		_ = live.Close()
		if isCurrent && !closed && onEnded != nil {
			onEnded()
		}
	}()
}

// Detach closes any currently attached LiveSession while keeping the terminal view open.
func (tc *TabConn) Detach() {
	tc.mu.Lock()
	old := tc.live
	tc.live = nil
	tc.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
}

func (tc *TabConn) Read(p []byte) (int, error) {
	tc.outMu.Lock()
	defer tc.outMu.Unlock()
	for tc.outBuf.Len() == 0 && !tc.outClosed {
		tc.outCond.Wait()
	}
	if tc.outBuf.Len() == 0 && tc.outClosed {
		return 0, io.EOF
	}
	n, err := tc.outBuf.Read(p)
	if tc.outBuf.Len() == 0 && tc.outBuf.Cap() > tabConnShrinkThreshold {
		tc.outBuf = bytes.Buffer{}
	}
	tc.outCond.Broadcast()
	return n, err
}

func (tc *TabConn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if tc.onActivity != nil {
		tc.onActivity()
	}
	tc.mu.Lock()
	live := tc.live
	closed := tc.closed
	tc.mu.Unlock()
	if closed {
		return 0, io.ErrClosedPipe
	}
	if live == nil {
		// Discard keystrokes while connecting or disconnected without killing terminal's inputQueue.
		return len(p), nil
	}
	if _, err := live.Write(p); err != nil {
		// Ignore transient write errors on disconnect so inputQueue stays alive for in-tab reconnect.
		return len(p), nil
	}
	return len(p), nil
}

func (tc *TabConn) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	tc.mu.Lock()
	tc.cols = cols
	tc.rows = rows
	live := tc.live
	tc.mu.Unlock()
	if live != nil {
		return live.Resize(cols, rows)
	}
	return nil
}

func (tc *TabConn) Close() error {
	tc.closeOnce.Do(func() {
		tc.mu.Lock()
		tc.closed = true
		live := tc.live
		tc.live = nil
		tc.mu.Unlock()

		if live != nil {
			_ = live.Close()
		}

		tc.outMu.Lock()
		tc.outClosed = true
		if tc.outBuf.Len() == 0 && tc.outBuf.Cap() > tabConnShrinkThreshold {
			tc.outBuf = bytes.Buffer{}
		}
		tc.outCond.Broadcast()
		tc.outMu.Unlock()
	})
	return nil
}

// SessionTab owns one native Ghostty terminal instance and its SSH connection lifecycle.
type SessionTab struct {
	id         int
	host       store.Host
	conn       *TabConn
	term       *terminal.Terminal
	status     string // "connecting", "connected", "error", "idle"
	statusText string

	dialCancel context.CancelFunc
	closing    bool
}

func (a *App) connectHost(hostID int64) {
	a.touchActivity()
	host, err := a.store.GetHost(hostID)
	if err != nil {
		a.pushToast("连接失败", "读取主机配置失败: "+err.Error(), "error")
		return
	}
	if err := a.ensureTerminalRuntime(); err != nil {
		a.pushToast("终端运行库初始化失败", err.Error(), "error")
		return
	}

	a.nextTabSeq++
	tabID := a.nextTabSeq
	tc := newTabConn(a.touchActivity)

	term, err := terminal.New(terminal.Options{
		Conn:          tc,
		Scrollback:    a.scrollbackBytes,
		ImageLimit:    -1,
		NoBlink:       true,
		NoExitMessage: true,
		Font:          terminal.Font{Size: 14},
		Theme:         terminal.DarkTheme(),
	})
	if err != nil {
		_ = tc.Close()
		a.pushToast("终端初始化失败", err.Error(), "error")
		return
	}

	tab := &SessionTab{
		id:         tabID,
		host:       *host,
		conn:       tc,
		term:       term,
		status:     "connecting",
		statusText: fmt.Sprintf("正在连接 %s (%s@%s:%d)", host.Name, host.Username, host.Address, host.Port),
	}
	a.tabs = append(a.tabs, tab)
	a.activeTab = len(a.tabs) - 1

	a.startTabDial(tab)
}

func (a *App) reconnectTab(tab *SessionTab) {
	if tab == nil || tab.closing {
		return
	}
	a.touchActivity()
	// Refresh latest host credentials & metadata from SQLite before redialing.
	if fresh, err := a.store.GetHost(tab.host.ID); err == nil {
		tab.host = *fresh
	}
	if tab.dialCancel != nil {
		tab.dialCancel()
		tab.dialCancel = nil
	}
	tab.conn.Detach()
	tab.status = "connecting"
	tab.statusText = fmt.Sprintf("正在重连 %s (%s@%s:%d)", tab.host.Name, tab.host.Username, tab.host.Address, tab.host.Port)
	tab.conn.WriteLocal("\r\n")
	a.startTabDial(tab)
}

func (a *App) startTabDial(tab *SessionTab) {
	ctx, cancel := context.WithCancel(context.Background())
	tab.dialCancel = cancel
	hostCopy := tab.host
	tabID := tab.id
	tc := tab.conn

	go func() {
		// Brief yield so terminal.View can report initial layout cols/rows if mounted.
		time.Sleep(15 * time.Millisecond)
		if ctx.Err() != nil {
			return
		}
		cols, rows := tc.Size()
		live, err := sshproxy.DialSession(ctx, sshproxy.BridgeConfig{
			Store:         a.store,
			Vault:         a.vault,
			Host:          &hostCopy,
			ClientIP:      "desktop-local",
			RecordingsDir: a.recordingsDir,
			InitialCols:   cols,
			InitialRows:   rows,
			DialConn:      a.dialConnFn,
			OnStatus: func(status string) {
				tc.WriteLocal(fmt.Sprintf("\x1b[36m[SSH-Guard]\x1b[0m %s\r\n", status))
			},
			OnGuardBlock: func(ruleName, cmd string) {
				a.runUI(func() {
					a.pushToast("已拦截高危命令", fmt.Sprintf("规则: %s (%s)", ruleName, hostCopy.Name), "error")
				})
			},
		})
		if ctx.Err() != nil {
			if live != nil {
				_ = live.Close()
			}
			return
		}
		if err != nil {
			var untrusted *sshproxy.HostKeyUntrustedError
			if errors.As(err, &untrusted) {
				tc.WriteLocal("\x1b[33m[SSH-Guard]\x1b[0m 首次连接该主机，等待确认公钥指纹 (TOFU)...\r\n")
				a.runUI(func() {
					cur := a.findTabByID(tabID)
					if cur == nil || cur.closing {
						return
					}
					cur.status = "error"
					cur.statusText = "等待首次指纹信任 (TOFU)"
					a.enqueueTofu(&tofuItem{
						tabID:        tabID,
						host:         hostCopy,
						keyType:      untrusted.KeyType,
						fingerprint:  untrusted.Fingerprint,
						publicKeyB64: untrusted.PublicKeyBase64,
						message:      "首次连接该主机，请核对公钥指纹（TOFU 验证）。",
						isMismatch:   false,
					})
				})
				return
			}
			var mismatch *sshproxy.HostKeyMismatchError
			if errors.As(err, &mismatch) {
				tc.WriteLocal("\x1b[1;31m[SSH-Guard 安全阻断]\x1b[0m 检测到主机公钥指纹变更 (疑似 MITM)！\r\n")
				a.runUI(func() {
					cur := a.findTabByID(tabID)
					if cur == nil || cur.closing {
						return
					}
					cur.status = "error"
					cur.statusText = "安全阻断：检测到主机公钥变更 (疑似 MITM)"
					a.enqueueTofu(&tofuItem{
						tabID:        tabID,
						host:         hostCopy,
						keyType:      mismatch.ActualKeyType,
						fingerprint:  mismatch.ActualFP,
						publicKeyB64: mismatch.ActualPubKeyB64,
						expectedFP:   mismatch.ExpectedFP,
						message:      "警告：目标主机公钥指纹与已信任记录不一致，可能遭遇中间人攻击 (MITM)！连接已阻断。若确认是服务器重装系统导致，可点击下方按钮覆盖信任新指纹。",
						isMismatch:   true,
					})
				})
				return
			}
			tc.WriteLocal(fmt.Sprintf("\r\n\x1b[1;31m[错误]\x1b[0m %s\r\n", err.Error()))
			a.runUI(func() {
				cur := a.findTabByID(tabID)
				if cur == nil || cur.closing {
					return
				}
				cur.status = "error"
				cur.statusText = fmt.Sprintf("连接失败 · %s", hostCopy.Name)
			})
			return
		}

		tc.Attach(live, func() {
			tc.WriteLocal("\r\n\x1b[36m[SSH-Guard]\x1b[0m 会话已断开\r\n")
			a.runUI(func() {
				cur := a.findTabByID(tabID)
				if cur == nil || cur.closing {
					return
				}
				if cur.status != "error" {
					cur.status = "idle"
					cur.statusText = fmt.Sprintf("会话已断开 · %s", hostCopy.Name)
				}
			})
			debug.FreeOSMemory()
		})

		a.runUI(func() {
			cur := a.findTabByID(tabID)
			if cur == nil || cur.closing {
				return
			}
			cur.status = "connected"
			cur.statusText = fmt.Sprintf("已连接 · %s (%s@%s:%d)", hostCopy.Name, hostCopy.Username, hostCopy.Address, hostCopy.Port)
		})
	}()
}

func (a *App) findTabByID(id int) *SessionTab {
	for _, t := range a.tabs {
		if t.id == id {
			return t
		}
	}
	return nil
}

func (a *App) activeSessionTab() *SessionTab {
	if a.activeTab < 0 || a.activeTab >= len(a.tabs) {
		return nil
	}
	return a.tabs[a.activeTab]
}

func (a *App) closeTabByID(id int) {
	idx := -1
	for i, t := range a.tabs {
		if t.id == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	tab := a.tabs[idx]
	tab.closing = true
	if tab.dialCancel != nil {
		tab.dialCancel()
		tab.dialCancel = nil
	}
	a.removeTofuForTab(tab.id)
	if tab.conn != nil {
		_ = tab.conn.Close()
	}
	if tab.term != nil {
		_ = tab.term.Close()
		tab.term = nil
	}
	a.tabs = append(a.tabs[:idx], a.tabs[idx+1:]...)
	if len(a.tabs) == 0 {
		a.activeTab = 0
	} else if a.activeTab >= len(a.tabs) {
		a.activeTab = len(a.tabs) - 1
	} else if idx < a.activeTab {
		a.activeTab--
	}
	debug.FreeOSMemory()
}

func (a *App) closeTabsForHost(hostID int64) {
	var ids []int
	for _, t := range a.tabs {
		if t.host.ID == hostID {
			ids = append(ids, t.id)
		}
	}
	for _, id := range ids {
		a.closeTabByID(id)
	}
}

func (a *App) closeAllTabs() {
	tabs := append([]*SessionTab(nil), a.tabs...)
	a.tabs = nil
	a.activeTab = 0
	for _, tab := range tabs {
		tab.closing = true
		if tab.dialCancel != nil {
			tab.dialCancel()
			tab.dialCancel = nil
		}
		if tab.conn != nil {
			_ = tab.conn.Close()
		}
		if tab.term != nil {
			_ = tab.term.Close()
			tab.term = nil
		}
	}
}
