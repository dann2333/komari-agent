//go:build linux

package ws

import (
	"net"
	"time"

	"golang.org/x/sys/unix"
)

func tuneTCP(tcp *net.TCPConn) {
	_ = tcp.SetKeepAlive(true)
	_ = tcp.SetKeepAlivePeriod(15 * time.Second)
	raw, err := tcp.SyscallConn()
	if err != nil {
		return
	}
	_ = raw.Control(func(fd uintptr) {
		// 未确认的数据超过这个时间, 连接直接失败, 不再等 tcp_retries2。
		_ = unix.SetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_USER_TIMEOUT, 30000)
	})
}
