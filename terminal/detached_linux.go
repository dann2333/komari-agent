//go:build linux

package terminal

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

func init() {
	startDetached = startDetachedLinux
	ttyDevice = ttyDeviceNumber
	detachedAlive = shellOwnsTty
}

var detachedStartTimeout = 5 * time.Second

// spawnEnv 是 agent 重新执行自己时带上的环境变量。带着它的那次不当 agent 跑，而是起一个
// 子进程、报出子进程的 pid 就退出（RunSpawnHelper）。
// 走环境变量而不是命令行参数：/proc/<pid>/cmdline 和审计里的 execve 只看得到和 agent
// 自己一样的启动命令。
const spawnEnv = "AGENT_SPAWN"

// spawnRequest 经 spawnEnv 交给那个短命的副本。
type spawnRequest struct {
	Argv []string `json:"argv"`
	Env  []string `json:"env"`
	// NewSession 让子进程成为新会话的首进程，并把 3 号描述符那台终端接成它的控制终端
	NewSession bool `json:"new_session"`
}

// RunSpawnHelper 在 agent 进程一启动时调用（cmd.Execute）。带着 spawnEnv 的那一次返回
// true：它已经按请求起了子进程，把子进程的 pid 写到了 6 号描述符，调用方直接退出。
// 3、4、5 号描述符成为子进程的标准输入、输出和错误。
func RunSpawnHelper() (bool, int) {
	raw := os.Getenv(spawnEnv)
	if raw == "" {
		return false, 0
	}
	_ = os.Unsetenv(spawnEnv)
	var req spawnRequest
	if json.Unmarshal([]byte(raw), &req) != nil || len(req.Argv) == 0 {
		return true, 1
	}
	child := exec.Command(req.Argv[0], req.Argv[1:]...)
	child.Env = req.Env
	tty := os.NewFile(3, "spawn-tty")
	child.Stdin, child.Stdout, child.Stderr = tty, tty, tty
	// 这几个描述符只给副本自己用：不设成 exec 时关闭，会跟着漏进 shell
	for fd := 3; fd <= 6; fd++ {
		unix.CloseOnExec(fd)
	}
	if req.NewSession {
		child.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	}
	if err := child.Start(); err != nil {
		return true, 1
	}
	_, _ = fmt.Fprintf(os.NewFile(6, "spawn-status"), "%d\n", child.Process.Pid)
	return true, 0
}

// startDetachedLinux 让 shell 不挂在 agent 名下：
//
//	agent ──▶ 自己的副本（起完 shell 就退出）
//	            └──▶ sh -c … ──▶ exec shell    副本退出后被 init 收养，PPID 不再是 agent
//
// shell 是副本用 Go 起的直接子进程，和 pty.Start 起出来的完全一样：新会话的首进程、
// 持有控制终端、信号处置是默认值。这三样缺一个终端就不正常：
//   - 长活的 shell 必须自己当会话首进程：首进程一退出，内核就给控制终端的前台进程组
//     发 SIGHUP；
//   - 不能靠 shell 的 "&" 来当中间那一层：非交互 shell 起的后台命令带着"忽略
//     SIGINT/SIGQUIT"的处置，shell 改不回来，Ctrl-C 就失效了。
//
// 成不成功看 /proc/<pid>/stat：会话号等于自己的 pid、控制终端就是这台 pty、没有忽略
// SIGINT。任何一步不对都返回错误，调用方回退到 pty.Start。
func startDetachedLinux(shell string, env []string) (*os.File, int, error) {
	if os.Getpid() == 1 {
		// agent 自己是 PID 1（容器里）时，孤儿还是被它收养，等于没脱离
		return nil, 0, errors.New("agent is PID 1")
	}
	self, err := os.Executable()
	if err != nil || strings.HasSuffix(self, " (deleted)") {
		return nil, 0, fmt.Errorf("executable path is unavailable: %v", err)
	}
	master, slave, err := pty.Open()
	if err != nil {
		return nil, 0, err
	}
	defer slave.Close()
	dev, err := ttyDeviceNumber(slave.Name())
	if err != nil {
		_ = master.Close()
		return nil, 0, err
	}
	statusR, statusW, err := os.Pipe()
	if err != nil {
		_ = master.Close()
		return nil, 0, err
	}
	defer statusR.Close()

	request, err := json.Marshal(spawnRequest{
		Argv:       []string{"/bin/sh", "-c", motdShellPrelude, "sh", shell},
		Env:        env,
		NewSession: true,
	})
	if err != nil {
		_ = master.Close()
		return nil, 0, err
	}
	helper := exec.Command(self)
	helper.Args[0] = filepath.Base(self) // 命令行里只出现名字，和 agent 自己一样
	helper.Env = append(os.Environ(), spawnEnv+"="+string(request))
	helper.ExtraFiles = []*os.File{slave, slave, slave, statusW}
	if err := helper.Start(); err != nil {
		_ = statusW.Close()
		_ = master.Close()
		return nil, 0, err
	}
	_ = statusW.Close()
	go func() { _ = helper.Wait() }()

	pid := make(chan int, 1)
	go func() {
		line, err := bufio.NewReader(statusR).ReadString('\n')
		n, convErr := strconv.Atoi(strings.TrimSpace(line))
		if err != nil || convErr != nil || n <= 1 {
			n = 0
		}
		pid <- n
	}()
	var shellPID int
	select {
	case shellPID = <-pid:
	case <-time.After(detachedStartTimeout):
	}
	if shellPID == 0 || !detachedReady(shellPID, dev) {
		if shellPID > 0 {
			_ = syscall.Kill(shellPID, syscall.SIGKILL)
			_ = syscall.Kill(-shellPID, syscall.SIGKILL)
		}
		_ = helper.Process.Kill()
		_ = master.Close()
		return nil, 0, fmt.Errorf("shell did not take over the terminal")
	}
	return master, shellPID, nil
}

// detachedReady 等 shell 接管终端；它先退出了就不等了。
func detachedReady(pid int, dev uint64) bool {
	for deadline := time.Now().Add(detachedStartTimeout); !shellOwnsTty(pid, dev) || ignoresSIGINT(pid); time.Sleep(10 * time.Millisecond) {
		if syscall.Kill(pid, 0) == syscall.ESRCH || time.Now().After(deadline) {
			return false
		}
	}
	return true
}

// shellOwnsTty 报告 pid 是否是活着的会话首进程，并且控制终端就是 dev。
func shellOwnsTty(pid int, dev uint64) bool {
	state, session, tty, err := procStatTty(pid)
	return err == nil && state != 'Z' && session == pid && tty == dev
}

// procStatTty 从 /proc/<pid>/stat 取进程状态、会话号和控制终端的设备号。
func procStatTty(pid int) (state byte, session int, tty uint64, err error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, 0, 0, err
	}
	// comm 带括号，里面还可能有空格，从最后一个右括号往后数：
	// 状态、父进程、进程组、会话、控制终端
	fields := strings.Fields(string(data[bytes.LastIndexByte(data, ')')+1:]))
	if len(fields) < 5 {
		return 0, 0, 0, fmt.Errorf("short /proc/%d/stat", pid)
	}
	if session, err = strconv.Atoi(fields[3]); err != nil {
		return 0, 0, 0, err
	}
	tty, err = strconv.ParseUint(fields[4], 10, 64)
	return fields[0][0], session, tty, err
}

// ignoresSIGINT 报告进程是否带着"忽略 SIGINT"的处置在跑（/proc/<pid>/status 的 SigIgn）。
func ignoresSIGINT(pid int) bool {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "SigIgn:") {
			mask, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "SigIgn:")), 16, 64)
			return err == nil && mask&(1<<(syscall.SIGINT-1)) != 0
		}
	}
	return false
}

// ttyDeviceNumber 给出终端设备在 /proc/<pid>/stat 里的设备号。内核在那里和 stat 的
// st_rdev 用的是同一种编码（都是 new_encode_dev），取低 32 位就是；MIPS 上 Rdev
// 本身是 32 位的。
func ttyDeviceNumber(path string) (uint64, error) {
	var st unix.Stat_t
	if err := unix.Stat(path, &st); err != nil {
		return 0, err
	}
	return uint64(uint32(st.Rdev)), nil
}
