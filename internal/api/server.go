package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"ssh-guard/internal/auth"
	"ssh-guard/internal/crypto"
	"ssh-guard/internal/sshproxy"
	"ssh-guard/internal/store"
	"ssh-guard/web"

	"github.com/gorilla/websocket"
)

type Server struct {
	store         *store.Store
	vault         *crypto.Vault
	sessions      *auth.Manager
	recordingsDir string
	mux           *http.ServeMux
	upgrader      websocket.Upgrader
}

func NewServer(st *store.Store, v *crypto.Vault, sm *auth.Manager, recordingsDir string) *Server {
	s := &Server{
		store:         st,
		vault:         v,
		sessions:      sm,
		recordingsDir: recordingsDir,
		mux:           http.NewServeMux(),
	}
	s.upgrader = websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				return true
			}
			return strings.HasSuffix(origin, "://"+r.Host)
		},
	}
	s.registerRoutes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Baseline security headers & strict offline CSP
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'self' ws: wss:; img-src 'self' data:")

	s.mux.ServeHTTP(w, r)
}

func (s *Server) registerRoutes() {
	// Vault lifecycle
	s.mux.HandleFunc("/api/vault/status", s.handleVaultStatus)
	s.mux.HandleFunc("/api/vault/setup", s.handleVaultSetup)
	s.mux.HandleFunc("/api/vault/unlock", s.handleVaultUnlock)
	s.mux.HandleFunc("/api/vault/lock", s.requireAuth(s.handleVaultLock))

	// Protected APIs
	s.mux.HandleFunc("/api/hosts", s.requireAuth(s.handleHosts))
	s.mux.HandleFunc("/api/hosts/", s.requireAuth(s.handleHostByID))
	s.mux.HandleFunc("/api/audit-logs", s.requireAuth(s.handleAuditLogs))
	s.mux.HandleFunc("/api/recordings", s.requireAuth(s.handleRecordings))
	s.mux.HandleFunc("/api/recordings/", s.requireAuth(s.handleRecordingCast))

	// WebSocket SSH Terminal
	s.mux.HandleFunc("/ws/ssh/", s.requireAuth(s.handleWSSSH))

	// Embedded Web UI
	staticFS, err := fs.Sub(web.StaticFiles, "static")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(staticFS))
	s.mux.Handle("/", fileServer)
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.vault.IsUnlocked() || !s.sessions.Validate(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "vault locked or session expired"})
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !strings.HasPrefix(r.URL.Path, "/ws/") {
			if r.Header.Get("X-Requested-With") != "SSHGuard" {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "missing CSRF header"})
				return
			}
		}
		next(w, r)
	}
}

// --- Vault Handlers ---

func (s *Server) handleVaultStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	_, err := s.store.GetVaultMeta()
	initialized := !errors.Is(err, store.ErrNotFound)
	unlocked := s.vault.IsUnlocked()
	authenticated := unlocked && s.sessions.Validate(r)

	writeJSON(w, http.StatusOK, map[string]any{
		"initialized":   initialized,
		"unlocked":      unlocked,
		"authenticated": authenticated,
		"mode":          "web",
	})
}

func (s *Server) handleVaultSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if _, err := s.store.GetVaultMeta(); !errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "vault already initialized"})
		return
	}

	var req struct {
		MasterPassword string `json:"master_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}

	meta, err := s.vault.Setup(req.MasterPassword)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := s.store.SaveVaultMeta(meta); err != nil {
		s.vault.Lock()
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save vault meta failed"})
		return
	}

	ip := clientIP(r)
	if _, err := s.sessions.Create(w, ip); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create session failed"})
		return
	}
	s.store.AddAuditLog(0, "Vault", "VAULT_SETUP", "INFO", ip, "初始化主密钥并解锁凭证库")
	writeJSON(w, http.StatusOK, map[string]string{"status": "initialized"})
}

func (s *Server) handleVaultUnlock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	meta, err := s.store.GetVaultMeta()
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "vault not initialized"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read vault meta failed"})
		return
	}

	var req struct {
		MasterPassword string `json:"master_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}

	ip := clientIP(r)
	if err := s.vault.Unlock(req.MasterPassword, meta); err != nil {
		s.store.AddAuditLog(0, "Vault", "VAULT_UNLOCK_FAILED", "WARN", ip, "主密码校验失败")
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "主密码错误"})
		return
	}
	if _, err := s.sessions.Create(w, ip); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create session failed"})
		return
	}
	s.store.AddAuditLog(0, "Vault", "VAULT_UNLOCK", "INFO", ip, "主密钥已解锁至内存")
	writeJSON(w, http.StatusOK, map[string]string{"status": "unlocked"})
}

func (s *Server) handleVaultLock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.vault.Lock()
	s.sessions.RevokeAll(w)
	s.store.AddAuditLog(0, "Vault", "VAULT_LOCK", "INFO", clientIP(r), "手动锁定凭证库，内存主密钥已清零")
	writeJSON(w, http.StatusOK, map[string]string{"status": "locked"})
}

// --- Host Handlers ---

type hostPayload struct {
	Name         string         `json:"name"`
	Address      string         `json:"address"`
	Port         int            `json:"port"`
	Username     string         `json:"username"`
	AuthType     store.AuthType `json:"auth_type"`
	Secret       string         `json:"secret"`
	Passphrase   string         `json:"passphrase"`
	GuardEnabled bool           `json:"guard_enabled"`
	Notes        string         `json:"notes"`
}

func (s *Server) handleHosts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		hosts, err := s.store.ListHosts()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, hosts)

	case http.MethodPost:
		var req hostPayload
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if req.Name == "" || req.Address == "" || req.Username == "" || req.Secret == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "名称、地址、用户名和凭据不能为空"})
			return
		}
		if req.Port <= 0 || req.Port > 65535 {
			req.Port = 22
		}
		if req.AuthType != store.AuthPassword && req.AuthType != store.AuthPrivateKey {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "认证类型仅支持 password 或 private_key"})
			return
		}

		secBytes := []byte(req.Secret)
		secEnv, err := s.vault.EncryptSecret(secBytes)
		crypto.Zeroize(secBytes)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "加密凭据失败"})
			return
		}

		var passEnv *crypto.Envelope
		if req.AuthType == store.AuthPrivateKey && req.Passphrase != "" {
			passBytes := []byte(req.Passphrase)
			passEnv, err = s.vault.EncryptSecret(passBytes)
			crypto.Zeroize(passBytes)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "加密私钥口令失败"})
				return
			}
		} else {
			passEnv = &crypto.Envelope{}
		}

		h := &store.Host{
			Name:          req.Name,
			Address:       req.Address,
			Port:          req.Port,
			Username:      req.Username,
			AuthType:      req.AuthType,
			EncSecretDEK:  secEnv.EncDEK,
			EncSecretData: secEnv.EncSecret,
			EncPassDEK:    passEnv.EncDEK,
			EncPassData:   passEnv.EncSecret,
			GuardEnabled:  req.GuardEnabled,
			Notes:         req.Notes,
		}
		id, err := s.store.CreateHost(h)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		s.store.AddAuditLog(id, h.Name, "HOST_CREATE", "INFO", clientIP(r), fmt.Sprintf("新增主机 %s@%s:%d (%s)", h.Username, h.Address, h.Port, h.AuthType))
		writeJSON(w, http.StatusCreated, map[string]any{"id": id})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleHostByID(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/api/hosts/")
	parts := strings.Split(strings.Trim(trimmed, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid host id"})
		return
	}

	if len(parts) == 2 && parts[1] == "trust-key" {
		s.handleHostTrustKey(w, r, id)
		return
	}

	switch r.Method {
	case http.MethodPut:
		existing, err := s.store.GetHost(id)
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "host not found"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		var req hostPayload
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if req.Port <= 0 || req.Port > 65535 {
			req.Port = 22
		}

		existing.Name = req.Name
		existing.Address = req.Address
		existing.Port = req.Port
		existing.Username = req.Username
		existing.AuthType = req.AuthType
		existing.GuardEnabled = req.GuardEnabled
		existing.Notes = req.Notes

		if req.Secret != "" {
			secBytes := []byte(req.Secret)
			secEnv, err := s.vault.EncryptSecret(secBytes)
			crypto.Zeroize(secBytes)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "加密凭据失败"})
				return
			}
			existing.EncSecretDEK = secEnv.EncDEK
			existing.EncSecretData = secEnv.EncSecret
		}
		if req.AuthType == store.AuthPassword {
			existing.EncPassDEK = ""
			existing.EncPassData = ""
		} else if req.Passphrase != "" {
			passBytes := []byte(req.Passphrase)
			passEnv, err := s.vault.EncryptSecret(passBytes)
			crypto.Zeroize(passBytes)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "加密私钥口令失败"})
				return
			}
			existing.EncPassDEK = passEnv.EncDEK
			existing.EncPassData = passEnv.EncSecret
		}

		if err := s.store.UpdateHost(existing); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		s.store.AddAuditLog(id, existing.Name, "HOST_UPDATE", "INFO", clientIP(r), "更新主机配置")
		writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})

	case http.MethodDelete:
		existing, _ := s.store.GetHost(id)
		if err := s.store.DeleteHost(id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		name := fmt.Sprintf("#%d", id)
		if existing != nil {
			name = existing.Name
		}
		s.store.AddAuditLog(id, name, "HOST_DELETE", "WARN", clientIP(r), "删除主机及关联加密凭据")
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleHostTrustKey(w http.ResponseWriter, r *http.Request, hostID int64) {
	host, err := s.store.GetHost(hostID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "host not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	switch r.Method {
	case http.MethodPost:
		var req struct {
			KeyType         string `json:"key_type"`
			Fingerprint     string `json:"fingerprint"`
			PublicKeyBase64 string `json:"public_key_base64"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Fingerprint == "" || req.PublicKeyBase64 == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid host key payload"})
			return
		}
		if err := s.store.UpsertKnownHost(&store.KnownHost{
			HostID:          hostID,
			KeyType:         req.KeyType,
			Fingerprint:     req.Fingerprint,
			PublicKeyBase64: req.PublicKeyBase64,
		}); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		s.store.AddAuditLog(hostID, host.Name, "TOFU_TRUST", "INFO", clientIP(r), "已信任主机公钥指纹: "+req.Fingerprint)
		writeJSON(w, http.StatusOK, map[string]string{"status": "trusted"})

	case http.MethodDelete:
		if err := s.store.DeleteKnownHost(hostID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		s.store.AddAuditLog(hostID, host.Name, "TOFU_RESET", "WARN", clientIP(r), "已重置主机已知公钥指纹")
		writeJSON(w, http.StatusOK, map[string]string{"status": "reset"})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// --- Audit & Recordings ---

func (s *Server) handleAuditLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	logs, err := s.store.ListAuditLogs(150)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, logs)
}

func (s *Server) handleRecordings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	recs, err := s.store.ListRecordings(100)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, recs)
}

func (s *Server) handleRecordingCast(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/recordings/")
	id = strings.Trim(id, "/")
	if id == "" || strings.Contains(id, "/") || strings.Contains(id, "..") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid recording id"})
		return
	}
	rec, err := s.store.GetRecording(id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "recording not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	data, err := os.ReadFile(filepath.Clean(rec.FilePath))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "recording file missing"})
		return
	}
	w.Header().Set("Content-Type", "application/x-asciicast; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// --- WebSocket SSH Handler ---

func (s *Server) handleWSSSH(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/ws/ssh/")
	hostID, err := strconv.ParseInt(strings.Trim(idStr, "/"), 10, 64)
	if err != nil {
		http.Error(w, "invalid host id", http.StatusBadRequest)
		return
	}
	host, err := s.store.GetHost(hostID)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "host not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	cols, _ := strconv.Atoi(r.URL.Query().Get("cols"))
	rows, _ := strconv.Atoi(r.URL.Query().Get("rows"))

	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()

	sshproxy.ServeSession(ws, sshproxy.BridgeConfig{
		Store:         s.store,
		Vault:         s.vault,
		Host:          host,
		ClientIP:      clientIP(r),
		RecordingsDir: s.recordingsDir,
		InitialCols:   cols,
		InitialRows:   rows,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
