package sshproxy

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ssh-guard/internal/crypto"
	"ssh-guard/internal/store"

	"golang.org/x/crypto/ssh"
)

type mockSSHServer struct {
	hostPubKey ssh.PublicKey
	cfg        *ssh.ServerConfig

	mu           sync.Mutex
	receivedIn   []byte
	resizeEvents [][2]int
	emitStderr   string
}

func newMockSSHServer(t *testing.T, password, stderrMsg string) *mockSSHServer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}

	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if string(pass) == password {
				return &ssh.Permissions{}, nil
			}
			return nil, errors.New("bad password")
		},
	}
	cfg.AddHostKey(signer)

	return &mockSSHServer{
		hostPubKey: signer.PublicKey(),
		cfg:        cfg,
		emitStderr: stderrMsg,
	}
}

type pipeAddr string

func (a pipeAddr) Network() string { return "pipe" }
func (a pipeAddr) String() string  { return string(a) }

type bufferedNetConn struct {
	r *memStream
	w *memStream
}

func newBufferedConnPair() (net.Conn, net.Conn) {
	aToB := newMemStream()
	bToA := newMemStream()
	return &bufferedNetConn{r: bToA, w: aToB}, &bufferedNetConn{r: aToB, w: bToA}
}

func (c *bufferedNetConn) Read(p []byte) (int, error)         { return c.r.Read(p) }
func (c *bufferedNetConn) Write(p []byte) (int, error)        { return c.w.Write(p) }
func (c *bufferedNetConn) Close() error                       { _ = c.w.Close(); return c.r.Close() }
func (c *bufferedNetConn) LocalAddr() net.Addr                { return pipeAddr("local") }
func (c *bufferedNetConn) RemoteAddr() net.Addr               { return pipeAddr("remote") }
func (c *bufferedNetConn) SetDeadline(t time.Time) error      { return nil }
func (c *bufferedNetConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *bufferedNetConn) SetWriteDeadline(t time.Time) error { return nil }

func (s *mockSSHServer) dial(_ context.Context, _ string) (net.Conn, error) {
	cConn, sConn := newBufferedConnPair()
	go s.handleConn(sConn)
	return cConn, nil
}

func (s *mockSSHServer) handleConn(nConn net.Conn) {
	defer nConn.Close()
	_, chans, reqs, err := ssh.NewServerConn(nConn, s.cfg)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)

	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			_ = newChan.Reject(ssh.UnknownChannelType, "unknown")
			continue
		}
		ch, requests, err := newChan.Accept()
		if err != nil {
			continue
		}
		go func(ch ssh.Channel, in <-chan *ssh.Request) {
			defer ch.Close()
			for req := range in {
				switch req.Type {
				case "pty-req":
					_ = req.Reply(true, nil)
				case "window-change":
					if len(req.Payload) >= 8 {
						cols := int(binary.BigEndian.Uint32(req.Payload[0:4]))
						rows := int(binary.BigEndian.Uint32(req.Payload[4:8]))
						s.mu.Lock()
						s.resizeEvents = append(s.resizeEvents, [2]int{cols, rows})
						s.mu.Unlock()
					}
					_ = req.Reply(true, nil)
				case "shell":
					_ = req.Reply(true, nil)
					_, _ = ch.Write([]byte("STDOUT-HELLO\r\n"))
					if s.emitStderr != "" {
						_, _ = ch.Stderr().Write([]byte(s.emitStderr))
					}
					go func() {
						buf := make([]byte, 1024)
						for {
							n, rerr := ch.Read(buf)
							if n > 0 {
								s.mu.Lock()
								s.receivedIn = append(s.receivedIn, buf[:n]...)
								s.mu.Unlock()
								_, _ = ch.Write([]byte("ECHO:" + string(buf[:n])))
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
		}(ch, requests)
	}
}

func newTestStoreVaultHost(t *testing.T, password string, guardEnabled bool) (*store.Store, *crypto.Vault, *store.Host, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	v := crypto.NewVault()
	meta, err := v.Setup("TestMasterPass!2026")
	if err != nil {
		t.Fatalf("setup vault: %v", err)
	}
	if err := st.SaveVaultMeta(meta); err != nil {
		t.Fatalf("save meta: %v", err)
	}

	env, err := v.EncryptSecret([]byte(password))
	if err != nil {
		t.Fatalf("encrypt secret: %v", err)
	}

	h := &store.Host{
		Name:          "test-box",
		Address:       "127.0.0.1",
		Port:          2222,
		Username:      "tester",
		AuthType:      store.AuthPassword,
		EncSecretDEK:  env.EncDEK,
		EncSecretData: env.EncSecret,
		GuardEnabled:  guardEnabled,
	}
	id, err := st.CreateHost(h)
	if err != nil {
		t.Fatalf("create host: %v", err)
	}
	h.ID = id
	return st, v, h, filepath.Join(dir, "recordings")
}

func TestDialSessionLockedVaultTOFUAndMITM(t *testing.T) {
	srv := newMockSSHServer(t, "s3cr3t", "")
	st, v, h, recDir := newTestStoreVaultHost(t, "s3cr3t", true)

	// 1. Locked vault must reject before dialing.
	v.Lock()
	if _, err := DialSession(context.Background(), BridgeConfig{
		Store: st, Vault: v, Host: h, ClientIP: "127.0.0.1", RecordingsDir: recDir, DialConn: srv.dial,
	}); !errors.Is(err, crypto.ErrVaultLocked) {
		t.Fatalf("expected ErrVaultLocked, got %v", err)
	}

	meta, _ := st.GetVaultMeta()
	if err := v.Unlock("TestMasterPass!2026", meta); err != nil {
		t.Fatalf("unlock: %v", err)
	}

	// 2. First dial without known_hosts must return HostKeyUntrustedError (TOFU).
	_, err := DialSession(context.Background(), BridgeConfig{
		Store: st, Vault: v, Host: h, ClientIP: "127.0.0.1", RecordingsDir: recDir, DialConn: srv.dial,
	})
	var untrusted *HostKeyUntrustedError
	if !errors.As(err, &untrusted) {
		t.Fatalf("expected HostKeyUntrustedError, got %v", err)
	}
	if untrusted.Fingerprint != ssh.FingerprintSHA256(srv.hostPubKey) {
		t.Fatalf("unexpected fingerprint: %s", untrusted.Fingerprint)
	}

	// 3. Wrong stored fingerprint must trigger MITM error and CRITICAL audit log.
	if err := st.UpsertKnownHost(&store.KnownHost{
		HostID:          h.ID,
		KeyType:         "ssh-ed25519",
		Fingerprint:     "SHA256:WRONG",
		PublicKeyBase64: "AAAA",
	}); err != nil {
		t.Fatalf("upsert wrong known host: %v", err)
	}
	_, err = DialSession(context.Background(), BridgeConfig{
		Store: st, Vault: v, Host: h, ClientIP: "10.0.0.5", RecordingsDir: recDir, DialConn: srv.dial,
	})
	var mismatch *HostKeyMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("expected HostKeyMismatchError, got %v", err)
	}
	logs, err := st.ListAuditLogs(20)
	if err != nil {
		t.Fatalf("list audit logs: %v", err)
	}
	foundMITM := false
	for _, l := range logs {
		if l.EventType == "MITM_ALERT" && l.Severity == "CRITICAL" {
			foundMITM = true
			break
		}
	}
	if !foundMITM {
		t.Fatalf("expected CRITICAL MITM_ALERT audit log, got %+v", logs)
	}
}

func TestLiveSessionMergedOutputGuardResizeAndIdempotentClose(t *testing.T) {
	srv := newMockSSHServer(t, "s3cr3t", "STDERR-WARN\r\n")
	st, v, h, recDir := newTestStoreVaultHost(t, "s3cr3t", true)

	if err := st.UpsertKnownHost(&store.KnownHost{
		HostID:          h.ID,
		KeyType:         srv.hostPubKey.Type(),
		Fingerprint:     ssh.FingerprintSHA256(srv.hostPubKey),
		PublicKeyBase64: base64.StdEncoding.EncodeToString(srv.hostPubKey.Marshal()),
	}); err != nil {
		t.Fatalf("trust host key: %v", err)
	}

	var (
		blockMu   sync.Mutex
		blockRule string
		blockCmd  string
	)
	ls, err := DialSession(context.Background(), BridgeConfig{
		Store:         st,
		Vault:         v,
		Host:          h,
		ClientIP:      "desktop-local",
		RecordingsDir: recDir,
		InitialCols:   100,
		InitialRows:   30,
		DialConn:      srv.dial,
		OnGuardBlock: func(ruleName, cmd string) {
			blockMu.Lock()
			blockRule = ruleName
			blockCmd = cmd
			blockMu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("DialSession failed: %v", err)
	}

	// Read both stdout and stderr from unified stream.
	var (
		outMu sync.Mutex
		got   strings.Builder
	)
	go func() {
		buf := make([]byte, 2048)
		for {
			n, rerr := ls.Read(buf)
			if n > 0 {
				outMu.Lock()
				got.Write(buf[:n])
				outMu.Unlock()
			}
			if rerr != nil {
				return
			}
		}
	}()

	waitForOutput := func(substr string) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			outMu.Lock()
			ok := strings.Contains(got.String(), substr)
			outMu.Unlock()
			if ok {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		outMu.Lock()
		cur := got.String()
		outMu.Unlock()
		t.Fatalf("timed out waiting for %q; got %q", substr, cur)
	}

	waitForOutput("STDOUT-HELLO")
	waitForOutput("STDERR-WARN")

	// Resize forwards window-change to SSH server.
	if err := ls.Resize(140, 42); err != nil {
		t.Fatalf("Resize failed: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		srv.mu.Lock()
		n := len(srv.resizeEvents)
		srv.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	srv.mu.Lock()
	resizes := append([][2]int(nil), srv.resizeEvents...)
	srv.mu.Unlock()
	if len(resizes) == 0 || resizes[len(resizes)-1] != [2]int{140, 42} {
		t.Fatalf("expected resize [140 42], got %+v", resizes)
	}

	// High-risk command triggers block callback, inline red warning, 0x03 to stdin, and BLOCKED_CMD audit.
	if _, err := ls.Write([]byte("rm -rf /\r")); err != nil {
		t.Fatalf("Write blocked cmd failed: %v", err)
	}
	waitForOutput("SSH-Guard 已拦截高危命令")

	blockMu.Lock()
	if blockRule == "" || blockCmd != "rm -rf /" {
		t.Fatalf("expected OnGuardBlock callback, got rule=%q cmd=%q", blockRule, blockCmd)
	}
	blockMu.Unlock()

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		srv.mu.Lock()
		hasCtrlC := strings.Contains(string(srv.receivedIn), "\x03")
		srv.mu.Unlock()
		if hasCtrlC {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	srv.mu.Lock()
	rawIn := string(srv.receivedIn)
	srv.mu.Unlock()
	if !strings.Contains(rawIn, "\x03") || strings.Contains(rawIn, "rm -rf /\r") {
		t.Fatalf("expected 0x03 without executing rm -rf /, server saw %q", rawIn)
	}

	// Concurrent Close must be idempotent and write exactly one DISCONNECT log + finish recording.
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = ls.Close()
		}()
	}
	wg.Wait()
	<-ls.Done()

	logs, err := st.ListAuditLogs(50)
	if err != nil {
		t.Fatalf("list audit logs: %v", err)
	}
	disconnects, blockedCount := 0, 0
	for _, l := range logs {
		if l.EventType == "DISCONNECT" {
			disconnects++
		}
		if l.EventType == "BLOCKED_CMD" && l.Severity == "WARN" {
			blockedCount++
		}
	}
	if disconnects != 1 {
		t.Fatalf("expected exactly 1 DISCONNECT audit log, got %d", disconnects)
	}
	if blockedCount != 1 {
		t.Fatalf("expected 1 BLOCKED_CMD audit log, got %d", blockedCount)
	}

	rec, err := st.GetRecording(ls.SessionID())
	if err != nil {
		t.Fatalf("get recording: %v", err)
	}
	if rec.SizeBytes <= 0 {
		t.Fatalf("expected finalized recording size > 0, got %d", rec.SizeBytes)
	}

	// Verify streaming CastReader reads the generated recording cleanly.
	cr, err := OpenCastReader(rec.FilePath)
	if err != nil {
		t.Fatalf("OpenCastReader: %v", err)
	}
	defer cr.Close()
	if cr.Header().Width != 100 || cr.Header().Height != 30 {
		t.Fatalf("unexpected header dimensions: %+v", cr.Header())
	}
	var replayed strings.Builder
	for {
		ev, err := cr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("CastReader.Next: %v", err)
		}
		replayed.WriteString(ev.Data)
	}
	if !strings.Contains(replayed.String(), "STDOUT-HELLO") || !strings.Contains(replayed.String(), "SSH-Guard 已拦截高危命令") {
		t.Fatalf("recording missing expected output/warning: %q", replayed.String())
	}
}

func TestCastReaderHandlesLargeLinesAndNonOutputEvents(t *testing.T) {
	bigChunk := strings.Repeat("A", 128*1024)
	payload := `{"version":2,"width":132,"height":40,"timestamp":1700000000}` + "\n" +
		"\n" +
		`[0.1,"i","ignored-input"]` + "\n" +
		`[0.25,"o","hello"]` + "\n" +
		`[1.5,"o","` + bigChunk + `"]` + "\n"

	cr, err := NewCastReader(strings.NewReader(payload))
	if err != nil {
		t.Fatalf("NewCastReader failed: %v", err)
	}
	if cr.Header().Width != 132 || cr.Header().Height != 40 {
		t.Fatalf("unexpected header: %+v", cr.Header())
	}
	ev1, err := cr.Next()
	if err != nil || ev1.Data != "hello" || ev1.Time != 0.25 {
		t.Fatalf("unexpected ev1: %+v err=%v", ev1, err)
	}
	ev2, err := cr.Next()
	if err != nil || len(ev2.Data) != len(bigChunk) || ev2.Time != 1.5 {
		t.Fatalf("unexpected ev2 len=%d time=%v err=%v", len(ev2.Data), ev2.Time, err)
	}
	if _, err := cr.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF, got %v", err)
	}
}

func TestMemStreamBoundedAndShrinksAfterBurstDrain(t *testing.T) {
	s := newMemStream()
	defer s.Close()

	// Write a burst exceeding streamShrinkThreshold (16 KB) and filling past streamMaxBuffered (64 KB).
	chunk := bytes.Repeat([]byte("0123456789abcdef"), 2048) // 32 KB
	if _, err := s.Write(chunk); err != nil {
		t.Fatalf("write #1: %v", err)
	}
	if _, err := s.Write(chunk); err != nil {
		t.Fatalf("write #2: %v", err)
	}

	// Third write must block on backpressure until reader drains.
	thirdDone := make(chan error, 1)
	go func() {
		_, err := s.Write([]byte("TAIL"))
		thirdDone <- err
	}()

	select {
	case err := <-thirdDone:
		t.Fatalf("expected backpressure wait at %d bytes, got immediate return err=%v", streamMaxBuffered, err)
	case <-time.After(40 * time.Millisecond):
	}

	s.mu.Lock()
	peakCap := s.buf.Cap()
	s.mu.Unlock()
	if peakCap <= streamShrinkThreshold {
		t.Fatalf("expected peak capacity > %d, got %d", streamShrinkThreshold, peakCap)
	}

	// Drain all bytes (64 KB + "TAIL").
	var out bytes.Buffer
	readBuf := make([]byte, 4096)
	wantLen := len(chunk)*2 + len("TAIL")
	for out.Len() < wantLen {
		n, err := s.Read(readBuf)
		if err != nil {
			t.Fatalf("read failed at %d bytes: %v", out.Len(), err)
		}
		out.Write(readBuf[:n])
	}
	if err := <-thirdDone; err != nil {
		t.Fatalf("blocked write failed: %v", err)
	}
	if !bytes.HasSuffix(out.Bytes(), []byte("TAIL")) || out.Len() != wantLen {
		t.Fatalf("drained payload mismatch: len=%d", out.Len())
	}

	s.mu.Lock()
	afterLen, afterCap := s.buf.Len(), s.buf.Cap()
	s.mu.Unlock()
	if afterLen != 0 || afterCap > streamShrinkThreshold {
		t.Fatalf("expected drained memStream to shrink (len=0, cap<=%d), got len=%d cap=%d", streamShrinkThreshold, afterLen, afterCap)
	}
}

func TestCastRecorderBufferReuseCompatibilityAndShrink(t *testing.T) {
	dir := t.TempDir()
	rec, err := NewCastRecorder(dir, "compat-shrink", "test", 100, 30)
	if err != nil {
		t.Fatalf("NewCastRecorder: %v", err)
	}

	samples := [][]byte{
		[]byte("plain ascii\r\n"),
		[]byte("\x1b[1;31mANSI\x1b[0m \"quote\" \\slash\\ \t\x00\x07\x08\x0c <>&"),
		[]byte("中文输出 🚀 line-sep:\u2028 para-sep:\u2029"),
		{0x41, 0xff, 0xfe, 0x42}, // invalid UTF-8 bytes must decode to U+FFFD like encoding/json
	}
	for _, sample := range samples {
		rec.RecordOutput(sample)
	}

	// Burst exceeding recorderShrinkThreshold (32 KB) must shrink lineBuf immediately after writing.
	large := bytes.Repeat([]byte("X"), recorderShrinkThreshold+8192)
	rec.RecordOutput(large)

	rec.mu.Lock()
	capAfterBurst := rec.lineBuf.Cap()
	rec.mu.Unlock()
	if capAfterBurst > recorderShrinkThreshold {
		t.Fatalf("expected recorder lineBuf to shrink after large burst (<= %d), got %d", recorderShrinkThreshold, capAfterBurst)
	}

	size, err := rec.Close()
	if err != nil || size <= 0 {
		t.Fatalf("Close failed: size=%d err=%v", size, err)
	}

	// Verify exact round-trip against encoding/json string semantics.
	cr, err := OpenCastReader(rec.FilePath())
	if err != nil {
		t.Fatalf("OpenCastReader: %v", err)
	}
	defer cr.Close()

	all := append(samples, large)
	for i, raw := range all {
		ev, err := cr.Next()
		if err != nil {
			t.Fatalf("event #%d Next failed: %v", i, err)
		}
		// Compare against reference encoding/json string round-trip.
		refJSON, err := json.Marshal(string(raw))
		if err != nil {
			t.Fatalf("reference marshal #%d: %v", i, err)
		}
		var want string
		if err := json.Unmarshal(refJSON, &want); err != nil {
			t.Fatalf("reference unmarshal #%d: %v", i, err)
		}
		if ev.Data != want {
			t.Fatalf("event #%d mismatch: got %q, want %q", i, ev.Data, want)
		}
	}
	if _, err := cr.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF, got %v", err)
	}
}
