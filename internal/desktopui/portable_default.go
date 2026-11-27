//go:build !windows || !portable

package desktopui

// EnsurePortableRuntime is a no-op outside Windows portable single-file builds.
func EnsurePortableRuntime() error {
	return nil
}
