//go:build !unix

package cmd

import "errors"

// restartExitCode 表示新版本已经装好，要重启才能用上。
const restartExitCode = 42

// restartInPlace 在非 unix 平台做不到，调用方照常退出，交给服务管理器。
func restartInPlace() error {
	return errors.New("restarting in place is not supported on this platform")
}
