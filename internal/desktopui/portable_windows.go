//go:build windows && portable

package desktopui

import (
	_ "embed"
	"os"
)

//go:embed nativeembed/ghostty-vt.dll
var embeddedGhosttyDLL []byte

// isPortableBuild reports whether this binary was compiled as the Windows
// single-file portable executable.
func isPortableBuild() bool {
	return true
}

// EnsurePortableRuntime extracts the embedded ghostty-vt.dll to %LocalAppData%
// with SHA-256 verification before terminal.Load() runs.
func EnsurePortableRuntime() error {
	baseDir := os.Getenv("LOCALAPPDATA")
	if baseDir == "" {
		if d, err := os.UserCacheDir(); err == nil && d != "" {
			baseDir = d
		} else {
			baseDir = os.TempDir()
		}
	}
	_, _, err := EnsurePortableNativeLib(embeddedGhosttyDLL, baseDir)
	return err
}
