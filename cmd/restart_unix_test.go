//go:build unix

package cmd

import (
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const (
	// restartLifeEnv 区分进程映像被替换前后的两次运行：1 是替换前，2 是替换后。
	restartLifeEnv = "KOMARI_TEST_RESTART_LIFE"
	// restartParentEnv 记下启动子进程的测试进程 PID，替换后父进程应当还是它。
	restartParentEnv = "KOMARI_TEST_RESTART_PARENT"
)

// 拿不到 /proc/self/exe 时只能用带路径的 argv[0]；裸名字经 PATH 可能找到别的程序。
func TestResolveSelfPath(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("locate the test binary: %v", err)
	}
	noProc := errors.New("readlink /proc/self/exe: no such file")
	for _, tt := range []struct {
		name, exe, argv0, want string
		exeErr                 error
	}{
		{"the running executable wins", "/opt/agent/rsyslogd", "anything", "/opt/agent/rsyslogd", nil},
		{"falls back to an argv0 that carries a path", "", self, self, noProc},
		{"never looks a bare name up in PATH", "", "rsyslogd", "", noProc},
		{"empty argv0", "", "", "", noProc},
	} {
		if got := resolveSelfPath(tt.exe, tt.exeErr, tt.argv0); got != tt.want {
			t.Errorf("%s: resolveSelfPath() = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// TestRestartInPlace 在子进程里先套上进程名伪装，再调用 restartInPlace：
// 同一个测试会带着"2"、带着原来的参数再跑一遍，PID 和父进程都必须和替换前一致，
// 证明是原地替换而不是新起了一个进程，也没有被伪装改坏的参数。
func TestRestartInPlace(t *testing.T) {
	switch os.Getenv(restartLifeEnv) {
	case "":
	case "1":
		applyProcessName("rsyslogd")
		t.Logf("life=1 pid=%d argv0=%q", os.Getpid(), os.Args[0])
		if err := os.Setenv(restartLifeEnv, "2"); err != nil {
			t.Fatal(err)
		}
		if err := restartInPlace(); err != nil {
			t.Fatalf("restartInPlace() = %v, want the process to be replaced", err)
		}
		return
	default:
		t.Logf("life=2 pid=%d", os.Getpid())
		parent, _ := strconv.Atoi(os.Getenv(restartParentEnv))
		if parent == 0 || os.Getppid() != parent {
			t.Fatalf("ppid = %d after the replacement, want %d", os.Getppid(), parent)
		}
		if got := os.Getenv(restartLifeEnv); got != "2" {
			t.Fatalf("life = %q after the replacement, want 2", got)
		}
		return
	}

	self, err := os.Executable()
	if err != nil {
		t.Fatalf("locate the test binary: %v", err)
	}
	cmd := exec.Command(self, "-test.run=^TestRestartInPlace$", "-test.v")
	cmd.Env = append(os.Environ(), restartLifeEnv+"=1", restartParentEnv+"="+strconv.Itoa(os.Getpid()))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper exited with %v\n%s", err, out)
	}

	// The command line only survives because it was copied before the mask.
	if !strings.Contains(string(out), `argv0="rsyslogd\x00`) {
		t.Fatalf("the mask was not applied before the replacement:\n%s", out)
	}
	lives := regexp.MustCompile(`life=([12]) pid=(\d+)`).FindAllStringSubmatch(string(out), -1)
	if len(lives) != 2 || lives[0][1] != "1" || lives[1][1] != "2" {
		t.Fatalf("expected the test to run once before and once after the replacement:\n%s", out)
	}
	if lives[0][2] != lives[1][2] {
		t.Fatalf("pid changed across the replacement: %s -> %s", lives[0][2], lives[1][2])
	}
}
