package dnsresolver

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/komari-monitor/komari-agent/internal/tlsfp"
	"github.com/komari-monitor/komari-agent/internal/ua"
)

// TestClientUsesUAAndHTTP11 走完整的 net/http 路径：自定义 TLS 拨号器 + UA 包装层 + HTTP/1.1。
// httptest 监听在 127.0.0.1，为了绕开 IP 字面量走 crypto/tls 的回退，URL 用 localhost，
// 拨号器把主机名换回 127.0.0.1，证书校验按 cfg.ServerName 走。
func TestClientUsesUAAndHTTP11(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.Header.Get("User-Agent")+" "+r.Proto)
	}))
	srv.StartTLS()
	defer srv.Close()

	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	cfg := &tls.Config{RootCAs: roots, ServerName: "example.com"} // httptest 自带证书的 SAN
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp4", srv.Listener.Addr().String())
	}
	transport := buildTransportWithPreferenceAndHTTP2(10*time.Second, cfg, "", true)
	transport.DialTLSContext = tlsfp.Wrap(dial, cfg, 10*time.Second)
	client := &http.Client{Transport: withUserAgent(transport), Timeout: 10 * time.Second}

	for _, host := range []string{"localhost", "127.0.0.1"} {
		_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
		resp, err := client.Get("https://" + net.JoinHostPort(host, port))
		if err != nil {
			t.Fatalf("GET via %s: %v", host, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if got, want := string(body), ua.UserAgent+" HTTP/1.1"; got != want {
			t.Errorf("via %s: server saw %q, want %q", host, got, want)
		}
	}
}
