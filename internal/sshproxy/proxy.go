package sshproxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
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

// Transport abstracts the bidirectional terminal channel for WebSocket and legacy IPC adapters.
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

	// Optional callbacks consumed by native or transport adapters.
	OnStatus     func(msg string)
	OnGuardBlock func(ruleName, cmd string)

	// Optional custom dialer (used by tests to run full SSH over net.Pipe without TCP sockets).
	DialConn func(ctx context.Context, addr string) (net.Conn, error)
}

// ServeSession bridges a WebSocket connection to an SSH PTY session (Web Edition).
func ServeSession(ws *websocket.Conn, cfg BridgeConfig) {
	ServeTransport(NewWSTransport(ws), cfg)
}

// ServeTransport bridges any Transport to the shared DialSession/LiveSession core.
func ServeTransport(tr Transport, cfg BridgeConfig) {
	defer tr.Close()

	sendMsg := func(msg WSMessage) {
		_ = tr.Send(msg)
	}

	cfg.OnStatus = func(status string) {
		sendMsg(WSMessage{Type: "status", Data: status})
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	live, err := DialSession(ctx, cfg)
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
		sendMsg(WSMessage{Type: "error", Data: err.Error()})
		return
	}
	defer live.Close()

	sendMsg(WSMessage{Type: "status", Data: "CONNECTED"})

	done := make(chan struct{})
	var closeOnce sync.Once
	signalDone := func() {
		closeOnce.Do(func() {
			close(done)
			_ = live.Close()
		})
	}

	// Pump merged LiveSession output -> Transport.
	go func() {
		defer signalDone()
		buf := make([]byte, 4096)
		for {
			n, rerr := live.Read(buf)
			if n > 0 {
				if sendErr := tr.Send(WSMessage{Type: "output", Data: string(buf[:n])}); sendErr != nil {
					return
				}
			}
			if rerr != nil {
				return
			}
		}
	}()

	// Pump Transport input/resize -> LiveSession.
	go func() {
		defer signalDone()
		for {
			msg, rerr := tr.Recv()
			if rerr != nil || msg == nil {
				return
			}
			switch msg.Type {
			case "resize":
				if msg.Cols > 0 && msg.Rows > 0 {
					_ = live.Resize(msg.Cols, msg.Rows)
				}
			case "input":
				if msg.Data == "" {
					continue
				}
				if _, werr := live.Write([]byte(msg.Data)); werr != nil {
					return
				}
			}
		}
	}()

	select {
	case <-done:
	case <-live.Done():
	}
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
