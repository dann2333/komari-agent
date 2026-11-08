//go:build windows

package ws

import (
	"net"
	"time"

	"golang.org/x/sys/windows"
)

func tuneTCP(tcp *net.TCPConn) {
	_ = tcp.SetKeepAlive(true)
	_ = tcp.SetKeepAlivePeriod(15 * time.Second)
	raw, err := tcp.SyscallConn()
	if err != nil {
		return
	}
	_ = raw.Control(func(fd uintptr) {
		// TCP_MAXRT: 重传最长秒数。0 表示沿用系统默认, 那个默认可以拖到好几分钟。
		_ = windows.SetsockoptInt(windows.Handle(fd), windows.IPPROTO_TCP, 5, 30)
	})
}
