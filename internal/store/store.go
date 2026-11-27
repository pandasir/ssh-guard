package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"ssh-guard/internal/crypto"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("record not found")

type AuthType string

const (
	AuthPassword   AuthType = "password"
	AuthPrivateKey AuthType = "private_key"
)

// Host represents a managed SSH target server.
// Secrets are stored strictly as envelope-encrypted strings.
type Host struct {
	ID              int64     `json:"id"`
	Name            string    `json:"name"`
	Address         string    `json:"address"`
	Port            int       `json:"port"`
	Username        string    `json:"username"`
	AuthType        AuthType  `json:"auth_type"`
	GuardEnabled    bool      `json:"guard_enabled"`
	Notes           string    `json:"notes"`
	KnownHostFP     string    `json:"known_host_fp,omitempty"`
	HasSecret       bool      `json:"has_secret"`
	HasPassphrase   bool      `json:"has_passphrase"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`

	// Encrypted secret fields (never serialized to JSON responses)
	EncSecretDEK    string `json:"-"`
	EncSecretData   string `json:"-"`
	EncPassDEK      string `json:"-"`
	EncPassData     string `json:"-"`
}

type KnownHost struct {
	HostID          int64     `json:"host_id"`
	KeyType         string    `json:"key_type"`
	Fingerprint     string    `json:"fingerprint"`
	PublicKeyBase64 string    `json:"public_key_base64"`
	TrustedAt       time.Time `json:"trusted_at"`
}

type AuditLog struct {
	ID        int64     `json:"id"`
	HostID    int64     `json:"host_id"`
	HostName  string    `json:"host_name"`
	EventType string    `json:"event_type"` // CONNECT, DISCONNECT, BLOCKED_CMD, MITM_ALERT, TOFU_TRUST, VAULT_UNLOCK, VAULT_LOCK
	Severity  string    `json:"severity"`   // INFO, WARN, CRITICAL
	ClientIP  string    `json:"client_ip"`
	Detail    string    `json:"detail"`
	CreatedAt time.Time `json:"created_at"`
}

type Recording struct {
	ID        string    `json:"id"`
	HostID    int64     `json:"host_id"`
	HostName  string    `json:"host_name"`
	ClientIP  string    `json:"client_ip"`
	FilePath  string    `json:"-"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	SizeBytes int64     `json:"size_bytes"`
}

type Store struct {
	db *sql.DB
}

func Open(dbPath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)&_pragma=cache_size(-256)&_pragma=mmap_size(0)&_pragma=temp_store(FILE)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// Single-user local SQLite: keep strictly one connection to avoid multiple
	// modernc.org/sqlite page caches and mmap arenas.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	// Restrict DB file permissions to owner only
	_ = os.Chmod(dbPath, 0o600)

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate db: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// ShrinkMemory asks SQLite to release unused page cache memory immediately.
func (s *Store) ShrinkMemory() error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.Exec("PRAGMA shrink_memory")
	return err
}

func (s *Store) migrate() error {
	schema := `
CREATE TABLE IF NOT EXISTS vault_meta (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	salt_b64 TEXT NOT NULL,
	verifier_b64 TEXT NOT NULL,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS hosts (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL,
	address TEXT NOT NULL,
	port INTEGER NOT NULL DEFAULT 22,
	username TEXT NOT NULL,
	auth_type TEXT NOT NULL,
	enc_secret_dek TEXT NOT NULL DEFAULT '',
	enc_secret_data TEXT NOT NULL DEFAULT '',
	enc_pass_dek TEXT NOT NULL DEFAULT '',
	enc_pass_data TEXT NOT NULL DEFAULT '',
	guard_enabled INTEGER NOT NULL DEFAULT 1,
	notes TEXT NOT NULL DEFAULT '',
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS known_hosts (
	host_id INTEGER PRIMARY KEY,
	key_type TEXT NOT NULL,
	fingerprint TEXT NOT NULL,
	public_key_b64 TEXT NOT NULL,
	trusted_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	FOREIGN KEY(host_id) REFERENCES hosts(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS audit_logs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	host_id INTEGER NOT NULL DEFAULT 0,
	host_name TEXT NOT NULL DEFAULT '',
	event_type TEXT NOT NULL,
	severity TEXT NOT NULL DEFAULT 'INFO',
	client_ip TEXT NOT NULL DEFAULT '',
	detail TEXT NOT NULL DEFAULT '',
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS recordings (
	id TEXT PRIMARY KEY,
	host_id INTEGER NOT NULL,
	host_name TEXT NOT NULL,
	client_ip TEXT NOT NULL DEFAULT '',
	file_path TEXT NOT NULL,
	started_at DATETIME NOT NULL,
	ended_at DATETIME,
	size_bytes INTEGER NOT NULL DEFAULT 0
);
`
	_, err := s.db.Exec(schema)
	return err
}

// --- Vault Meta ---

func (s *Store) GetVaultMeta() (*crypto.VaultMeta, error) {
	var m crypto.VaultMeta
	err := s.db.QueryRow(`SELECT salt_b64, verifier_b64 FROM vault_meta WHERE id = 1`).
		Scan(&m.SaltBase64, &m.VerifierBase64)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *Store) SaveVaultMeta(m *crypto.VaultMeta) error {
	_, err := s.db.Exec(
		`INSERT INTO vault_meta (id, salt_b64, verifier_b64) VALUES (1, ?, ?)`,
		m.SaltBase64, m.VerifierBase64,
	)
	return err
}

// --- Hosts ---

func (s *Store) ListHosts() ([]Host, error) {
	rows, err := s.db.Query(`
		SELECT h.id, h.name, h.address, h.port, h.username, h.auth_type,
		       h.enc_secret_data, h.enc_pass_data, h.guard_enabled, h.notes,
		       h.created_at, h.updated_at, COALESCE(k.fingerprint, '')
		FROM hosts h
		LEFT JOIN known_hosts k ON k.host_id = h.id
		ORDER BY h.id DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hosts []Host
	for rows.Next() {
		var h Host
		var secData, passData string
		var guardInt int
		if err := rows.Scan(
			&h.ID, &h.Name, &h.Address, &h.Port, &h.Username, &h.AuthType,
			&secData, &passData, &guardInt, &h.Notes,
			&h.CreatedAt, &h.UpdatedAt, &h.KnownHostFP,
		); err != nil {
			return nil, err
		}
		h.GuardEnabled = guardInt != 0
		h.HasSecret = secData != ""
		h.HasPassphrase = passData != ""
		hosts = append(hosts, h)
	}
	if hosts == nil {
		hosts = []Host{}
	}
	return hosts, rows.Err()
}

func (s *Store) GetHost(id int64) (*Host, error) {
	var h Host
	var guardInt int
	err := s.db.QueryRow(`
		SELECT h.id, h.name, h.address, h.port, h.username, h.auth_type,
		       h.enc_secret_dek, h.enc_secret_data, h.enc_pass_dek, h.enc_pass_data,
		       h.guard_enabled, h.notes, h.created_at, h.updated_at, COALESCE(k.fingerprint, '')
		FROM hosts h
		LEFT JOIN known_hosts k ON k.host_id = h.id
		WHERE h.id = ?
	`, id).Scan(
		&h.ID, &h.Name, &h.Address, &h.Port, &h.Username, &h.AuthType,
		&h.EncSecretDEK, &h.EncSecretData, &h.EncPassDEK, &h.EncPassData,
		&guardInt, &h.Notes, &h.CreatedAt, &h.UpdatedAt, &h.KnownHostFP,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	h.GuardEnabled = guardInt != 0
	h.HasSecret = h.EncSecretData != ""
	h.HasPassphrase = h.EncPassData != ""
	return &h, nil
}

func (s *Store) CreateHost(h *Host) (int64, error) {
	guardInt := 0
	if h.GuardEnabled {
		guardInt = 1
	}
	res, err := s.db.Exec(`
		INSERT INTO hosts (
			name, address, port, username, auth_type,
			enc_secret_dek, enc_secret_data, enc_pass_dek, enc_pass_data,
			guard_enabled, notes
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, h.Name, h.Address, h.Port, h.Username, h.AuthType,
		h.EncSecretDEK, h.EncSecretData, h.EncPassDEK, h.EncPassData,
		guardInt, h.Notes,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdateHost(h *Host) error {
	guardInt := 0
	if h.GuardEnabled {
		guardInt = 1
	}
	_, err := s.db.Exec(`
		UPDATE hosts
		SET name = ?, address = ?, port = ?, username = ?, auth_type = ?,
		    enc_secret_dek = ?, enc_secret_data = ?, enc_pass_dek = ?, enc_pass_data = ?,
		    guard_enabled = ?, notes = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, h.Name, h.Address, h.Port, h.Username, h.AuthType,
		h.EncSecretDEK, h.EncSecretData, h.EncPassDEK, h.EncPassData,
		guardInt, h.Notes, h.ID,
	)
	return err
}

func (s *Store) DeleteHost(id int64) error {
	_, err := s.db.Exec(`DELETE FROM hosts WHERE id = ?`, id)
	return err
}

// --- Known Hosts (Anti-MITM TOFU) ---

func (s *Store) GetKnownHost(hostID int64) (*KnownHost, error) {
	var kh KnownHost
	err := s.db.QueryRow(`
		SELECT host_id, key_type, fingerprint, public_key_b64, trusted_at
		FROM known_hosts WHERE host_id = ?
	`, hostID).Scan(&kh.HostID, &kh.KeyType, &kh.Fingerprint, &kh.PublicKeyBase64, &kh.TrustedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &kh, nil
}

func (s *Store) UpsertKnownHost(kh *KnownHost) error {
	_, err := s.db.Exec(`
		INSERT INTO known_hosts (host_id, key_type, fingerprint, public_key_b64, trusted_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(host_id) DO UPDATE SET
			key_type = excluded.key_type,
			fingerprint = excluded.fingerprint,
			public_key_b64 = excluded.public_key_b64,
			trusted_at = CURRENT_TIMESTAMP
	`, kh.HostID, kh.KeyType, kh.Fingerprint, kh.PublicKeyBase64)
	return err
}

func (s *Store) DeleteKnownHost(hostID int64) error {
	_, err := s.db.Exec(`DELETE FROM known_hosts WHERE host_id = ?`, hostID)
	return err
}

// --- Audit Logs ---

func (s *Store) AddAuditLog(hostID int64, hostName, eventType, severity, clientIP, detail string) {
	_, _ = s.db.Exec(`
		INSERT INTO audit_logs (host_id, host_name, event_type, severity, client_ip, detail)
		VALUES (?, ?, ?, ?, ?, ?)
	`, hostID, hostName, eventType, severity, clientIP, detail)
}

func (s *Store) ListAuditLogs(limit int) ([]AuditLog, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(`
		SELECT id, host_id, host_name, event_type, severity, client_ip, detail, created_at
		FROM audit_logs ORDER BY id DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []AuditLog
	for rows.Next() {
		var l AuditLog
		if err := rows.Scan(&l.ID, &l.HostID, &l.HostName, &l.EventType, &l.Severity, &l.ClientIP, &l.Detail, &l.CreatedAt); err != nil {
			return nil, err
		}
		logs = append(logs, l)
	}
	if logs == nil {
		logs = []AuditLog{}
	}
	return logs, rows.Err()
}

// --- Recordings ---

func (s *Store) CreateRecording(r *Recording) error {
	_, err := s.db.Exec(`
		INSERT INTO recordings (id, host_id, host_name, client_ip, file_path, started_at, ended_at, size_bytes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, r.ID, r.HostID, r.HostName, r.ClientIP, r.FilePath, r.StartedAt, r.StartedAt, 0)
	return err
}

func (s *Store) FinishRecording(id string, endedAt time.Time, sizeBytes int64) error {
	_, err := s.db.Exec(`
		UPDATE recordings SET ended_at = ?, size_bytes = ? WHERE id = ?
	`, endedAt, sizeBytes, id)
	return err
}

func (s *Store) ListRecordings(limit int) ([]Recording, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT id, host_id, host_name, client_ip, file_path, started_at, ended_at, size_bytes
		FROM recordings ORDER BY started_at DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var recs []Recording
	for rows.Next() {
		var r Recording
		if err := rows.Scan(&r.ID, &r.HostID, &r.HostName, &r.ClientIP, &r.FilePath, &r.StartedAt, &r.EndedAt, &r.SizeBytes); err != nil {
			return nil, err
		}
		recs = append(recs, r)
	}
	if recs == nil {
		recs = []Recording{}
	}
	return recs, rows.Err()
}

func (s *Store) GetRecording(id string) (*Recording, error) {
	var r Recording
	err := s.db.QueryRow(`
		SELECT id, host_id, host_name, client_ip, file_path, started_at, ended_at, size_bytes
		FROM recordings WHERE id = ?
	`, id).Scan(&r.ID, &r.HostID, &r.HostName, &r.ClientIP, &r.FilePath, &r.StartedAt, &r.EndedAt, &r.SizeBytes)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}
