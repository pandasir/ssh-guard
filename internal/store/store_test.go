package store

import (
	"path/filepath"
	"testing"
	"time"

	"ssh-guard/internal/crypto"
)

func TestStoreMemoryPragmasCRUDAndShrinkMemory(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "vault.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer st.Close()

	var cacheSize int
	if err := st.db.QueryRow("PRAGMA cache_size").Scan(&cacheSize); err != nil {
		t.Fatalf("query PRAGMA cache_size: %v", err)
	}
	if cacheSize != -256 {
		t.Fatalf("expected cache_size=-256, got %d", cacheSize)
	}

	var mmapSize int64
	if err := st.db.QueryRow("PRAGMA mmap_size").Scan(&mmapSize); err != nil {
		t.Fatalf("query PRAGMA mmap_size: %v", err)
	}
	if mmapSize != 0 {
		t.Fatalf("expected mmap_size=0, got %d", mmapSize)
	}

	meta := &crypto.VaultMeta{SaltBase64: "c2FsdA==", VerifierBase64: "dmVyaWZpZXI="}
	if err := st.SaveVaultMeta(meta); err != nil {
		t.Fatalf("SaveVaultMeta: %v", err)
	}
	gotMeta, err := st.GetVaultMeta()
	if err != nil || gotMeta.SaltBase64 != meta.SaltBase64 {
		t.Fatalf("GetVaultMeta mismatch: got %+v err=%v", gotMeta, err)
	}

	hostID, err := st.CreateHost(&Host{
		Name:         "prod-1",
		Address:      "10.0.0.1",
		Port:         22,
		Username:     "ops",
		AuthType:     AuthPassword,
		GuardEnabled: true,
	})
	if err != nil {
		t.Fatalf("CreateHost: %v", err)
	}

	if err := st.UpsertKnownHost(&KnownHost{
		HostID:          hostID,
		KeyType:         "ssh-ed25519",
		Fingerprint:     "SHA256:testfp",
		PublicKeyBase64: "AAAA",
	}); err != nil {
		t.Fatalf("UpsertKnownHost: %v", err)
	}

	st.AddAuditLog(hostID, "prod-1", "CONNECT", "INFO", "127.0.0.1", "ok")
	now := time.Now()
	if err := st.CreateRecording(&Recording{
		ID:        "rec-1",
		HostID:    hostID,
		HostName:  "prod-1",
		FilePath:  "/tmp/rec-1.cast",
		StartedAt: now,
	}); err != nil {
		t.Fatalf("CreateRecording: %v", err)
	}
	if err := st.FinishRecording("rec-1", now.Add(time.Second), 128); err != nil {
		t.Fatalf("FinishRecording: %v", err)
	}

	if err := st.ShrinkMemory(); err != nil {
		t.Fatalf("ShrinkMemory failed: %v", err)
	}

	hosts, err := st.ListHosts()
	if err != nil || len(hosts) != 1 || hosts[0].Name != "prod-1" {
		t.Fatalf("ListHosts after ShrinkMemory failed: hosts=%+v err=%v", hosts, err)
	}
	if err := st.DeleteHost(hostID); err != nil {
		t.Fatalf("DeleteHost: %v", err)
	}
}
