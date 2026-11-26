//go:build !windows

package terminal

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// newTerminalImpl 创建一个新的终端实例。
// 它会尝试根据用户配置文件查找默认 shell，如果失败则回退到常见 shell。
// 优先以交互模式启动 shell，如果不支持则回退到非交互模式。
func newTerminalImpl() (*terminalImpl, error) {
	shell := ""
	// 从 /etc/passwd 获取用户默认 shell
	userHomeDir, err := os.UserHomeDir() // 获取当前用户的主目录
	if err == nil {
		passwdContent, err := os.ReadFile("/etc/passwd")
		if err == nil {
			for _, line := range strings.Split(string(passwdContent), "\n") {
				if strings.Contains(line, userHomeDir) {
					parts := strings.Split(line, ":")
					if len(parts) >= 7 && parts[6] != "" {
						shell = parts[6]
						//log.Printf("Found shell from /etc/passwd: %s for user home: %s\n", shell, userHomeDir)
						break
					}
				}
			}
		} else {
			log.Printf("Error reading /etc/passwd: %v\n", err)
		}
	} else {
		log.Printf("Error getting user home directory: %v\n", err)
	}

	// 验证从 /etc/passwd 获取的 shell 是否可用
	if shell != "" {
		if _, err := exec.LookPath(shell); err != nil {
			log.Printf("Shell '%s' from /etc/passwd not found in PATH, falling back.\n", shell)
			shell = "" // 默认 shell 不可用，清空以进入回退逻辑
		}
	}

	// 回退到默认 shell 列表
	defaultShells := []string{"zsh", "bash", "sh"}
	if shell == "" {
		log.Println("Shell not found or invalid, trying default shells.")
		for _, s := range defaultShells {
			if _, err := exec.LookPath(s); err == nil {
				shell = s
				log.Printf("Using default shell: %s\n", shell)
				break
			}
		}
	}

	if shell == "" {
		return nil, fmt.Errorf("no supported shell found among %v", defaultShells)
	}

	cmd := buildMotdShellCommand(shell)
	env := append(os.Environ(), // 继承系统环境变量
		"TERM=xterm-256color", // 设置终端类型，提高兼容性
		"LANG=C.UTF-8",        // 设置语言环境为 UTF-8
		"LC_ALL=C.UTF-8",      // 强制所有本地化变量为 UTF-8
		// 终端里敲过的命令不落盘：shell 退出或连接断开时默认会写进 ~/.bash_history
		"HISTFILE=/dev/null",
	)
	cmd.Env = env

	// 能脱离 agent 就脱离（见 startDetached），不成就照旧直接启动
	var pid int
	var tty *os.File
	if !flags.DisableProcessMask {
		tty, pid, err = startDetached(shell, env)
	}
	if tty == nil {
		tty, err = pty.Start(cmd)
	}
	if err != nil {
		// 回退到原始启动逻辑（直接启动 shell，再无参数）
		cmd = exec.Command(shell)
		cmd.Env = env
		tty, err = pty.Start(cmd)
		if err != nil {
			return nil, fmt.Errorf("failed to start pty with argv0 prelude and plain shell: %v", err)
		}
	}

	// 设置初始终端大小
	pty.Setsize(tty, &pty.Winsize{Rows: 24, Cols: 80})

	return &terminalImpl{
		shell: shell,
		term: &unixTerminal{
			tty: tty,
			cmd: cmd,
			pid: pid,
		},
	}, nil
}

// startDetached 让 shell 脱离 agent 单独成一个会话，返回 pty 主设备和 shell 的进程号。
// ttyDevice 给出终端设备的设备号，detachedAlive 报告进程是否还是持有这台终端的会话首进程。
// 不支持脱离的平台保持这里的默认值，调用方照旧用 pty.Start 直接启动。
var (
	startDetached = func(string, []string) (*os.File, int, error) { return nil, 0, errors.New("not supported") }
	ttyDevice     = func(string) (uint64, error) { return 0, nil }
	detachedAlive = func(int, uint64) bool { return false }
)

const motdShellPrelude = "for f in /etc/update-motd.d/*; do [ -e \"$f\" ] && [ -x \"$f\" ] && \"$f\"; done; [ -r /etc/motd ] && cat /etc/motd; exec \"$1\""

func buildMotdShellCommand(shell string) *exec.Cmd {
	return exec.Command("/bin/sh", "-c", motdShellPrelude, "sh", shell)
}

// unixTerminal 实现了 Unix 系统下的终端接口。
type unixTerminal struct {
	tty *os.File  // 伪终端设备文件
	cmd *exec.Cmd // 启动的 shell 进程命令
	// 脱离了 agent 的 shell 的进程号（见 startDetached）；0 表示 shell 是 cmd 启动的子进程
	pid int
}

// Close 关闭终端，并尝试优雅地终止 shell 进程及其子进程。
func (t *unixTerminal) Close() error {
	defer func() {
		if t.tty != nil {
			_ = t.tty.Close()
		}
	}()
	if t.pid > 0 {
		return t.closeDetached()
	}
	if t.cmd == nil || t.cmd.Process == nil {
		return fmt.Errorf("terminal process is already nil or not started")
	}

	// 获取进程组 ID (PGID)。如果获取失败，则使用进程 PID 作为回退。
	// 向进程组发送信号可以确保 shell 启动的子进程也能接收到信号。
	pgid, err := syscall.Getpgid(t.cmd.Process.Pid)
	if err != nil {
		log.Printf("Failed to get process group ID for PID %d: %v. Using PID as PGID.\n", t.cmd.Process.Pid, err)
		pgid = t.cmd.Process.Pid
	}

	// 发送 SIGTERM 信号，请求进程组优雅退出
	log.Printf("Sending SIGTERM to process group %d...\n", pgid)
	_ = syscall.Kill(-pgid, syscall.SIGTERM) // -pgid 表示发送给进程组

	done := make(chan error, 1)
	go func() {
		// 等待命令退出。如果命令已经退出，Wait()会立即返回。
		done <- t.cmd.Wait()
	}()

	select {
	case err := <-done:
		// 进程已退出
		if err == nil {
			return nil
		}
		// 如果是 ExitError 且进程已退出，也视为成功关闭
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.Exited() {
			return nil // 进程已退出，尽管可能不是0状态码，但我们认为它已关闭
		}
		return fmt.Errorf("process group did not exit gracefully: %v", err)
	case <-time.After(5 * time.Second):
		// 5 秒内未退出，发送 SIGKILL 强制终止
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		// 再次等待，确保进程被杀死，并获取最终的退出状态
		killErr := <-done
		if killErr == nil {
			return nil
		}
		if exitErr, ok := killErr.(*exec.ExitError); ok && exitErr.Exited() {
			return nil
		}
		log.Printf("Failed to kill process group %d after SIGKILL: %v\n", pgid, killErr)
		return fmt.Errorf("failed to kill process group %d: %v", pgid, killErr)
	}
}

// closeDetached 结束脱离了 agent 的 shell。它不是本进程的子进程，等不到退出状态，
// 只能等它的进程号消失（由 init 收尸）。
func (t *unixTerminal) closeDetached() error {
	pgid := t.pid // setsid 之后 shell 既是会话首进程，也是进程组首进程
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	if t.waitGone(5 * time.Second) {
		return nil
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	if t.waitGone(5 * time.Second) {
		return nil
	}
	return fmt.Errorf("failed to kill shell process group %d", pgid)
}

// waitGone 等到 shell 的进程号不再是那个持有这台终端的会话首进程。
// 进程号被别的进程复用不算，免得对不相干的进程组下杀手。timeout 为 0 时一直等。
func (t *unixTerminal) waitGone(timeout time.Duration) bool {
	dev, _ := ttyDevice(t.tty.Name())
	deadline := time.Now().Add(timeout)
	for detachedAlive(t.pid, dev) {
		if timeout > 0 && time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
	return true
}

// Read 从伪终端读取数据。
func (t *unixTerminal) Read(p []byte) (int, error) {
	if t.tty == nil {
		return 0, fmt.Errorf("tty is nil")
	}
	return t.tty.Read(p)
}

// Write 向伪终端写入数据。
func (t *unixTerminal) Write(p []byte) (int, error) {
	if t.tty == nil {
		return 0, fmt.Errorf("tty is nil")
	}
	return t.tty.Write(p)
}

// Resize 调整伪终端的大小。
func (t *unixTerminal) Resize(cols, rows int) error {
	if t.tty == nil {
		return fmt.Errorf("tty is nil")
	}
	return pty.Setsize(t.tty, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
}

// Wait 等待 shell 进程退出。
func (t *unixTerminal) Wait() error {
	if t.pid > 0 {
		t.waitGone(0)
		return nil
	}
	if t.cmd == nil {
		return fmt.Errorf("command is nil")
	}
	return t.cmd.Wait()
}
