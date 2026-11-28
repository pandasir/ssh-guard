//go:build !windows || !portable

package desktopui

// isPortableBuild reports whether this binary was compiled as the Windows
// single-file portable executable.
func isPortableBuild() bool {
	return false
}

// EnsurePortableRuntime is a no-op outside Windows portable single-file builds.
func EnsurePortableRuntime() error {
	return nil
}
