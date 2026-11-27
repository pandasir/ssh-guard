package crypto

import (
	"bytes"
	"runtime"
	"runtime/debug"
	"sync"
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

func TestDeriveKEKRestoresTightMemoryLimitAndPreservesHigherLimit(t *testing.T) {
	origLimit := debug.SetMemoryLimit(-1)
	t.Cleanup(func() {
		debug.SetMemoryLimit(origLimit)
	})

	salt := bytes.Repeat([]byte{0x24}, saltSize)

	// 1. Tight steady-state limit (28 MiB) must be temporarily raised and restored cleanly,
	// even when multiple goroutines call DeriveKEK concurrently.
	const tightLimit int64 = 28 << 20
	debug.SetMemoryLimit(tightLimit)

	var (
		wg   sync.WaitGroup
		keks [2][]byte
	)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			keks[idx] = DeriveKEK("SuperStrongMasterPass!2026", salt)
		}(i)
	}
	wg.Wait()
	defer Zeroize(keks[0])
	defer Zeroize(keks[1])

	if !bytes.Equal(keks[0], keks[1]) || len(keks[0]) != int(argonKeyLen) {
		t.Fatalf("unexpected concurrent KEK outputs: len0=%d len1=%d", len(keks[0]), len(keks[1]))
	}
	if got := debug.SetMemoryLimit(-1); got != tightLimit {
		t.Fatalf("expected GOMEMLIMIT restored to %d, got %d", tightLimit, got)
	}

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	retained := ms.HeapInuse + (ms.HeapIdle - ms.HeapReleased)
	if retained > 24*1024*1024 {
		t.Fatalf("expected retained heap <= 24 MiB after tight-limit DeriveKEK, got %.2f MiB", float64(retained)/(1024*1024))
	}

	// 2. Higher finite limit (>= 96 MiB) must not be lowered or altered.
	const higherLimit int64 = 128 << 20
	debug.SetMemoryLimit(higherLimit)
	kek := DeriveKEK("SuperStrongMasterPass!2026", salt)
	defer Zeroize(kek)
	if got := debug.SetMemoryLimit(-1); got != higherLimit {
		t.Fatalf("expected higher GOMEMLIMIT %d preserved, got %d", higherLimit, got)
	}
}
