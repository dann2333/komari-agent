//go:build !notlsfp

package tlsfp

import (
	"crypto/tls"

	utls "github.com/refraction-networking/utls"
)

// openSSL 是一份手写的 OpenSSL 3.x 默认 ClientHello：TLS 1.3 三件套在前，后面是
// ECDHE 和静态 RSA 的 1.2 列表；renegotiation_info 放最前、空 session_ticket、
// encrypt_then_mac、extended_master_secret、psk_dhe_ke、单个 x25519 key_share，
// 这些都是 OpenSSL 的形状。ALPN 只宣告 http/1.1，和 agent 实际走的 HTTP/1.1 一致。
//
// 和真 OpenSSL 的差别只有一处：没有 DHE_RSA 套件（uTLS 不支持 DHE 密钥交换，
// 宣告了会被选中导致握手失败）。
// 这不是从某个具体 curl 抓的逐字节规格；要精确匹配某个发行版，用 tcpdump 抓一份
// 参考 curl 的 ClientHello，换成 ClientHelloSpec 替换这里即可。
func openSSL() *utls.ClientHelloSpec {
	const (
		x448                  = utls.CurveID(0x001e)
		ed448                 = utls.SignatureScheme(0x0808)
		pssPssSHA256          = utls.SignatureScheme(0x0809)
		pssPssSHA384          = utls.SignatureScheme(0x080a)
		pssPssSHA512          = utls.SignatureScheme(0x080b)
		encryptThenMAC uint16 = 22
	)
	return &utls.ClientHelloSpec{
		TLSVersMin:         tls.VersionTLS12,
		TLSVersMax:         tls.VersionTLS13,
		CompressionMethods: []uint8{0},
		CipherSuites: []uint16{
			utls.TLS_AES_256_GCM_SHA384,
			utls.TLS_CHACHA20_POLY1305_SHA256,
			utls.TLS_AES_128_GCM_SHA256,
			utls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			utls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			utls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
			utls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
			utls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			utls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			0xc024, // TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA384，uTLS 没起名
			0xc028, // TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA384
			utls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA256,
			utls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA256,
			utls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
			utls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
			utls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,
			utls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
			utls.TLS_RSA_WITH_AES_256_GCM_SHA384,
			utls.TLS_RSA_WITH_AES_128_GCM_SHA256,
			0x003d, // TLS_RSA_WITH_AES_256_CBC_SHA256
			utls.TLS_RSA_WITH_AES_128_CBC_SHA256,
			utls.TLS_RSA_WITH_AES_256_CBC_SHA,
			utls.TLS_RSA_WITH_AES_128_CBC_SHA,
		},
		Extensions: []utls.TLSExtension{
			&utls.RenegotiationInfoExtension{},
			&utls.SNIExtension{},
			// uncompressed, ansiX962_compressed_prime, ansiX962_compressed_char2
			&utls.SupportedPointsExtension{SupportedPoints: []uint8{0, 1, 2}},
			&utls.SupportedCurvesExtension{Curves: []utls.CurveID{utls.X25519, utls.CurveP256, x448, utls.CurveP521, utls.CurveP384}},
			&utls.SessionTicketExtension{},
			&utls.GenericExtension{Id: encryptThenMAC},
			&utls.ExtendedMasterSecretExtension{},
			&utls.SignatureAlgorithmsExtension{SupportedSignatureAlgorithms: []utls.SignatureScheme{
				utls.ECDSAWithP256AndSHA256, utls.ECDSAWithP384AndSHA384, utls.ECDSAWithP521AndSHA512,
				utls.Ed25519, ed448,
				pssPssSHA256, pssPssSHA384, pssPssSHA512,
				utls.PSSWithSHA256, utls.PSSWithSHA384, utls.PSSWithSHA512,
				utls.PKCS1WithSHA256, utls.PKCS1WithSHA384, utls.PKCS1WithSHA512,
				utls.FakeSHA256WithDSA,
				utls.ECDSAWithSHA1, utls.PKCS1WithSHA1, utls.FakeSHA1WithDSA,
			}},
			&utls.SupportedVersionsExtension{Versions: []uint16{tls.VersionTLS13, tls.VersionTLS12}},
			&utls.PSKKeyExchangeModesExtension{Modes: []uint8{utls.PskModeDHE}},
			// Data 留空，uTLS 在 ApplyPreset 里生成 x25519 的 key share
			&utls.KeyShareExtension{KeyShares: []utls.KeyShare{{Group: utls.X25519}}},
			&utls.ALPNExtension{AlpnProtocols: []string{"http/1.1"}},
		},
	}
}
