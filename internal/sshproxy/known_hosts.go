package sshproxy

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"

	"ssh-guard/internal/store"

	"golang.org/x/crypto/ssh"
)

// HostKeyUntrustedError is returned on first connection when TOFU approval is required.
type HostKeyUntrustedError struct {
	KeyType         string
	Fingerprint     string
	PublicKeyBase64 string
}

func (e *HostKeyUntrustedError) Error() string {
	return fmt.Sprintf("untrusted host key: %s (%s)", e.Fingerprint, e.KeyType)
}

// HostKeyMismatchError is returned when a host's public key has changed (potential MITM).
type HostKeyMismatchError struct {
	ExpectedFP      string
	ActualFP        string
	ActualKeyType   string
	ActualPubKeyB64 string
}

func (e *HostKeyMismatchError) Error() string {
	return fmt.Sprintf("HOST KEY MISMATCH! expected %s, got %s", e.ExpectedFP, e.ActualFP)
}

// NewStrictHostKeyCallback enforces TOFU + strict fingerprint matching.
// It NEVER silently accepts unknown or changed host keys.
func NewStrictHostKeyCallback(st *store.Store, hostID int64) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		actualFP := ssh.FingerprintSHA256(key)
		actualType := key.Type()
		actualPubB64 := base64.StdEncoding.EncodeToString(key.Marshal())

		kh, err := st.GetKnownHost(hostID)
		if errors.Is(err, store.ErrNotFound) {
			return &HostKeyUntrustedError{
				KeyType:         actualType,
				Fingerprint:     actualFP,
				PublicKeyBase64: actualPubB64,
			}
		}
		if err != nil {
			return fmt.Errorf("lookup known host: %w", err)
		}

		if kh.Fingerprint != actualFP || kh.PublicKeyBase64 != actualPubB64 {
			return &HostKeyMismatchError{
				ExpectedFP:      kh.Fingerprint,
				ActualFP:        actualFP,
				ActualKeyType:   actualType,
				ActualPubKeyB64: actualPubB64,
			}
		}
		return nil
	}
}
