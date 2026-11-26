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
	// prctl 只改调用线程；/proc/<pid>/comm 显示的是主线程，所以再写一次。
	// 名字后面不能带换行：新内核不剥空白，会把它算进名字里，
	// ps -e 就显示成 "rsyslogd?"。
	_ = os.WriteFile("/proc/self/comm", []byte(comm), 0)
}
