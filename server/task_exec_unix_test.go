//go:build !windows

package server

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func TestRunTaskCommandRunsOutsideAgentUnix(t *testing.T) {
	// 任务的父进程是包装它的 sh。外层 sh 已经退出，所以它的父进程是收养孤儿的那个
	// 进程（init，上层有子收割器时是收割器），不是本进程。
	adopter := orphanAdopter(t)
	command := strings.Join([]string{
		"ADOPTER=" + adopter,
		"for i in 1 2 3 4 5 6 7 8 9 10; do",
		`  parent=$(cut -d' ' -f4 /proc/$PPID/stat)`,
		`  [ "$parent" = "$ADOPTER" ] && break`,
		"  sleep 0.1",
		"done",
		`echo "parent=$parent ppid=$PPID self=$$"`,
	}, "\n")

	result, exitCode := runTaskCommand(command)

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with result %q", exitCode, result)
	}
	if !strings.HasPrefix(result, "parent="+adopter+" ") {
		t.Fatalf("task shell is not adopted by process %s: %q", adopter, result)
	}
	if strings.Contains(result, " self="+strconv.Itoa(os.Getpid())+" ") || strings.Contains(result, " ppid="+strconv.Itoa(os.Getpid())+" ") {
		t.Fatalf("task shell is still a child of this process: %q", result)
	}
}

// orphanAdopter 给出这个环境里收养孤儿的进程号：通常是 init，
// 上层有子收割器（subreaper）时是离得最近的那一个。
func orphanAdopter(t *testing.T) string {
	t.Helper()
	pidFile := filepath.Join(t.TempDir(), "pid")
	// sh 一退出，它放到后台的 sleep 就成了孤儿
	if err := exec.Command("sh", "-c", "sleep 30 & echo $! > "+pidFile).Run(); err != nil {
		t.Fatalf("starting an orphan: %v", err)
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	orphan, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("orphan pid %q: %v", data, err)
	}
	defer syscall.Kill(orphan, syscall.SIGKILL)
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(orphan) + "/stat")
	if err != nil {
		t.Fatal(err)
	}
	// pid (comm) 状态 父进程 …
	return strings.Fields(string(stat[bytes.LastIndexByte(stat, ')')+1:]))[1]
}
