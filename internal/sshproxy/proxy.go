package sshproxy

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"sync"
	"time"

	"ssh-guard/internal/crypto"
	"ssh-guard/internal/store"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"
)

type WSMessage struct {
	Type string `json:"type"` // input, resize, output, error, status, host_key_verify, host_key_mismatch, guard_blocked
	Data string `json:"data,omitempty"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`

	// Host key fields
	KeyType         string `json:"key_type,omitempty"`
	Fingerprint     string `json:"fingerprint,omitempty"`
	PublicKeyBase64 string `json:"public_key_base64,omitempty"`
	ExpectedFP      string `json:"expected_fp,omitempty"`
	RuleName        string `json:"rule_name,omitempty"`
}

// Transport abstracts the bidirectional terminal channel so Web uses WebSocket
// and Desktop uses zero-port in-process Wails IPC.
type Transport interface {
	Send(msg WSMessage) error
	Recv() (*WSMessage, error)
	Close() error
}

// WSTransport adapts a Gorilla WebSocket connection to Transport.
type WSTransport struct {
	ws *websocket.Conn
	mu sync.Mutex
}

func NewWSTransport(ws *websocket.Conn) *WSTransport {
	return &WSTransport{ws: ws}
}

func (t *WSTransport) Send(msg WSMessage) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	_ = t.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return t.ws.WriteJSON(msg)
}

func (t *WSTransport) Recv() (*WSMessage, error) {
	for {
		_, raw, err := t.ws.ReadMessage()
		if err != nil {
			return nil, err
		}
		var msg WSMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}
		return &msg, nil
	}
}

func (t *WSTransport) Close() error {
	return t.ws.Close()
}

type BridgeConfig struct {
	Store         *store.Store
	Vault         *crypto.Vault
	Host          *store.Host
	ClientIP      string
	RecordingsDir string
	InitialCols   int
	InitialRows   int
}

// ServeSession bridges a WebSocket connection to an SSH PTY session (Web Edition).
func ServeSession(ws *websocket.Conn, cfg BridgeConfig) {
	ServeTransport(NewWSTransport(ws), cfg)
}

// ServeTransport bridges any Transport (WebSocket or Wails IPC) to an SSH PTY session.
// Credentials are decrypted just-in-time for the SSH handshake and immediately zeroized.
func ServeTransport(tr Transport, cfg BridgeConfig) {
	defer tr.Close()

	sendMsg := func(msg WSMessage) {
		_ = tr.Send(msg)
	}

	cols := cfg.InitialCols
	rows := cfg.InitialRows
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 32
	}

	// 1. Decrypt credentials in memory and build ssh.AuthMethod
	authMethods, cleanupCreds, err := buildAuthMethods(cfg.Vault, cfg.Host)
	if err != nil {
		sendMsg(WSMessage{Type: "error", Data: "凭据解密失败: " + err.Error()})
		return
	}

	sshConfig := &ssh.ClientConfig{
		User:            cfg.Host.Username,
		Auth:            authMethods,
		HostKeyCallback: NewStrictHostKeyCallback(cfg.Store, cfg.Host.ID),
		Timeout:         12 * time.Second,
	}

	targetAddr := net.JoinHostPort(cfg.Host.Address, fmt.Sprintf("%d", cfg.Host.Port))
	sendMsg(WSMessage{Type: "status", Data: fmt.Sprintf("正在连接 %s@%s ...", cfg.Host.Username, targetAddr)})

	// 2. Dial SSH server (handshake runs here)
	client, err := ssh.Dial("tcp", targetAddr, sshConfig)
	// Immediately wipe decrypted password/key material from memory right after dial completes!
	cleanupCreds()

	if err != nil {
		var untrustedErr *HostKeyUntrustedError
		if errors.As(err, &untrustedErr) {
			sendMsg(WSMessage{
				Type:            "host_key_verify",
				KeyType:         untrustedErr.KeyType,
				Fingerprint:     untrustedErr.Fingerprint,
				PublicKeyBase64: untrustedErr.PublicKeyBase64,
				Data:            "首次连接该主机，请核对公钥指纹（TOFU 验证）",
			})
			return
		}
		var mismatchErr *HostKeyMismatchError
		if errors.As(err, &mismatchErr) {
			cfg.Store.AddAuditLog(
				cfg.Host.ID, cfg.Host.Name, "MITM_ALERT", "CRITICAL", cfg.ClientIP,
				fmt.Sprintf("主机指纹变更！预期: %s，实际: %s", mismatchErr.ExpectedFP, mismatchErr.ActualFP),
			)
			sendMsg(WSMessage{
				Type:            "host_key_mismatch",
				ExpectedFP:      mismatchErr.ExpectedFP,
				Fingerprint:     mismatchErr.ActualFP,
				KeyType:         mismatchErr.ActualKeyType,
				PublicKeyBase64: mismatchErr.ActualPubKeyB64,
				Data:            "警告：目标主机公钥指纹与已信任记录不一致，可能遭遇中间人攻击 (MITM)！连接已阻断。",
			})
			return
		}
		sendMsg(WSMessage{Type: "error", Data: "SSH 握手失败: " + err.Error()})
		return
	}
	defer client.Close()

	// 3. Create SSH session & request PTY
	session, err := client.NewSession()
	if err != nil {
		sendMsg(WSMessage{Type: "error", Data: "创建 SSH 会话失败: " + err.Error()})
		return
	}
	defer session.Close()

	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := session.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		sendMsg(WSMessage{Type: "error", Data: "申请 PTY 失败: " + err.Error()})
		return
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		sendMsg(WSMessage{Type: "error", Data: "绑定 Stdin 失败: " + err.Error()})
		return
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		sendMsg(WSMessage{Type: "error", Data: "绑定 Stdout 失败: " + err.Error()})
		return
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		sendMsg(WSMessage{Type: "error", Data: "绑定 Stderr 失败: " + err.Error()})
		return
	}

	if err := session.Shell(); err != nil {
		sendMsg(WSMessage{Type: "error", Data: "启动远程 Shell 失败: " + err.Error()})
		return
	}

	// 4. Start asciinema (.cast) recording & audit log
	sessionID := randomSessionID()
	recorder, recErr := NewCastRecorder(cfg.RecordingsDir, sessionID, fmt.Sprintf("%s (%s)", cfg.Host.Name, targetAddr), cols, rows)
	startedAt := time.Now()
	if recErr == nil {
		_ = cfg.Store.CreateRecording(&store.Recording{
			ID:        sessionID,
			HostID:    cfg.Host.ID,
			HostName:  cfg.Host.Name,
			ClientIP:  cfg.ClientIP,
			FilePath:  filepath.Clean(recorder.FilePath()),
			StartedAt: startedAt,
		})
	}
	cfg.Store.AddAuditLog(cfg.Host.ID, cfg.Host.Name, "CONNECT", "INFO", cfg.ClientIP, fmt.Sprintf("建立 SSH 会话 (session=%s)", sessionID))
	sendMsg(WSMessage{Type: "status", Data: "CONNECTED"})

	defer func() {
		if recorder != nil {
			size, _ := recorder.Close()
			_ = cfg.Store.FinishRecording(sessionID, time.Now(), size)
		}
		cfg.Store.AddAuditLog(cfg.Host.ID, cfg.Host.Name, "DISCONNECT", "INFO", cfg.ClientIP, fmt.Sprintf("关闭 SSH 会话 (session=%s)", sessionID))
	}()

	// 5. Pump SSH stdout/stderr -> Transport + Recorder
	done := make(chan struct{})
	var closeOnce sync.Once
	signalDone := func() {
		closeOnce.Do(func() { close(done) })
	}

	pumpOutput := func(r io.Reader) {
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				slice := buf[:n]
				if recorder != nil {
					recorder.RecordOutput(slice)
				}
				if sendErr := tr.Send(WSMessage{Type: "output", Data: string(slice)}); sendErr != nil {
					signalDone()
					return
				}
			}
			if err != nil {
				signalDone()
				return
			}
		}
	}

	go pumpOutput(stdout)
	go pumpOutput(stderr)

	// 6. Pump Transport -> High-Risk Command Guard -> SSH stdin
	guard := NewCommandGuard(cfg.Host.GuardEnabled)
	go func() {
		defer signalDone()
		for {
			msg, err := tr.Recv()
			if err != nil || msg == nil {
				return
			}
			switch msg.Type {
			case "resize":
				if msg.Cols > 0 && msg.Rows > 0 {
					_ = session.WindowChange(msg.Rows, msg.Cols)
				}
			case "input":
				if msg.Data == "" {
					continue
				}
				inputBytes := []byte(msg.Data)
				if blocked, ruleName, cmd := guard.Feed(inputBytes); blocked {
					_, _ = stdin.Write([]byte{0x03})
					cfg.Store.AddAuditLog(
						cfg.Host.ID, cfg.Host.Name, "BLOCKED_CMD", "WARN", cfg.ClientIP,
						fmt.Sprintf("已拦截高危命令 [%s]: %s", ruleName, cmd),
					)
					warnBanner := fmt.Sprintf("\r\n\x1b[1;31m[SSH-Guard 已拦截高危命令]\x1b[0m 规则: %s | 命令: %s\r\n", ruleName, cmd)
					if recorder != nil {
						recorder.RecordOutput([]byte(warnBanner))
					}
					sendMsg(WSMessage{Type: "guard_blocked", RuleName: ruleName, Data: warnBanner})
					continue
				}
				if _, err := stdin.Write(inputBytes); err != nil {
					return
				}
			}
		}
	}()

	go func() {
		_ = session.Wait()
		signalDone()
	}()

	<-done
}

func buildAuthMethods(vault *crypto.Vault, host *store.Host) ([]ssh.AuthMethod, func(), error) {
	var sensitiveBuffers [][]byte
	cleanup := func() {
		for _, b := range sensitiveBuffers {
			crypto.Zeroize(b)
		}
	}

	secretBytes, err := vault.DecryptSecret(&crypto.Envelope{
		EncDEK:    host.EncSecretDEK,
		EncSecret: host.EncSecretData,
	})
	if err != nil {
		return nil, cleanup, err
	}
	if len(secretBytes) == 0 {
		return nil, cleanup, errors.New("主机未配置认证凭据")
	}
	sensitiveBuffers = append(sensitiveBuffers, secretBytes)

	switch host.AuthType {
	case store.AuthPassword:
		passCopy := string(secretBytes)
		return []ssh.AuthMethod{
			ssh.Password(passCopy),
			ssh.KeyboardInteractive(func(user, instruction string, questions []string, echos []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range questions {
					answers[i] = passCopy
				}
				return answers, nil
			}),
		}, cleanup, nil

	case store.AuthPrivateKey:
		var signer ssh.Signer
		if host.EncPassData != "" {
			passBytes, err := vault.DecryptSecret(&crypto.Envelope{
				EncDEK:    host.EncPassDEK,
				EncSecret: host.EncPassData,
			})
			if err != nil {
				cleanup()
				return nil, func() {}, fmt.Errorf("解密私钥口令失败: %w", err)
			}
			sensitiveBuffers = append(sensitiveBuffers, passBytes)
			signer, err = ssh.ParsePrivateKeyWithPassphrase(secretBytes, passBytes)
			if err != nil {
				cleanup()
				return nil, func() {}, fmt.Errorf("解析加密私钥失败（请检查口令是否正确）: %w", err)
			}
		} else {
			signer, err = ssh.ParsePrivateKey(secretBytes)
			if err != nil {
				cleanup()
				return nil, func() {}, fmt.Errorf("解析私钥失败（若私钥带口令请填写口令）: %w", err)
			}
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, cleanup, nil

	default:
		cleanup()
		return nil, func() {}, fmt.Errorf("不支持的认证类型: %s", host.AuthType)
	}
}

func randomSessionID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%s", time.Now().Format("20060102-150405"), hex.EncodeToString(b))
}
