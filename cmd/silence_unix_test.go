//go:build unix

package cmd

import (
	"log"
	"os"
	"os/exec"
	"strings"
	"testing"
)

const (
	// silenced: 先静默再输出; control: 不静默
	silenceHelperEnv = "KOMARI_TEST_SILENCE_HELPER"
	// 1: 只制造输出，不再派生子进程
	silenceNoiseEnv = "KOMARI_TEST_SILENCE_NOISE"
)

// TestSilenceOutput 只能在子进程里验证：silenceOutput 改的是整个进程的描述符，
// 在测试进程里直接调会把 testing 自己的输出一起丢掉。
func TestSilenceOutput(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("locate the test binary: %v", err)
	}
	run := func(mode string) string {
		cmd := exec.Command(self, "-test.run=^TestSilenceOutputHelper$")
		cmd.Env = append(os.Environ(), silenceHelperEnv+"="+mode)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: helper exited with %v\n%s", mode, err, out)
		}
		return string(out)
	}
	if control := run("control"); !strings.Contains(control, "noise via log") {
		t.Fatalf("control run shows no output, so the check below would prove nothing:\n%q", control)
	}
	if got := run("silenced"); got != "" {
		t.Fatalf("silenced helper still wrote to stdout/stderr:\n%s", got)
	}
}

func TestSilenceOutputHelper(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("locate the test binary: %v", err)
	}
	if os.Getenv(silenceNoiseEnv) == "1" {
		printNoise()
		return
	}
	switch os.Getenv(silenceHelperEnv) {
	case "silenced":
		silenceOutput()
		printNoise()
		spawnNoiseChild(self)
	case "control":
		printNoise()
		spawnNoiseChild(self)
	}
}

// printNoise 按运行期代码会用的每种写法各写一次。
func printNoise() {
	log.Print("noise via log")
	os.Stdout.WriteString("noise via os.Stdout\n")
	os.Stderr.WriteString("noise via os.Stderr\n")
}

// spawnNoiseChild 启一个继承了 1、2 号描述符的子进程。
func spawnNoiseChild(self string) {
	child := exec.Command(self, "-test.run=^TestSilenceOutputHelper$")
	child.Env = append(os.Environ(), silenceNoiseEnv+"=1")
	_ = child.Run()
}
