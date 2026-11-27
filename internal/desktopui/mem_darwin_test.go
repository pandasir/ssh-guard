//go:build darwin && !ios

package desktopui

import (
	"encoding/binary"
	"fmt"
	"os"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

var (
	darwinMemOnce   sync.Once
	darwinMemErr    error
	procPidRusageFn func(pid, flavor int32, buf unsafe.Pointer) int32
)

// currentProcessPhysicalMemory returns the macOS process physical footprint
// (rusage_info_v2.ri_phys_footprint), matching Activity Monitor's Memory metric.
func currentProcessPhysicalMemory() (string, uint64, error) {
	darwinMemOnce.Do(func() {
		lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			darwinMemErr = err
			return
		}
		purego.RegisterLibFunc(&procPidRusageFn, lib, "proc_pid_rusage")
	})
	if darwinMemErr != nil {
		return "ri_phys_footprint", 0, darwinMemErr
	}
	var info [512]byte
	if rc := procPidRusageFn(int32(os.Getpid()), 2, unsafe.Pointer(&info[0])); rc != 0 {
		return "ri_phys_footprint", 0, fmt.Errorf("proc_pid_rusage(%d) returned %d", os.Getpid(), rc)
	}
	// In sys/resource.h rusage_info_v2, ri_phys_footprint sits at byte offset 72.
	return "ri_phys_footprint", binary.LittleEndian.Uint64(info[72:80]), nil
}
