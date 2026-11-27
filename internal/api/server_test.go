package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"ssh-guard/internal/auth"
	"ssh-guard/internal/crypto"
	"ssh-guard/internal/store"
)

func TestWebServerVaultHostsAndCSPRegression(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "web.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	v := crypto.NewVault()
	sm := auth.NewManager(false)
	srv := NewServer(st, v, sm, filepath.Join(dir, "recordings"))

	// 1. Static index.html serves with strict CSP.
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "default-src 'self'") {
		t.Fatalf("unexpected static response: code=%d csp=%q", rec.Code, rec.Header().Get("Content-Security-Policy"))
	}

	// 2. Setup vault sets HttpOnly session cookie and does not return desktop session_token.
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/vault/setup", strings.NewReader(`{"master_password":"WebMasterPass!2026"}`))
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup failed: %d %s", rec.Code, rec.Body.String())
	}
	var setupResp map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &setupResp)
	if _, hasDesktopToken := setupResp["session_token"]; hasDesktopToken {
		t.Fatalf("web edition must not leak desktop session_token: %+v", setupResp)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 || cookies[0].Name != auth.CookieName {
		t.Fatalf("expected session cookie, got %+v", cookies)
	}

	// 3. Creating host via cookie + CSRF header succeeds.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/hosts", strings.NewReader(`{"name":"web-node","address":"10.0.0.9","port":22,"username":"root","auth_type":"password","secret":"pw","guard_enabled":true}`))
	req.AddCookie(cookies[0])
	req.Header.Set("X-Requested-With", "SSHGuard")
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create host failed: %d %s", rec.Code, rec.Body.String())
	}
}
