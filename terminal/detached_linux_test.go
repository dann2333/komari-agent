//go:build linux

package terminal

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

// 测试程序自己就是那个被重新执行的副本：带着 spawnEnv 启动时只起子进程，不跑测试。
func TestMain(m *testing.M) {
	if handled, code := RunSpawnHelper(); handled {
		os.Exit(code)
	}
	os.Exit(m.Run())
}

// newDetachedShell 起一个脱离本进程的交互式 shell，测试结束后关掉。
func newDetachedShell(t *testing.T, shell string) (*os.File, *unixTerminal) {
	t.Helper()
	master, pid, err := startDetachedLinux(shell, append(os.Environ(), "PS1=det> "))
	if err != nil {
		t.Fatalf("detached shell did not start: %v", err)
	}
	_ = pty.Setsize(master, &pty.Winsize{Rows: 24, Cols: 80})
	term := &unixTerminal{tty: master, pid: pid}
	t.Cleanup(func() {
		_, _ = master.Write([]byte("exit\n"))
		started := time.Now()
		if err := term.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
		if took := time.Since(started); took > 3*time.Second {
			t.Errorf("Close took %s", took)
		}
	})
	return master, term
}

// slaveName 给出 pty 主设备对应的从机路径。
func slaveName(t *testing.T, master *os.File) string {
	t.Helper()
	n, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("/dev/pts/%d", n)
}

// readUntil 读到 marker 出现为止，5 秒读不到就带着已有输出报错。
func readUntil(t *testing.T, tty *os.File, marker string) string {
	t.Helper()
	var got []byte
	buf := make([]byte, 4096)
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(string(got), marker) {
		_ = tty.SetReadDeadline(deadline)
		n, err := tty.Read(buf)
		got = append(got, buf[:n]...)
		if err == nil {
			continue
		}
		t.Fatalf("read ended with %v while waiting for %q; output so far:\n%s", err, marker, got)
	}
	return string(got)
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

func TestDetachedShellIsAdoptedAndOwnsTheTerminal(t *testing.T) {
	master, term := newDetachedShell(t, "/bin/sh")
	slave := slaveName(t, master)

	// /proc/<pid>/stat：pid (comm) 状态 父进程 进程组 会话 …
	_, _ = master.Write([]byte("echo \"ppid=$(cut -d' ' -f4 /proc/$$/stat) pgrp=$(cut -d' ' -f5 /proc/$$/stat) sid=$(cut -d' ' -f6 /proc/$$/stat) pid=$$ tty=$(tty)\"\n"))
	output := readUntil(t, master, "tty=/dev/")

	match := regexp.MustCompile(`ppid=(\d+) pgrp=(\d+) sid=(\d+) pid=(\d+) tty=(\S+)`).FindStringSubmatch(output)
	if match == nil {
		t.Fatalf("shell did not report its process state:\n%s", output)
	}
	ppid, pgrp, sid, pid, ttyName := match[1], match[2], match[3], match[4], strings.TrimSuffix(match[5], "\r")
	if want := orphanAdopter(t); ppid != want {
		t.Errorf("shell's parent is process %s, expected the orphan adopter %s", ppid, want)
	}
	if pid != strconv.Itoa(term.pid) {
		t.Errorf("shell pid is %s, expected %d", pid, term.pid)
	}
	if sid != pid || pgrp != pid {
		t.Errorf("shell should lead its own session and process group: pgrp=%s sid=%s pid=%s", pgrp, sid, pid)
	}
	if ttyName != slave {
		t.Errorf("controlling terminal is %s, expected %s", ttyName, slave)
	}
}

func TestDetachedShellHasOnlyTheTerminalOpen(t *testing.T) {
	master, _ := newDetachedShell(t, "/bin/sh")
	slave := slaveName(t, master)

	_, _ = master.Write([]byte("ls -l /proc/$$/fd | awk '{print $9, $10, $11}'\n"))
	output := readUntil(t, master, "2 -> ")

	var fds []string
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, " -> ") && !strings.Contains(line, "awk") {
			fds = append(fds, strings.TrimSpace(line))
		}
	}
	if len(fds) == 0 {
		t.Fatalf("no descriptors reported:\n%s", output)
	}
	for _, fd := range fds {
		_, target, _ := strings.Cut(fd, "-> ")
		target = strings.TrimSpace(target)
		// 交互式 shell 会另开一个 /dev/tty 来读键盘，其它描述符都应该指向这台终端
		if target != slave && target != "/dev/tty" {
			t.Errorf("shell holds a descriptor that is not the terminal: %s", fd)
		}
	}
}

func TestDetachedShellJobControl(t *testing.T) {
	shell := "/bin/sh"
	if _, err := os.Stat("/bin/bash"); err == nil {
		shell = "/bin/bash"
	}
	master, term := newDetachedShell(t, shell)
	if ignoresSIGINT(term.pid) {
		t.Error("shell ignores SIGINT, Ctrl-C would not work")
	}

	// 前台作业收到 Ctrl-C，shell 拿到 128+SIGINT
	_, _ = master.Write([]byte("sleep 30\n"))
	time.Sleep(500 * time.Millisecond)
	_, _ = master.Write([]byte{3})
	_, _ = master.Write([]byte("echo \"done=$?\"\n"))
	readUntil(t, master, "done=130")

	// Ctrl-Z 停住前台作业，fg 恢复，再 Ctrl-C 结束它
	_, _ = master.Write([]byte("sleep 31\n"))
	time.Sleep(500 * time.Millisecond)
	_, _ = master.Write([]byte{26})
	readUntil(t, master, "Stopped")
	_, _ = master.Write([]byte("fg\n"))
	time.Sleep(500 * time.Millisecond)
	_, _ = master.Write([]byte{3})
	_, _ = master.Write([]byte("echo \"resumed=$?\"\n"))
	readUntil(t, master, "resumed=130")
}

func TestDetachedShellResize(t *testing.T) {
	master, term := newDetachedShell(t, "/bin/sh")

	_, _ = master.Write([]byte("echo \"size1=$(stty size)\"\n"))
	readUntil(t, master, "size1=24 80")

	if err := term.Resize(100, 40); err != nil {
		t.Fatalf("resize failed: %v", err)
	}
	_, _ = master.Write([]byte("echo \"size2=$(stty size)\"\n"))
	readUntil(t, master, "size2=40 100")
}

func TestDetachedShellExitEndsTheReaderAndWait(t *testing.T) {
	master, term := newDetachedShell(t, "/bin/sh")

	_, _ = master.Write([]byte("exit\n"))

	waited := make(chan error, 1)
	go func() { waited <- term.Wait() }()
	select {
	case err := <-waited:
		if err != nil {
			t.Fatalf("Wait: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not return after the shell exited")
	}

	_ = master.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 4096)
	for {
		_, err := master.Read(buf)
		if err == nil {
			continue
		}
		if os.IsTimeout(err) {
			t.Fatal("reader still waits after the shell exited")
		}
		break
	}
	if dev, _ := ttyDevice(slaveName(t, master)); shellOwnsTty(term.pid, dev) {
		t.Error("shell still owns the terminal after exit")
	}
}

func TestNewTerminalImplDetachesShellFromAgent(t *testing.T) {
	impl, err := newTerminalImpl()
	if err != nil {
		t.Skipf("no shell available: %v", err)
	}
	t.Cleanup(func() {
		_, _ = impl.term.Write([]byte("exit\n"))
		_ = impl.term.Close()
	})
	term := impl.term.(*unixTerminal)
	if term.pid == 0 {
		t.Fatalf("shell was started as a child process (pid=%d)", term.pid)
	}
	_, _ = impl.term.Write([]byte("echo \"marker=$$ hist=$HISTFILE\"\n"))
	readUntil(t, term.tty, "marker="+strconv.Itoa(term.pid)+" hist=/dev/null")
}

func TestNewTerminalImplFallsBackToDirectStart(t *testing.T) {
	// 脱离不了的时候，终端要照旧能起来，只是 shell 回到 agent 名下
	original := startDetached
	startDetached = func(string, []string) (*os.File, int, error) { return nil, 0, errors.New("cannot detach") }
	t.Cleanup(func() { startDetached = original })

	impl, err := newTerminalImpl()
	if err != nil {
		t.Fatalf("terminal did not start: %v", err)
	}
	t.Cleanup(func() {
		_, _ = impl.term.Write([]byte("exit\n"))
		_ = impl.term.Close()
	})
	term := impl.term.(*unixTerminal)
	if term.pid != 0 {
		t.Fatalf("expected a directly started shell, got detached pid %d", term.pid)
	}
	_, _ = impl.term.Write([]byte("echo \"marker=$$\"\n"))
	readUntil(t, term.tty, "marker="+strconv.Itoa(term.cmd.Process.Pid))
}

func TestDetachedStartWorksWithPtyNumbersAbove255(t *testing.T) {
	// 占住前面的 pty，让这次的从机编号越过 255：那里的设备号编码和 256 以下不一样，
	// 核对启动的算法必须两种都认得
	var held []*os.File
	defer func() {
		for _, f := range held {
			_ = f.Close()
		}
	}()
	for high := false; !high; {
		master, slave, err := pty.Open()
		if err != nil {
			t.Skipf("no pty numbered above 255 available here: %v", err)
		}
		held = append(held, master, slave)
		n, _ := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
		high = n >= 256
	}

	master, term := newDetachedShell(t, "/bin/sh")
	n, _ := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if n < 256 {
		t.Skipf("got pty %d, the high numbers were not reached", n)
	}
	state, session, tty, err := procStatTty(term.pid)
	if dev, _ := ttyDevice(slaveName(t, master)); err != nil || state == 'Z' || session != term.pid || tty != dev {
		t.Fatalf("shell does not own pty %d: state=%c session=%d tty=%d dev=%d err=%v", n, state, session, tty, dev, err)
	}
	_, _ = master.Write([]byte("echo \"high=$$\"\n"))
	readUntil(t, master, "high="+strconv.Itoa(term.pid))
}

func TestDetachedReadyRejectsProcessesWithoutTheTerminal(t *testing.T) {
	master, term := newDetachedShell(t, "/bin/sh")
	dev, _ := ttyDevice(slaveName(t, master))
	if !detachedReady(term.pid, dev) {
		t.Error("the detached shell should be accepted")
	}

	// 别的设备号：不是这台终端的会话首进程
	detachedStartTimeout = 300 * time.Millisecond
	if detachedReady(term.pid, dev+1) {
		t.Error("a shell controlling another terminal was accepted")
	}

	// 普通子进程：不是会话首进程
	plain := exec.Command("sleep", "30")
	if err := plain.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = plain.Process.Kill()
		_, _ = plain.Process.Wait()
	}()
	if detachedReady(plain.Process.Pid, dev) {
		t.Error("a process that is not a session leader was accepted")
	}

	// 已经退出的进程号
	done := exec.Command("true")
	if err := done.Run(); err != nil {
		t.Fatal(err)
	}
	if detachedReady(done.ProcessState.Pid(), dev) {
		t.Error("an exited process was accepted")
	}
}

func TestRunSpawnHelperOnlyReactsToItsVariable(t *testing.T) {
	t.Setenv(spawnEnv, "")
	if handled, _ := RunSpawnHelper(); handled {
		t.Fatal("helper mode entered without a request")
	}

	t.Setenv(spawnEnv, "not json")
	if handled, code := RunSpawnHelper(); !handled || code == 0 {
		t.Fatalf("helper mode with a broken request: handled=%v code=%d", handled, code)
	}
	if os.Getenv(spawnEnv) != "" {
		t.Error("the request must be dropped so that child processes do not see it")
	}
}
