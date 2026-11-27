//go:build notlsfp

package tlsfp

import (
	"crypto/tls"
	"time"
)

// Wrap 在 -tags notlsfp 下永远返回 nil：调用方把 nil 赋给 DialTLSContext 即可，走 crypto/tls。
func Wrap(dial DialFunc, cfg *tls.Config, handshakeTimeout time.Duration) DialFunc { return nil }
