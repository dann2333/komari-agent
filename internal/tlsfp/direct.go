package tlsfp

import (
	"net/url"

	"golang.org/x/net/http/httpproxy"
)

// Direct 报告出站连接是否会直连：环境里没配 HTTPS 代理才算。
// 用 httpproxy 而不是 http.ProxyFromEnvironment，后者第一次调用后会把环境变量缓存住。
func Direct() bool {
	proxyURL, err := httpproxy.FromEnvironment().ProxyFunc()(&url.URL{Scheme: "https", Host: "proxy-probe.invalid"})
	return err == nil && proxyURL == nil
}
