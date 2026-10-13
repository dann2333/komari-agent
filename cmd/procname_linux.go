//go:build linux

package cmd

import (
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

func setComm(name string) {
	comm := name
	if len(comm) > 15 {
		comm = comm[:15]
	}
	b := append([]byte(comm), 0)
	_, _, _ = unix.RawSyscall6(unix.SYS_PRCTL, unix.PR_SET_NAME, uintptr(unsafe.Pointer(&b[0])), 0, 0, 0, 0)
	_ = os.WriteFile("/proc/self/comm", append([]byte(comm), '\n'), 0)
}
