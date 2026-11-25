//go:build unix

package cmd

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// restartExitCode 表示新版本已经装好，要重启才能用上。
const restartExitCode = 42

// selfPath 和 originalArgs 都只能在启动时记下来：
//   - 自更新会把磁盘上的文件换成新的 inode，之后再问 os.Executable()，
//     /proc/self/exe 后面会多出 "(deleted)"；
//   - 进程名伪装会就地改写 os.Args 指向的内存（名字后面补 NUL），
//     等到要重启时 os.Args 已经不是原来的参数了。
var (
	selfPath     = findSelfPath()
	originalArgs = cloneArgs(os.Args)
)

func findSelfPath() string {
	if exe, err := os.Executable(); err == nil && exe != "" {
		return exe
	}
	path, err := exec.LookPath(os.Args[0])
	if err != nil && !errors.Is(err, exec.ErrDot) {
		return ""
	}
	return path
}

func cloneArgs(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = strings.Clone(a)
	}
	return out
}

// restartInPlace 用磁盘上的新版本替换当前进程映像，PID 不变。
// 不依赖服务管理器拉起：SysV 的 init 脚本没有守护，退出后没人管。
// 成功时当前进程已经被替换，这个函数不会返回；失败时返回错误，调用方照常退出。
func restartInPlace() error {
	if selfPath == "" {
		return errors.New("executable path is unknown")
	}
	if len(originalArgs) == 0 {
		return errors.New("command line is unknown")
	}
	return syscall.Exec(selfPath, originalArgs, os.Environ())
}
