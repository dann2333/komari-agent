package dnsresolver

import (
	"net/http"

	"github.com/komari-monitor/komari-agent/internal/ua"
)

// withUserAgent 给没带 User-Agent 的请求补上统一 UA，调用方自己设了的不动。
// 包在 Transport 外面而不是逐个请求去改：面板、文件传输、自更新、自动发现共用这一个客户端。
func withUserAgent(rt http.RoundTripper) http.RoundTripper {
	return &uaTransport{rt}
}

type uaTransport struct{ http.RoundTripper }

// Unwrap 交出底层 Transport，需要检查 ForceAttemptHTTP2 / TLSNextProto 之类配置的地方用。
func (t *uaTransport) Unwrap() http.RoundTripper { return t.RoundTripper }

func (t *uaTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Header.Get("User-Agent") == "" {
		r.Header.Set("User-Agent", ua.UserAgent)
	}
	return t.RoundTripper.RoundTrip(r)
}

// 要透传，不然 http.Client.CloseIdleConnections 找不到底层 Transport。
func (t *uaTransport) CloseIdleConnections() {
	if c, ok := t.RoundTripper.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}
