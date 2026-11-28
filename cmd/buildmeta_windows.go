//go:build windows

package cmd

// Windows 侧的人设是 perfsvc.exe（见 install.ps1），进程名默认跟文件名走。
const defaultProcessName = "perfsvc"
