package desktopui

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const EnvGhosttyVTPath = "MYGO_GHOSTTY_VT"

// EnsurePortableNativeLib guarantees an offline copy of ghostty-vt.dll exists
// under <baseDir>/SSH-Guard/natives/ghostty-vt-<sha256[:12]>.dll and exports
// MYGO_GHOSTTY_VT pointing to it (unless MYGO_GHOSTTY_VT is already set).
// It returns (resolvedPath, extractedNow, error).
func EnsurePortableNativeLib(embeddedDLL []byte, baseDir string) (string, bool, error) {
	if existing := os.Getenv(EnvGhosttyVTPath); existing != "" {
		return existing, false, nil
	}
	if len(embeddedDLL) == 0 {
		return "", false, errors.New("embedded ghostty-vt.dll payload is empty")
	}
	if baseDir == "" {
		return "", false, errors.New("base cache directory is empty")
	}

	sum := sha256.Sum256(embeddedDLL)
	expectedHex := hex.EncodeToString(sum[:])
	targetDir := filepath.Join(baseDir, "SSH-Guard", "natives")
	targetPath := filepath.Join(targetDir, fmt.Sprintf("ghostty-vt-%s.dll", expectedHex[:12]))

	if fileMatchesSHA256(targetPath, int64(len(embeddedDLL)), expectedHex) {
		if err := os.Setenv(EnvGhosttyVTPath, targetPath); err != nil {
			return "", false, err
		}
		return targetPath, false, nil
	}

	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		return "", false, fmt.Errorf("create natives dir: %w", err)
	}
	tmp, err := os.CreateTemp(targetDir, "ghostty-vt-*.tmp")
	if err != nil {
		return "", false, fmt.Errorf("create temp dll: %w", err)
	}
	tmpPath := tmp.Name()
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(embeddedDLL); err != nil {
		_ = tmp.Close()
		return "", false, fmt.Errorf("write temp dll: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", false, fmt.Errorf("sync temp dll: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", false, fmt.Errorf("close temp dll: %w", err)
	}

	if err := os.Rename(tmpPath, targetPath); err != nil {
		// On Windows, os.Rename fails if a corrupted target file already exists.
		_ = os.Remove(targetPath)
		if err2 := os.Rename(tmpPath, targetPath); err2 != nil {
			return "", false, fmt.Errorf("install portable dll: %w", err2)
		}
	}
	cleanupTmp = false

	if err := os.Setenv(EnvGhosttyVTPath, targetPath); err != nil {
		return "", false, err
	}
	return targetPath, true, nil
}

func fileMatchesSHA256(path string, wantSize int64, wantHex string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() != wantSize {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	h := sha256.New()
	buf := make([]byte, 32*1024)
	if _, err := io.CopyBuffer(h, f, buf); err != nil {
		return false
	}
	return hex.EncodeToString(h.Sum(nil)) == wantHex
}
