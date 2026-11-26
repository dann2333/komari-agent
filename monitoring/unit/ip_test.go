package monitoring

import (
	"testing"
	"time"
)

func TestGetIPAddressLooksUpOncePerHour(t *testing.T) {
	oldV4, oldV6, oldTTL := lookupIPv4, lookupIPv6, publicIPTTL
	oldCache := publicIP
	oldFromNic, oldV4Flag, oldV6Flag := flags.GetIpAddrFromNic, flags.CustomIpv4, flags.CustomIpv6
	t.Cleanup(func() {
		lookupIPv4, lookupIPv6, publicIPTTL, publicIP = oldV4, oldV6, oldTTL, oldCache
		flags.GetIpAddrFromNic, flags.CustomIpv4, flags.CustomIpv6 = oldFromNic, oldV4Flag, oldV6Flag
	})
	flags.GetIpAddrFromNic, flags.CustomIpv4, flags.CustomIpv6 = false, "", ""
	publicIPTTL, publicIP = time.Hour, publicIPEntry{}

	lookups := 0
	lookupIPv4 = func() (string, error) { lookups++; return "203.0.113.7", nil }
	lookupIPv6 = func() (string, error) { lookups++; return "2001:db8::7", nil }

	// 两次上报只查一次，结果相同
	firstV4, firstV6, err := GetIPAddress()
	if err != nil || firstV4 != "203.0.113.7" || firstV6 != "2001:db8::7" {
		t.Fatalf("first lookup returned %q, %q, %v", firstV4, firstV6, err)
	}
	secondV4, secondV6, _ := GetIPAddress()
	if lookups != 2 {
		t.Fatalf("second report looked up again: %d lookups", lookups)
	}
	if secondV4 != firstV4 || secondV6 != firstV6 {
		t.Fatalf("cached result differs: %q, %q", secondV4, secondV6)
	}

	// 缓存过期后重查
	publicIPTTL = 0
	GetIPAddress()
	if lookups != 4 {
		t.Fatalf("expired cache was not refreshed: %d lookups", lookups)
	}

	// 网卡变了立刻重查
	publicIPTTL = time.Hour
	publicIP.interfaces = "stale"
	GetIPAddress()
	if lookups != 6 {
		t.Fatalf("interface change did not refresh: %d lookups", lookups)
	}

	// 自定义地址不查，另一族照常查
	lookupIPv4 = func() (string, error) { t.Fatal("custom IPv4 must not be looked up"); return "", nil }
	flags.CustomIpv4 = "198.51.100.1"
	publicIP.interfaces = "stale"
	v4, _, _ := GetIPAddress()
	if v4 != "198.51.100.1" {
		t.Fatalf("custom IPv4 not used: %q", v4)
	}
	if lookups != 7 {
		t.Fatalf("IPv6 should still be looked up once, lookups=%d", lookups)
	}
}

func TestGetIPAddressDoesNotCacheFailure(t *testing.T) {
	oldV4, oldV6, oldTTL := lookupIPv4, lookupIPv6, publicIPTTL
	oldCache := publicIP
	oldFromNic, oldV4Flag, oldV6Flag := flags.GetIpAddrFromNic, flags.CustomIpv4, flags.CustomIpv6
	t.Cleanup(func() {
		lookupIPv4, lookupIPv6, publicIPTTL, publicIP = oldV4, oldV6, oldTTL, oldCache
		flags.GetIpAddrFromNic, flags.CustomIpv4, flags.CustomIpv6 = oldFromNic, oldV4Flag, oldV6Flag
	})
	flags.GetIpAddrFromNic, flags.CustomIpv4, flags.CustomIpv6 = false, "", ""
	publicIPTTL, publicIP = time.Hour, publicIPEntry{}

	lookups := 0
	lookupIPv4 = func() (string, error) { lookups++; return "", nil }
	lookupIPv6 = func() (string, error) { lookups++; return "", nil }

	v4, v6, _ := GetIPAddress()
	if v4 != "" || v6 != "" {
		t.Fatalf("failed lookup returned %q, %q", v4, v6)
	}
	GetIPAddress()
	if lookups != 4 {
		t.Fatalf("a failed lookup was cached: %d lookups", lookups)
	}
}

func TestInterfaceFingerprintStable(t *testing.T) {
	first := interfaceFingerprint()
	if first == "" {
		t.Fatal("interface fingerprint is empty")
	}
	if second := interfaceFingerprint(); second != first {
		t.Fatalf("fingerprint changed without any network change:\n%s\n%s", first, second)
	}
}
