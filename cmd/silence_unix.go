//go:build unix

package cmd

import (
	"os"

	"golang.org/x/sys/unix"
)

// silenceOutput 把进程的 1、2 号描述符指向 /dev/null。
// 之后不管日志有没有走标准 logger、子进程有没有继承标准输出，都写不到 stderr 上。
func silenceOutput() {
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return
	}
	defer devnull.Close()
	_ = unix.Dup2(int(devnull.Fd()), unix.Stdout)
	_ = unix.Dup2(int(devnull.Fd()), unix.Stderr)
}
