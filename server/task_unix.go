//go:build !windows

package server

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// detachedTaskCommand 让任务不挂在 agent 名下：外层 sh 把任务放到后台就退出，任务被
// init 收养。脚本仍然从标准输入读；非交互 shell 会把后台命令的标准输入换成 /dev/null，
// 所以先留一份再接回去。里外各包一层 sh 是为了把退出码经 3 号描述符传回来。
func detachedTaskCommand(command string, status *os.File) *exec.Cmd {
	cmd := exec.Command("sh", "-c", `exec 4<&0; sh -c 'sh -s 3<&-; echo "$?" >&3' <&4 4<&- &`)
	cmd.Stdin = strings.NewReader(command)
	cmd.ExtraFiles = []*os.File{status}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd
}
