//go:build windows

package desktopui

import (
	"syscall"
	"unsafe"
)

var k32GetProcessMemoryInfo = syscall.NewLazyDLL("kernel32.dll").NewProc("K32GetProcessMemoryInfo")

// processMemoryCountersEx2 mirrors PROCESS_MEMORY_COUNTERS_EX2 (Windows 10 1809+).
type processMemoryCountersEx2 struct {
	cb                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
	PrivateUsage               uintptr
	PrivateWorkingSetSize      uintptr
	SharedCommitUsage          uint64
}

// currentProcessPhysicalMemory returns the Windows Private Working Set size,
// matching Task Manager's Memory column.
func currentProcessPhysicalMemory() (string, uint64, error) {
	h, err := syscall.GetCurrentProcess()
	if err != nil {
		return "PrivateWorkingSetSize", 0, err
	}
	var c processMemoryCountersEx2
	c.cb = uint32(unsafe.Sizeof(c))
	if ok, _, callErr := k32GetProcessMemoryInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&c)), uintptr(c.cb)); ok == 0 {
		return "PrivateWorkingSetSize", 0, callErr
	}
	return "PrivateWorkingSetSize", uint64(c.PrivateWorkingSetSize), nil
}
