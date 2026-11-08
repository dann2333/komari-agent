package ws

import "net"

// Tune 收紧这条连接的重传上限。写进内核缓冲算成功, 对端不 ACK 时
// 默认要等系统的重传次数用完, 面板会有好几分钟收不到上报。
func Tune(conn net.Conn) {
	tcp := tcpConn(conn)
	if tcp == nil {
		return
	}
	tuneTCP(tcp)
}

func tcpConn(c net.Conn) *net.TCPConn {
	for i := 0; i < 5 && c != nil; i++ {
		if tcp, ok := c.(*net.TCPConn); ok {
			return tcp
		}
		u, ok := c.(interface{ NetConn() net.Conn })
		if !ok {
			return nil
		}
		next := u.NetConn()
		if next == c {
			return nil
		}
		c = next
	}
	return nil
}
