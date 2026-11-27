package sshproxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"sync"
	"time"

	"ssh-guard/internal/crypto"
	"ssh-guard/internal/store"

	"golang.org/x/crypto/ssh"
)

const (
	defaultCols           = 120
	defaultRows           = 32
	sshDialTimeout        = 12 * time.Second
	streamMaxBuffered     = 64 * 1024
	streamShrinkThreshold = 16 * 1024
)

// memStream is a bounded, concurrency-safe byte stream that allows multiple
// writers (stdout, stderr, inline guard warnings) and preserves unread tail
// bytes before returning io.EOF after Close.
type memStream struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buf    bytes.Buffer
	closed bool
}

func newMemStream() *memStream {
	s := &memStream{}
	s.cond = sync.NewCond(&s.mu)
	return s
}

func (s *memStream) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.buf.Len() >= streamMaxBuffered && !s.closed {
		s.cond.Wait()
	}
	if s.closed {
		return 0, io.ErrClosedPipe
	}
	n, err := s.buf.Write(p)
	s.cond.Broadcast()
	return n, err
}

func (s *memStream) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.buf.Len() == 0 && !s.closed {
		s.cond.Wait()
	}
	if s.buf.Len() == 0 && s.closed {
		return 0, io.EOF
	}
	n, err := s.buf.Read(p)
	if s.buf.Len() == 0 && s.buf.Cap() > streamShrinkThreshold {
		s.buf = bytes.Buffer{}
	}
	s.cond.Broadcast()
	return n, err
}

func (s *memStream) Close() error {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		if s.buf.Len() == 0 && s.buf.Cap() > streamShrinkThreshold {
			s.buf = bytes.Buffer{}
		}
		s.cond.Broadcast()
	}
	s.mu.Unlock()
	return nil
}

// LiveSession represents an established, transport-agnostic SSH PTY session.
// It implements io.ReadWriteCloser and Resize(cols, rows int) error so both
// the native terminal plugin and WebSocket adapter can consume it directly.
type LiveSession struct {
	sessionID string
	host      *store.Host
	clientIP  string
	store     *store.Store

	client  *ssh.Client
	session *ssh.Session
	stdin   io.WriteCloser

	writeMu      sync.Mutex
	guard        *CommandGuard
	onGuardBlock func(ruleName, cmd string)

	recorder *CastRecorder
	out      *memStream

	done      chan struct{}
	closeOnce sync.Once
}

// DialSession decrypts host credentials just-in-time for the SSH handshake,
// immediately zeroizes plaintext buffers after dial, enforces strict TOFU/MITM
// host key verification, opens the PTY shell, and starts recording and auditing.
func DialSession(ctx context.Context, cfg BridgeConfig) (*LiveSession, error) {
	if cfg.Host == nil {
		return nil, errors.New("host configuration is nil")
	}
	if cfg.Vault == nil || !cfg.Vault.IsUnlocked() {
		return nil, crypto.ErrVaultLocked
	}
	if cfg.Store == nil {
		return nil, errors.New("store is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	cols := cfg.InitialCols
	rows := cfg.InitialRows
	if cols <= 0 {
		cols = defaultCols
	}
	if rows <= 0 {
		rows = defaultRows
	}

	// 1. Decrypt credentials in memory and build ssh.AuthMethod.
	authMethods, cleanupCreds, err := buildAuthMethods(cfg.Vault, cfg.Host)
	if err != nil {
		return nil, fmt.Errorf("凭据解密失败: %w", err)
	}
	// Guarantee zeroization on every exit path as well as right after handshake.
	var cleanupOnce sync.Once
	wipeCreds := func() {
		cleanupOnce.Do(cleanupCreds)
	}
	defer wipeCreds()

	sshConfig := &ssh.ClientConfig{
		User:            cfg.Host.Username,
		Auth:            authMethods,
		HostKeyCallback: NewStrictHostKeyCallback(cfg.Store, cfg.Host.ID),
		Timeout:         sshDialTimeout,
	}

	targetAddr := net.JoinHostPort(cfg.Host.Address, fmt.Sprintf("%d", cfg.Host.Port))
	if cfg.OnStatus != nil {
		cfg.OnStatus(fmt.Sprintf("正在连接 %s@%s ...", cfg.Host.Username, targetAddr))
	}

	// 2. Dial transport with context cancellation, then perform SSH handshake.
	client, err := dialSSHContext(ctx, targetAddr, sshConfig, cfg.DialConn)
	// Immediately wipe decrypted password/key material right after handshake finishes.
	wipeCreds()

	if err != nil {
		var untrustedErr *HostKeyUntrustedError
		if errors.As(err, &untrustedErr) {
			return nil, untrustedErr
		}
		var mismatchErr *HostKeyMismatchError
		if errors.As(err, &mismatchErr) {
			cfg.Store.AddAuditLog(
				cfg.Host.ID, cfg.Host.Name, "MITM_ALERT", "CRITICAL", cfg.ClientIP,
				fmt.Sprintf("主机指纹变更！预期: %s，实际: %s", mismatchErr.ExpectedFP, mismatchErr.ActualFP),
			)
			return nil, mismatchErr
		}
		return nil, fmt.Errorf("SSH 握手失败: %w", err)
	}

	// 3. Create SSH session & request PTY.
	session, err := client.NewSession()
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("创建 SSH 会话失败: %w", err)
	}

	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := session.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, fmt.Errorf("申请 PTY 失败: %w", err)
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, fmt.Errorf("绑定 Stdin 失败: %w", err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, fmt.Errorf("绑定 Stdout 失败: %w", err)
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, fmt.Errorf("绑定 Stderr 失败: %w", err)
	}

	if err := session.Shell(); err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, fmt.Errorf("启动远程 Shell 失败: %w", err)
	}

	// 4. Start asciinema (.cast) recording & audit log.
	sessionID := randomSessionID()
	var recorder *CastRecorder
	if cfg.RecordingsDir != "" {
		rec, recErr := NewCastRecorder(cfg.RecordingsDir, sessionID, fmt.Sprintf("%s (%s)", cfg.Host.Name, targetAddr), cols, rows)
		if recErr == nil {
			recorder = rec
			_ = cfg.Store.CreateRecording(&store.Recording{
				ID:        sessionID,
				HostID:    cfg.Host.ID,
				HostName:  cfg.Host.Name,
				ClientIP:  cfg.ClientIP,
				FilePath:  filepath.Clean(recorder.FilePath()),
				StartedAt: time.Now(),
			})
		}
	}
	cfg.Store.AddAuditLog(cfg.Host.ID, cfg.Host.Name, "CONNECT", "INFO", cfg.ClientIP, fmt.Sprintf("建立 SSH 会话 (session=%s)", sessionID))

	ls := &LiveSession{
		sessionID:    sessionID,
		host:         cfg.Host,
		clientIP:     cfg.ClientIP,
		store:        cfg.Store,
		client:       client,
		session:      session,
		stdin:        stdin,
		guard:        NewCommandGuard(cfg.Host.GuardEnabled),
		onGuardBlock: cfg.OnGuardBlock,
		recorder:     recorder,
		out:          newMemStream(),
		done:         make(chan struct{}),
	}

	// 5. Pump both stdout and stderr into the unified output stream + recorder.
	var pumpWG sync.WaitGroup
	pumpOutput := func(r io.Reader) {
		defer pumpWG.Done()
		buf := make([]byte, 4096)
		for {
			n, rerr := r.Read(buf)
			if n > 0 {
				slice := buf[:n]
				if ls.recorder != nil {
					ls.recorder.RecordOutput(slice)
				}
				if _, werr := ls.out.Write(slice); werr != nil {
					return
				}
			}
			if rerr != nil {
				return
			}
		}
	}
	pumpWG.Add(2)
	go pumpOutput(stdout)
	go pumpOutput(stderr)

	// Wait until remote shell exits and output pipes drain, then close cleanly.
	go func() {
		pumpWG.Wait()
		_ = session.Wait()
		_ = ls.Close()
	}()

	return ls, nil
}

func dialSSHContext(ctx context.Context, addr string, cfg *ssh.ClientConfig, dialFn func(context.Context, string) (net.Conn, error)) (*ssh.Client, error) {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = sshDialTimeout
	}
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var (
		rawConn net.Conn
		err     error
	)
	if dialFn != nil {
		rawConn, err = dialFn(dialCtx, addr)
	} else {
		var d net.Dialer
		rawConn, err = d.DialContext(dialCtx, "tcp", addr)
	}
	if err != nil {
		return nil, err
	}
	if deadline, ok := dialCtx.Deadline(); ok {
		_ = rawConn.SetDeadline(deadline)
	}

	stopCancel := context.AfterFunc(dialCtx, func() {
		_ = rawConn.Close()
	})
	cc, chans, reqs, err := ssh.NewClientConn(rawConn, addr, cfg)
	stopCancel()
	if err != nil {
		_ = rawConn.Close()
		return nil, err
	}
	// Clear handshake deadline once SSH connection is established.
	_ = rawConn.SetDeadline(time.Time{})
	return ssh.NewClient(cc, chans, reqs), nil
}

// SessionID returns the unique recording/audit identifier for this live session.
func (s *LiveSession) SessionID() string {
	return s.sessionID
}

// Done is closed once the session has finished closing and cleaning up.
func (s *LiveSession) Done() <-chan struct{} {
	return s.done
}

// Read reads merged stdout, stderr, and inline guard warnings from the session.
func (s *LiveSession) Read(p []byte) (int, error) {
	return s.out.Read(p)
}

// Write inspects interactive input through CommandGuard before forwarding to remote stdin.
func (s *LiveSession) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	select {
	case <-s.done:
		return 0, io.ErrClosedPipe
	default:
	}

	if blocked, ruleName, cmd := s.guard.Feed(p); blocked {
		_, _ = s.stdin.Write([]byte{0x03})
		if s.store != nil && s.host != nil {
			s.store.AddAuditLog(
				s.host.ID, s.host.Name, "BLOCKED_CMD", "WARN", s.clientIP,
				fmt.Sprintf("已拦截高危命令 [%s]: %s", ruleName, cmd),
			)
		}
		warnBanner := fmt.Sprintf("\r\n\x1b[1;31m[SSH-Guard 已拦截高危命令]\x1b[0m 规则: %s | 命令: %s\r\n", ruleName, cmd)
		if s.recorder != nil {
			s.recorder.RecordOutput([]byte(warnBanner))
		}
		_, _ = s.out.Write([]byte(warnBanner))
		if s.onGuardBlock != nil {
			s.onGuardBlock(ruleName, cmd)
		}
		return len(p), nil
	}
	return s.stdin.Write(p)
}

// Resize forwards PTY dimension updates to the remote SSH session.
func (s *LiveSession) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	select {
	case <-s.done:
		return io.ErrClosedPipe
	default:
	}
	return s.session.WindowChange(rows, cols)
}

// Close releases all network, PTY, recording, and audit resources exactly once.
func (s *LiveSession) Close() error {
	s.closeOnce.Do(func() {
		if s.stdin != nil {
			_ = s.stdin.Close()
		}
		if s.session != nil {
			_ = s.session.Close()
		}
		if s.client != nil {
			_ = s.client.Close()
		}
		_ = s.out.Close()

		if s.recorder != nil {
			size, _ := s.recorder.Close()
			if s.store != nil {
				_ = s.store.FinishRecording(s.sessionID, time.Now(), size)
			}
		}
		if s.store != nil && s.host != nil {
			s.store.AddAuditLog(
				s.host.ID, s.host.Name, "DISCONNECT", "INFO", s.clientIP,
				fmt.Sprintf("关闭 SSH 会话 (session=%s)", s.sessionID),
			)
		}
		close(s.done)
	})
	return nil
}

func randomSessionID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%s", time.Now().Format("20060102-150405"), hex.EncodeToString(b))
}
