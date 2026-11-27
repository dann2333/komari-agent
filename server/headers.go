package server

import (
	"net/http"

	"github.com/komari-monitor/komari-agent/internal/ua"
)

// wsHeaders 是所有 WebSocket 握手都带的请求头。gorilla 的 Dialer 自己不设 User-Agent，
// 不传的话握手里就没有 UA，在反代和 WAF 眼里是个空白特征。
var wsHeaders = http.Header{"User-Agent": {ua.UserAgent}}
