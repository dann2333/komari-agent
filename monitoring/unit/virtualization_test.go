package monitoring

import (
	"testing"
)

func TestVirtualized(t *testing.T) {
	virt := Virtualized()
	cpuid_result := detectByCPUID()
	container_result := detectContainer()
	t.Logf("Virtualization type: %s", virt)
	t.Logf("CPUID result: %s", cpuid_result)
	t.Logf("Container result: %s", container_result)
}

// TestVirtualizedProbesOnce 上报基本信息时每次都会调用 Virtualized，
// 探测只能做一次，结果每次都得一样。
func TestVirtualizedProbesOnce(t *testing.T) {
	before := virtualizedProbes
	results := make([]string, 3)
	for i := range results {
		results[i] = Virtualized()
	}
	if probed := virtualizedProbes - before; probed > 1 {
		t.Fatalf("Virtualized() probed %d times, want at most 1", probed)
	}
	for _, r := range results {
		if r != results[0] {
			t.Fatalf("result changed between calls: %q then %q", results[0], r)
		}
	}
	if results[0] == "" {
		t.Fatal("Virtualized() returned an empty string")
	}
}
