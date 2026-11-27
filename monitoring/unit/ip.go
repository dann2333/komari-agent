package monitoring

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	pkg_flags "github.com/komari-monitor/komari-agent/cmd/flags"
	"github.com/komari-monitor/komari-agent/dnsresolver"
	"github.com/komari-monitor/komari-agent/internal/tlsfp"
	"github.com/komari-monitor/komari-agent/internal/trustroots"
	"github.com/komari-monitor/komari-agent/internal/ua"
)

// dialV4 / dialV6 锁死地址族：公网 IP 探测必须分别从 v4 / v6 出口出去。
var dialV4 = func(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := dnsresolver.GetNetDialer(15 * time.Second)
	return dialer.DialContext(ctx, "tcp4", addr)
}
var dialV6 = func(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := dnsresolver.GetNetDialer(15 * time.Second)
	return dialer.DialContext(ctx, "tcp6", addr)
}

var (
	// 创建适用于IPv4和IPv6的HTTP客户端
	ipv4HTTPClient = &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           dialV4,
			DialTLSContext:        tlsfp.Wrap(dialV4, trustroots.Config(false), 10*time.Second),
			MaxIdleConns:          10,
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			TLSClientConfig:       trustroots.Config(false),
		},
		Timeout: 15 * time.Second,
	}
	ipv6HTTPClient = &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           dialV6,
			DialTLSContext:        tlsfp.Wrap(dialV6, trustroots.Config(false), 10*time.Second),
			MaxIdleConns:          10,
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			TLSClientConfig:       trustroots.Config(false),
		},
		Timeout: 15 * time.Second,
	}
	userAgent = ua.UserAgent
)

func GetIPv4Address() (string, error) {

	webAPIs := []string{
		"https://www.visa.cn/cdn-cgi/trace",
		"https://www.qualcomm.cn/cdn-cgi/trace",
		"https://www.toutiao.com/stream/widget/local_weather/data/",
		"https://edge-ip.html.zone/geo",
		"https://vercel-ip.html.zone/geo",
		"http://ipv4.ip.sb",
		"https://api.ipify.org?format=json",
	}

	for _, api := range webAPIs {
		// get ipv4
		req, err := http.NewRequest("GET", api, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", userAgent)
		resp, err := ipv4HTTPClient.Do(req)
		if err != nil {
			continue
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close() // 获取后立即关闭防止堵塞
		if err != nil {
			continue
		}
		re := regexp.MustCompile(`\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}`)
		ipv4 := re.FindString(string(body))
		if ipv4 != "" {
			pkg_flags.LogStatusf("Get IPV4 Success: %s", ipv4)
			return ipv4, nil
		}
	}
	return "", nil
}

func GetIPv6Address() (string, error) {

	webAPIs := []string{
		"https://v6.ip.zxinc.org/info.php?type=json",
		"https://api6.ipify.org?format=json",
		"https://ipv6.icanhazip.com",
		"http://api-ipv6.ip.sb/geoip",
	}

	for _, api := range webAPIs {
		// get ipv6
		req, err := http.NewRequest("GET", api, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", userAgent)
		resp, err := ipv6HTTPClient.Do(req)
		if err != nil {
			continue
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close() // 获取后立即关闭防止堵塞
		if err != nil {
			continue
		}

		// 使用正则表达式从响应体中提取IPv6地址
		re := regexp.MustCompile(`(([0-9A-Fa-f]{1,4}:){7})([0-9A-Fa-f]{1,4})|(([0-9A-Fa-f]{1,4}:){1,6}:)(([0-9A-Fa-f]{1,4}:){0,4})([0-9A-Fa-f]{0,4})`)
		ipv6 := re.FindString(string(body))
		if ipv6 != "" {
			pkg_flags.LogStatusf("Get IPV6 Success:  %s", ipv6)
			return ipv6, nil
		}
	}
	return "", nil
}

func GetIPAddress() (ipv4, ipv6 string, err error) {

	if flags.GetIpAddrFromNic {
		allowNics, err := InterfaceList()
		if err != nil {
			log.Printf("Get Interface List Error: %v", err)
		} else {
			ipv4, ipv6 = getIPFromInterfaces(allowNics)
			if ipv4 != "" || ipv6 != "" {
				pkg_flags.LogStatusf("Get IP from NIC - IPv4: %s, IPv6: %s", ipv4, ipv6)
				return ipv4, ipv6, nil
			}
		}
	}

	// 公网地址不用每次上报都去问第三方接口：查到后缓存 publicIPTTL，网卡变了才重查。
	// 查不到不缓存，下次上报接着查。
	publicIPMu.Lock()
	defer publicIPMu.Unlock()
	interfaces := interfaceFingerprint()
	if age := time.Since(publicIP.at); publicIP.ipv4 != "" && publicIP.interfaces == interfaces && age >= 0 && age < publicIPTTL {
		return publicIP.ipv4, publicIP.ipv6, nil
	}
	ipv4, ipv6 = flags.CustomIpv4, flags.CustomIpv6
	if ipv4 == "" {
		if ipv4, err = lookupIPv4(); err != nil {
			log.Printf("Get IPV4 Error: %v", err)
			ipv4 = ""
		}
	}
	if ipv6 == "" {
		if ipv6, err = lookupIPv6(); err != nil {
			log.Printf("Get IPV6 Error: %v", err)
			ipv6 = ""
		}
	}
	publicIP = publicIPEntry{at: time.Now(), interfaces: interfaces, ipv4: ipv4, ipv6: ipv6}

	return ipv4, ipv6, nil
}

type publicIPEntry struct {
	at         time.Time
	interfaces string
	ipv4       string
	ipv6       string
}

var (
	publicIPTTL = time.Hour
	publicIPMu  sync.Mutex
	publicIP    publicIPEntry

	// 测试里替换，用来数查了几次
	lookupIPv4 = GetIPv4Address
	lookupIPv6 = GetIPv6Address
)

// interfaceFingerprint 把每张网卡的名字、状态和地址拼起来，任何一张网卡变了它就变。
func interfaceFingerprint() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	lines := make([]string, 0, 2*len(interfaces))
	for _, iface := range interfaces {
		lines = append(lines, iface.Name+" "+iface.Flags.String())
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			lines = append(lines, iface.Name+" "+addr.String())
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// getIPFromInterfaces 从指定的网卡接口获取 IPv4 和 IPv6 地址
func getIPFromInterfaces(nicNames []string) (ipv4, ipv6 string) {
	interfaces, err := net.Interfaces()
	if err != nil {
		log.Printf("Failed to get network interfaces: %v", err)
		return "", ""
	}
	for _, iface := range interfaces {
		// 检查接口是否在允许列表中
		if !func(slice []string, item string) bool {
			for _, s := range slice {
				if s == item {
					return true
				}
			}
			return false
		}(nicNames, iface.Name) {
			continue
		}

		// 跳过未启动的接口
		if iface.Flags&net.FlagUp == 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}

			if ip == nil || ip.IsLoopback() {
				continue
			}

			// 获取 IPv4 地址
			if ipv4 == "" && ip.To4() != nil {
				ipv4 = ip.String()
			}

			// 获取 IPv6 地址（排除链路本地地址）
			if ipv6 == "" && ip.To4() == nil && !ip.IsLinkLocalUnicast() {
				ipv6 = ip.String()
			}

			// 如果已经找到 IPv4 和 IPv6,提前返回
			if ipv4 != "" && ipv6 != "" {
				return ipv4, ipv6
			}
		}
	}

	return ipv4, ipv6
}
