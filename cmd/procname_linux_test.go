//go:build linux

package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestProcessNameComm 套上进程名后，/proc/self/comm 里只能是名字本身。
// 带着换行写进去，新内核会把它算进名字：ps -e 显示成 "rsyslogd?"，
// /proc/<pid>/status 的 Name 显示成 "rsyslogd\n"。
// 伪装会改写 os.Args 指向的内存，所以放在子进程里做，不影响别的测试。
func TestProcessNameComm(t *testing.T) {
	const life = "KOMARI_TEST_COMM_LIFE"
	if os.Getenv(life) == "1" {
		// 容器等环境可能不许写 /proc/self/comm，那就验不了
		if err := os.WriteFile("/proc/self/comm", []byte("probe"), 0); err != nil {
			fmt.Println("comm-unwritable")
			return
		}
		applyProcessName("rsyslogd")
		fmt.Printf("comm=%q\n", mustReadComm(t))
		// 超过 15 字节的名字只留前 15 个
		applyProcessName("averylongprocessname")
		fmt.Printf("comm=%q\n", mustReadComm(t))
		return
	}

	self, err := os.Executable()
	if err != nil {
		t.Fatalf("locate the test binary: %v", err)
	}
	cmd := exec.Command(self, "-test.run=^TestProcessNameComm$")
	cmd.Env = append(os.Environ(), life+"=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper exited with %v\n%s", err, out)
	}
	if strings.Contains(string(out), "comm-unwritable") {
		t.Skip("/proc/self/comm is not writable here")
	}
	for _, want := range []string{`comm="rsyslogd\n"`, `comm="averylongproces\n"`} {
		if !strings.Contains(string(out), want+"\n") {
			t.Fatalf("missing %s in the child's output:\n%s", want, out)
		}
	}
}

func mustReadComm(t *testing.T) []byte {
	t.Helper()
	comm, err := os.ReadFile("/proc/self/comm")
	if err != nil {
		t.Fatalf("read /proc/self/comm: %v", err)
	}
	return comm
}
