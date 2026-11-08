//go:build !linux && !windows

package ws

import "net"

func tuneTCP(tcp *net.TCPConn) {
	_ = tcp.SetKeepAlive(true)
}
