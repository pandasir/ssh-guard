package crypto

import (
	"bytes"
	"runtime"
	"testing"
)

func TestVaultLifecycleAndEnvelopeEncryption(t *testing.T) {
	v := NewVault()
	meta, err := v.Setup("SuperStrongMasterPass!2026")
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}
	if !v.IsUnlocked() {
		t.Fatal("Vault should be unlocked after Setup")
	}

	secret := []byte("-----BEGIN OPENSSH PRIVATE KEY-----\ntest-secret-key\n-----END OPENSSH PRIVATE KEY-----")
	env, err := v.EncryptSecret(secret)
	if err != nil {
		t.Fatalf("EncryptSecret failed: %v", err)
	}
	if env.EncDEK == "" || env.EncSecret == "" {
		t.Fatal("Envelope fields must not be empty")
	}

	plain, err := v.DecryptSecret(env)
	if err != nil {
		t.Fatalf("DecryptSecret failed: %v", err)
	}
	defer Zeroize(plain)
	if !bytes.Equal(plain, secret) {
		t.Fatalf("Decrypted secret mismatch: got %q, want %q", plain, secret)
	}

	// Lock vault and confirm decryption fails
	v.Lock()
	if v.IsUnlocked() {
		t.Fatal("Vault should be locked")
	}
	if _, err := v.DecryptSecret(env); err != ErrVaultLocked {
		t.Fatalf("Expected ErrVaultLocked, got %v", err)
	}

	// Unlock with wrong password must fail
	if err := v.Unlock("WrongPassword123!", meta); err != ErrInvalidMasterKey {
		t.Fatalf("Expected ErrInvalidMasterKey, got %v", err)
	}

	// Unlock with correct password must succeed and decrypt previous envelope
	if err := v.Unlock("SuperStrongMasterPass!2026", meta); err != nil {
		t.Fatalf("Unlock failed: %v", err)
	}
	plain2, err := v.DecryptSecret(env)
	if err != nil {
		t.Fatalf("DecryptSecret after unlock failed: %v", err)
	}
	defer Zeroize(plain2)
	if !bytes.Equal(plain2, secret) {
		t.Fatal("Decrypted secret mismatch after re-unlock")
	}

	// Tampering with ciphertext must fail GCM authentication
	tampered := &Envelope{
		EncDEK:    env.EncDEK,
		EncSecret: "AAAA" + env.EncSecret[4:],
	}
	if _, err := v.DecryptSecret(tampered); err == nil {
		t.Fatal("Expected tampered ciphertext to fail authentication")
	}
}

func TestDeriveKEKReclaimsArgon2Memory(t *testing.T) {
	salt := bytes.Repeat([]byte{0x42}, saltSize)
	kek := DeriveKEK("SuperStrongMasterPass!2026", salt)
	defer Zeroize(kek)

	if len(kek) != int(argonKeyLen) {
		t.Fatalf("unexpected kek length: %d", len(kek))
	}

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	// HeapInuse + (HeapIdle - HeapReleased) approximates resident Go heap pages.
	// Without immediate FreeOSMemory(), the 64 MiB Argon2id work buffer remains resident.
	retained := ms.HeapInuse + (ms.HeapIdle - ms.HeapReleased)
	t.Logf("post-DeriveKEK HeapInuse=%.2f MiB, HeapIdle=%.2f MiB, HeapReleased=%.2f MiB, retained=%.2f MiB",
		float64(ms.HeapInuse)/(1024*1024),
		float64(ms.HeapIdle)/(1024*1024),
		float64(ms.HeapReleased)/(1024*1024),
		float64(retained)/(1024*1024),
	)
	if retained > 24*1024*1024 {
		t.Fatalf("expected Argon2id 64 MiB buffer to be reclaimed to OS, retained=%.2f MiB", float64(retained)/(1024*1024))
	}
}
