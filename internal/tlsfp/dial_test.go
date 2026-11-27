//go:build !notlsfp

package tlsfp

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	utls "github.com/refraction-networking/utls"
)

// clientHello 是从线上读下来解析过的 ClientHello，只取测试要断言的字段。
type clientHello struct {
	raw        []byte
	ciphers    []uint16
	extensions []uint16
	curves     []uint16
	versions   []uint16
	alpn       []string
	serverName string
}

func (h clientHello) ja3() string {
	return fmt.Sprintf("771,%s,%s,%s,0", joinU16(h.ciphers), joinU16(h.extensions), joinU16(h.curves))
}

func joinU16(v []uint16) string {
	parts := make([]string, len(v))
	for i, x := range v {
		parts[i] = fmt.Sprintf("%d", x)
	}
	return strings.Join(parts, "-")
}

func readU8(r io.Reader) uint8 {
	var b [1]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		panic(err)
	}
	return b[0]
}

func readU16(r io.Reader) uint16 {
	var b [2]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		panic(err)
	}
	return binary.BigEndian.Uint16(b[:])
}

// readClientHello 从一条 TCP 连接上读走第一条记录并解析成 clientHello。
func readClientHello(conn net.Conn) (clientHello, error) {
	var h clientHello
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()

	var rec [5]byte
	if _, err := io.ReadFull(conn, rec[:]); err != nil {
		return h, err
	}
	if rec[0] != 0x16 {
		return h, fmt.Errorf("record type %d, want handshake 22", rec[0])
	}
	body := make([]byte, int(rec[3])<<8|int(rec[4]))
	if _, err := io.ReadFull(conn, body); err != nil {
		return h, err
	}
	h.raw = append(append([]byte{}, rec[:]...), body...)
	if len(body) < 4 || body[0] != 1 {
		return h, fmt.Errorf("handshake type %d, want ClientHello 1", body[0])
	}
	r := bytes.NewReader(body[4:])
	if _, err := r.Seek(2+32, io.SeekCurrent); err != nil { // legacy_version, random
		return h, err
	}
	if _, err := r.Seek(int64(readU8(r)), io.SeekCurrent); err != nil { // session_id
		return h, err
	}
	h.ciphers = make([]uint16, readU16(r)/2)
	for i := range h.ciphers {
		h.ciphers[i] = readU16(r)
	}
	if _, err := r.Seek(int64(readU8(r)), io.SeekCurrent); err != nil { // compression methods
		return h, err
	}
	ext := io.LimitReader(r, int64(readU16(r)))
	for {
		var head [4]byte
		if _, err := io.ReadFull(ext, head[:]); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return h, err
		}
		typ, size := binary.BigEndian.Uint16(head[:2]), int(binary.BigEndian.Uint16(head[2:]))
		data := make([]byte, size)
		if _, err := io.ReadFull(ext, data); err != nil {
			return h, err
		}
		h.extensions = append(h.extensions, typ)
		d := bytes.NewReader(data)
		switch typ {
		case 0:
			d.Seek(2, io.SeekCurrent)
			readU8(d)
			n := int(readU16(d))
			h.serverName = string(data[len(data)-n:])
		case 10:
			d.Seek(2, io.SeekCurrent)
			for d.Len() > 0 {
				h.curves = append(h.curves, readU16(d))
			}
		case 16:
			d.Seek(2, io.SeekCurrent)
			for d.Len() > 0 {
				n := int(readU8(d))
				h.alpn = append(h.alpn, string(data[len(data)-d.Len():len(data)-d.Len()+n]))
				d.Seek(int64(n), io.SeekCurrent)
			}
		case 43:
			n := int(readU8(d))
			for i := 0; i < n/2; i++ {
				h.versions = append(h.versions, readU16(d))
			}
		}
	}
	return h, nil
}

// prefixConn 把已经读走的字节还给 TLS 服务端。
type prefixConn struct {
	net.Conn
	r io.Reader
}

func (c *prefixConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// serveTLS 起一个只接一条连接的 TLS 服务端，把 ClientHello 解析后交给 captured。
func serveTLS(t *testing.T, maxVersion uint16) (port string, roots *x509.CertPool, captured <-chan clientHello) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots = x509.NewCertPool()
	roots.AddCert(leaf)

	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	ch := make(chan clientHello, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			hello, err := readClientHello(conn)
			if err != nil {
				t.Errorf("read ClientHello: %v", err)
				return
			}
			ch <- hello
			pc := &prefixConn{Conn: conn, r: io.MultiReader(bytes.NewReader(hello.raw), conn)}
			if hsErr := (tls.Server(pc, &tls.Config{
				Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
				MinVersion:   tls.VersionTLS12,
				MaxVersion:   maxVersion,
				NextProtos:   []string{"http/1.1"},
			})).Handshake(); hsErr != nil {
				t.Logf("server side handshake: %v", hsErr)
			}
		}()
	}()
	_, port, _ = net.SplitHostPort(ln.Addr().String())
	return port, roots, ch
}

// dialLoopback 忽略 addr 里的主机名，一律连到本机 127.0.0.1，这样 TLS 层看到的主机名由 addr 决定。
func dialLoopback(ctx context.Context, network, addr string) (net.Conn, error) {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	var d net.Dialer
	return d.DialContext(ctx, "tcp4", net.JoinHostPort("127.0.0.1", port))
}

var wantExtensions = []uint16{0xff01, 0, 11, 10, 35, 22, 23, 13, 43, 45, 51, 16}

// negotiated 返回握手结果。uTLS 的 ConnectionState 是它自己的类型，不能断言成 tls.ConnectionState，
// 所以分两种 conn 取。
func negotiated(conn net.Conn) (version uint16, proto string) {
	switch c := conn.(type) {
	case *utls.UConn:
		s := c.ConnectionState()
		return s.Version, s.NegotiatedProtocol
	case *tls.Conn:
		s := c.ConnectionState()
		return s.Version, s.NegotiatedProtocol
	}
	return 0, ""
}

func TestHelloShapeAndTLS13(t *testing.T) {
	port, roots, captured := serveTLS(t, tls.VersionTLS13)
	dial := Wrap(dialLoopback, &tls.Config{RootCAs: roots}, 10*time.Second)
	if dial == nil {
		t.Fatal("Wrap returned nil, want a dialer (no proxy configured)")
	}
	conn, err := dial(context.Background(), "tcp", "localhost:"+port)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	defer conn.Close()
	version, proto := negotiated(conn)
	if version != tls.VersionTLS13 {
		t.Errorf("negotiated %#x, want TLS 1.3", version)
	}
	if proto != "http/1.1" {
		t.Errorf("ALPN = %q, want http/1.1", proto)
	}

	hello := <-captured
	t.Logf("JA3: %s", hello.ja3())
	t.Logf("ciphers: %s", joinU16(hello.ciphers))
	if got := hello.ciphers[:3]; got[0] != 0x1302 || got[1] != 0x1303 || got[2] != 0x1301 {
		t.Errorf("TLS 1.3 ciphers = %x, want 1302 1303 1301", got)
	}
	if fmt.Sprint(hello.extensions) != fmt.Sprint(wantExtensions) {
		t.Errorf("extensions = %v, want %v", hello.extensions, wantExtensions)
	}
	if len(hello.alpn) != 1 || hello.alpn[0] != "http/1.1" {
		t.Errorf("ALPN offered = %v, want [http/1.1]", hello.alpn)
	}
	if hello.serverName != "localhost" {
		t.Errorf("SNI = %q, want localhost", hello.serverName)
	}
	if fmt.Sprint(hello.versions) != fmt.Sprint([]uint16{0x0304, 0x0303}) {
		t.Errorf("supported_versions = %x, want 0304 0303", hello.versions)
	}
	if fmt.Sprint(hello.curves) != fmt.Sprint([]uint16{29, 23, 30, 25, 24}) {
		t.Errorf("supported_groups = %v, want x25519 secp256r1 x448 secp521r1 secp384r1", hello.curves)
	}
	for _, c := range hello.ciphers {
		if c&0x0f0f == 0x0a0a { // GREASE 形态的值，OpenSSL 不发
			t.Errorf("cipher %#04x looks like GREASE", c)
		}
	}
}

func TestHelloFallsBackToTLS12(t *testing.T) {
	port, roots, captured := serveTLS(t, tls.VersionTLS12)
	dial := Wrap(dialLoopback, &tls.Config{RootCAs: roots}, 10*time.Second)
	conn, err := dial(context.Background(), "tcp", "localhost:"+port)
	if err != nil {
		t.Fatalf("handshake against TLS 1.2 server: %v", err)
	}
	defer conn.Close()
	version, _ := negotiated(conn)
	if version != tls.VersionTLS12 {
		t.Errorf("negotiated %#x, want TLS 1.2", version)
	}
	if _, ok := conn.(*utls.UConn); !ok {
		t.Errorf("conn type = %T, want *utls.UConn", conn)
	}
	<-captured
}

func TestHelloKeepsCertificateVerification(t *testing.T) {
	port, _, _ := serveTLS(t, tls.VersionTLS13)
	dial := Wrap(dialLoopback, &tls.Config{RootCAs: x509.NewCertPool()}, 10*time.Second)
	if dial == nil {
		t.Fatal("Wrap returned nil")
	}
	_, err := dial(context.Background(), "tcp", "localhost:"+port)
	var unknown x509.UnknownAuthorityError
	if !errors.As(err, &unknown) {
		t.Fatalf("handshake with empty roots: err = %v, want x509.UnknownAuthorityError", err)
	}
}

func TestIPLiteralUsesCryptoTLS(t *testing.T) {
	port, roots, captured := serveTLS(t, tls.VersionTLS13)
	dial := Wrap(dialLoopback, &tls.Config{RootCAs: roots}, 10*time.Second)
	conn, err := dial(context.Background(), "tcp", "127.0.0.1:"+port)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	conn.Close()
	hello := <-captured
	if fmt.Sprint(hello.extensions) == fmt.Sprint(wantExtensions) {
		t.Errorf("IP literal got the OpenSSL-shaped hello; want crypto/tls fallback")
	}
}

func TestDirectAndWrapWithProxy(t *testing.T) {
	if !Direct() {
		t.Fatal("Direct() = false with no proxy env")
	}
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	if Direct() {
		t.Fatal("Direct() = true with HTTPS_PROXY set")
	}
	if wrap := Wrap(dialLoopback, &tls.Config{}, 10*time.Second); wrap != nil {
		t.Fatal("Wrap returned a dialer although a proxy is configured; want nil")
	}
}

// TestHandshakeTimeout 对一个只 accept 不回话的服务端握手，必须在给定时间内放弃。
// Transport 有自定义 TLS 拨号器时不再管 TLSHandshakeTimeout，这个上限只能由 Wrap 自己兜。
func TestHandshakeTimeout(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
		}
	}()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	dial := Wrap(dialLoopback, &tls.Config{RootCAs: x509.NewCertPool()}, 300*time.Millisecond)
	start := time.Now()
	_, err = dial(context.Background(), "tcp", "localhost:"+port)
	if err == nil {
		t.Fatal("handshake succeeded against a server that never answers")
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("handshake gave up after %v, want bounded by 300ms", took)
	}
}
