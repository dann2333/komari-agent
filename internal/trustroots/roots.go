package trustroots

import (
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"net/http"
	"sync"
)

// cacert.pem 是 curl 提取的 Mozilla 公共根证书。
// 只信任这份内置列表，不使用系统证书库，避免本机被加进伪造根证书后做中间人。
//
//go:embed cacert.pem
var mozillaPEM []byte

var (
	once sync.Once
	pool *x509.CertPool
)

// Roots 只返回程序内置的公共根证书。
func Roots() *x509.CertPool {
	once.Do(func() {
		p := x509.NewCertPool()
		if !p.AppendCertsFromPEM(mozillaPEM) {
			p = x509.NewCertPool()
		}
		pool = p
	})
	return pool
}

// Config 构造带内置根证书的 TLS 配置。insecure 为真时跳过校验。
func Config(insecure bool) *tls.Config {
	return &tls.Config{
		RootCAs:            Roots(),
		InsecureSkipVerify: insecure,
	}
}

func init() {
	if transport, ok := http.DefaultTransport.(*http.Transport); ok && transport.TLSClientConfig == nil {
		transport.TLSClientConfig = Config(false)
	}
}
