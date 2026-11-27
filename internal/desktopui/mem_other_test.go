//go:build !darwin && !windows

package desktopui

import "errors"

func currentProcessPhysicalMemory() (string, uint64, error) {
	return "unsupported", 0, errors.New("OS physical memory helper unsupported on this platform")
}
