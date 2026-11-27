//go:build !notlsfp

package tlsfp

import (
	"context"
	"crypto/tls"
	"net"
	"time"

	utls "github.com/refraction-networking/utls"
)

// Wrap 包一层 TLS 拨号器：dial 负责拿到 TCP 连接（含原来的自定义 DNS 解析），
// cfg 是原有的 TLS 配置（钉死的根证书、InsecureSkipVerify），handshakeTimeout 只约束握手这一步
// （Transport 一旦有自定义 TLS 拨号器就不再管 TLSHandshakeTimeout，这里得自己兜住）。
// 有代理、或者目标是 IP 字面量（没有 SNI 可发）时照旧走 crypto/tls。
func Wrap(dial DialFunc, cfg *tls.Config, handshakeTimeout time.Duration) DialFunc {
	if dial == nil || cfg == nil || !Direct() {
		return nil
	}
	cfg = cfg.Clone()
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		conn, err := dial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		if handshakeTimeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, handshakeTimeout)
			defer cancel()
		}
		if net.ParseIP(host) != nil {
			return handshake(ctx, conn, cfg, host)
		}
		return hello(ctx, conn, cfg, host)
	}
}

func handshake(ctx context.Context, conn net.Conn, cfg *tls.Config, host string) (net.Conn, error) {
	c := cfg.Clone()
	if c.ServerName == "" {
		c.ServerName = host
	}
	tc := tls.Client(conn, c)
	if err := tc.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return tc, nil
}

func hello(ctx context.Context, conn net.Conn, cfg *tls.Config, host string) (net.Conn, error) {
	if cfg.ServerName != "" {
		host = cfg.ServerName
	}
	// 不拷贝 NextProtos：ALPN 由规格里的 ALPNExtension 决定，只宣告 http/1.1
	uc := utls.UClient(conn, &utls.Config{
		RootCAs:            cfg.RootCAs,
		InsecureSkipVerify: cfg.InsecureSkipVerify,
		ServerName:         host,
		MinVersion:         cfg.MinVersion,
		MaxVersion:         cfg.MaxVersion,
	}, utls.HelloCustom)
	if err := uc.ApplyPreset(openSSL()); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := uc.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return uc, nil
}
