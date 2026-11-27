// Package tlsfp 让直连的 TLS 握手用 OpenSSL 形状的 ClientHello，而不是 Go 自带的那个。
//
// Go 的 crypto/tls 发出的 ClientHello（密码套件、扩展顺序、没有 encrypt_then_mac、
// ALPN）在 JA3/JA4 库里都是现成的条目，路径上任何人不解密就能把这条流归类成
// "Go 客户端"；钉死根证书只保护内容，保护不了这一段明文。
//
// 只在直连时生效。配置了 HTTPS_PROXY / SOCKS 代理的连接照旧走 crypto/tls：
// Go 的 Transport 和 gorilla 的 Dialer 一旦有自定义 TLS 钩子，就会把代理地址当成
// TLS 对端直接握手，CONNECT / SOCKS 全乱，所以 Wrap 在有代理时返回 nil，
// 调用方把 nil 赋给 DialTLSContext 即可，什么都不用改。
// 编译时加 -tags notlsfp 可以整体关掉。
package tlsfp

import (
	"context"
	"net"
)

// DialFunc 是 Transport.DialContext / websocket.Dialer.NetDialContext 那种签名的拨号函数。
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)
