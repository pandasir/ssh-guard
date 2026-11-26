package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"runtime/debug"
	"sync"

	"golang.org/x/crypto/argon2"
)

const (
	// Argon2id parameters (OWASP recommended baseline for interactive unlock)
	argonTime    uint32 = 3
	argonMemory  uint32 = 64 * 1024 // 64 MiB
	argonThreads uint8  = 4
	argonKeyLen  uint32 = 32 // AES-256

	saltSize  = 32
	dekSize   = 32
	canaryStr = "SSH-GUARD-VAULT-VERIFIER-V1"
)

var (
	ErrVaultLocked      = errors.New("vault is locked")
	ErrInvalidMasterKey = errors.New("invalid master password")
	ErrCorruptedCipher  = errors.New("corrupted or tampered ciphertext")
)

// Envelope represents an envelope-encrypted secret stored in SQLite.
// Each secret has its own random 256-bit DEK encrypted by the in-memory KEK.
type Envelope struct {
	EncDEK    string `json:"enc_dek"`    // base64(nonce || AES-GCM(KEK, DEK))
	EncSecret string `json:"enc_secret"` // base64(nonce || AES-GCM(DEK, plaintext))
}

// VaultMeta holds non-secret salt and KEK verification canary persisted in DB.
type VaultMeta struct {
	SaltBase64     string
	VerifierBase64 string
}

// Vault holds the derived KEK strictly in memory (never written to disk).
type Vault struct {
	mu  sync.RWMutex
	kek []byte
}

func NewVault() *Vault {
	return &Vault{}
}

// Zeroize overwrites a byte slice with zeros to minimize secret lifetime in memory.
func Zeroize(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// DeriveKEK derives a 32-byte Key Encryption Key from masterPassword and salt using Argon2id.
// Immediately reclaims the transient 64 MiB Argon2id work buffer back to the OS so desktop
// processes do not retain a 64 MiB RSS spike waiting on Go's periodic GC/scavenger.
func DeriveKEK(masterPassword string, salt []byte) []byte {
	kek := argon2.IDKey([]byte(masterPassword), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	debug.FreeOSMemory()
	return kek
}

// Setup initializes a brand-new vault from a master password, keeps KEK in memory,
// and returns VaultMeta to be persisted in the database.
func (v *Vault) Setup(masterPassword string) (*VaultMeta, error) {
	if len(masterPassword) < 8 {
		return nil, errors.New("master password must be at least 8 characters")
	}
	salt := make([]byte, saltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("generate salt: %w", err)
	}

	kek := DeriveKEK(masterPassword, salt)
	verifier, err := aesGCMEncrypt(kek, []byte(canaryStr))
	if err != nil {
		Zeroize(kek)
		return nil, fmt.Errorf("create verifier: %w", err)
	}

	v.mu.Lock()
	Zeroize(v.kek)
	v.kek = kek
	v.mu.Unlock()

	return &VaultMeta{
		SaltBase64:     base64.StdEncoding.EncodeToString(salt),
		VerifierBase64: base64.StdEncoding.EncodeToString(verifier),
	}, nil
}

// Unlock derives KEK from masterPassword, validates against stored verifier, and caches KEK in memory.
func (v *Vault) Unlock(masterPassword string, meta *VaultMeta) error {
	salt, err := base64.StdEncoding.DecodeString(meta.SaltBase64)
	if err != nil {
		return fmt.Errorf("decode salt: %w", err)
	}
	verifier, err := base64.StdEncoding.DecodeString(meta.VerifierBase64)
	if err != nil {
		return fmt.Errorf("decode verifier: %w", err)
	}

	kek := DeriveKEK(masterPassword, salt)
	plain, err := aesGCMDecrypt(kek, verifier)
	if err != nil || string(plain) != canaryStr {
		Zeroize(kek)
		Zeroize(plain)
		return ErrInvalidMasterKey
	}
	Zeroize(plain)

	v.mu.Lock()
	Zeroize(v.kek)
	v.kek = kek
	v.mu.Unlock()
	return nil
}

// Lock wipes the in-memory KEK immediately.
func (v *Vault) Lock() {
	v.mu.Lock()
	Zeroize(v.kek)
	v.kek = nil
	v.mu.Unlock()
}

// IsUnlocked returns whether KEK is currently loaded in memory.
func (v *Vault) IsUnlocked() bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.kek) == int(argonKeyLen)
}

// EncryptSecret performs envelope encryption:
// 1. Generates a fresh random 256-bit DEK
// 2. Encrypts plaintext with DEK (AES-256-GCM)
// 3. Encrypts DEK with in-memory KEK (AES-256-GCM)
// 4. Zeroizes DEK immediately
func (v *Vault) EncryptSecret(plaintext []byte) (*Envelope, error) {
	v.mu.RLock()
	if len(v.kek) != int(argonKeyLen) {
		v.mu.RUnlock()
		return nil, ErrVaultLocked
	}
	kekCopy := make([]byte, len(v.kek))
	copy(kekCopy, v.kek)
	v.mu.RUnlock()
	defer Zeroize(kekCopy)

	if len(plaintext) == 0 {
		return &Envelope{}, nil
	}

	dek := make([]byte, dekSize)
	if _, err := io.ReadFull(rand.Reader, dek); err != nil {
		return nil, fmt.Errorf("generate dek: %w", err)
	}
	defer Zeroize(dek)

	encSecret, err := aesGCMEncrypt(dek, plaintext)
	if err != nil {
		return nil, fmt.Errorf("encrypt secret: %w", err)
	}
	encDEK, err := aesGCMEncrypt(kekCopy, dek)
	if err != nil {
		return nil, fmt.Errorf("encrypt dek: %w", err)
	}

	return &Envelope{
		EncDEK:    base64.StdEncoding.EncodeToString(encDEK),
		EncSecret: base64.StdEncoding.EncodeToString(encSecret),
	}, nil
}

// DecryptSecret unwraps the DEK using KEK and decrypts the secret.
// Caller MUST call Zeroize() on the returned byte slice as soon as possible.
func (v *Vault) DecryptSecret(env *Envelope) ([]byte, error) {
	if env == nil || (env.EncDEK == "" && env.EncSecret == "") {
		return nil, nil
	}

	v.mu.RLock()
	if len(v.kek) != int(argonKeyLen) {
		v.mu.RUnlock()
		return nil, ErrVaultLocked
	}
	kekCopy := make([]byte, len(v.kek))
	copy(kekCopy, v.kek)
	v.mu.RUnlock()
	defer Zeroize(kekCopy)

	rawEncDEK, err := base64.StdEncoding.DecodeString(env.EncDEK)
	if err != nil {
		return nil, ErrCorruptedCipher
	}
	rawEncSecret, err := base64.StdEncoding.DecodeString(env.EncSecret)
	if err != nil {
		return nil, ErrCorruptedCipher
	}

	dek, err := aesGCMDecrypt(kekCopy, rawEncDEK)
	if err != nil {
		return nil, ErrCorruptedCipher
	}
	defer Zeroize(dek)

	plain, err := aesGCMDecrypt(dek, rawEncSecret)
	if err != nil {
		return nil, ErrCorruptedCipher
	}
	return plain, nil
}

func aesGCMEncrypt(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	// Prepend nonce to ciphertext+tag
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func aesGCMDecrypt(key, blob []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(blob) < gcm.NonceSize() {
		return nil, ErrCorruptedCipher
	}
	nonce := blob[:gcm.NonceSize()]
	ciphertext := blob[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ciphertext, nil)
}
