package auth

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

const (
	CookieName  = "ssh_guard_session"
	idleTimeout = 30 * time.Minute
	maxLifetime = 8 * time.Hour
)

type sessionEntry struct {
	createdAt  time.Time
	lastActive time.Time
	clientIP   string
}

type Manager struct {
	mu           sync.Mutex
	sessions     map[string]*sessionEntry
	secureCookie bool
}

func NewManager(secureCookie bool) *Manager {
	return &Manager{
		sessions:     make(map[string]*sessionEntry),
		secureCookie: secureCookie,
	}
}

func (m *Manager) Create(w http.ResponseWriter, clientIP string) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)
	now := time.Now()

	m.mu.Lock()
	m.sessions[token] = &sessionEntry{
		createdAt:  now,
		lastActive: now,
		clientIP:   clientIP,
	}
	m.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   m.secureCookie,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(maxLifetime.Seconds()),
	})
	return token, nil
}

func (m *Manager) Validate(r *http.Request) bool {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return false
	}
	return m.ValidateToken(c.Value)
}

func (m *Manager) ValidateToken(token string) bool {
	if token == "" {
		return false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.sessions[token]
	if !ok {
		return false
	}
	now := time.Now()
	if now.Sub(entry.lastActive) > idleTimeout || now.Sub(entry.createdAt) > maxLifetime {
		delete(m.sessions, token)
		return false
	}
	entry.lastActive = now
	return true
}

func (m *Manager) RevokeAll(w http.ResponseWriter) {
	m.mu.Lock()
	m.sessions = make(map[string]*sessionEntry)
	m.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   m.secureCookie,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}
