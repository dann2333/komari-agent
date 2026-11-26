//go:build windows

package server

import (
	"os"
	"os/exec"
)

// detachedTaskCommand 只有 Unix 版本。Windows 的任务走 buildTaskCommand 里的
// PowerShell 分支，这里只是让那个跨平台的函数能编译。
func detachedTaskCommand(string, *os.File) *exec.Cmd { return nil }
