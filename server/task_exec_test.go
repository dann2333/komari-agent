package server

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestRunTaskCommandMultilineUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell script execution test")
	}

	result, exitCode := runTaskCommand("printf '%s\\n' first\nprintf '%s\\n' second\n")

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with result %q", exitCode, result)
	}
	if result != "first\nsecond\n" {
		t.Fatalf("unexpected result %q", result)
	}
}

func TestRunTaskCommandEscapingAndWildcardsUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell script execution test")
	}

	tempDir := t.TempDir()
	command := strings.Join([]string{
		"cd " + shellSingleQuote(tempDir),
		"touch alpha.txt beta.log gamma.txt",
		"printf '%s\\n' \"quoted value with spaces\"",
		"printf '%s\\n' '*.txt'",
		"printf '%s ' *.txt",
		"printf '\\n'",
	}, "\n")

	result, exitCode := runTaskCommand(command)

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d with result %q", exitCode, result)
	}
	lines := strings.Split(strings.TrimSuffix(result, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 output lines, got %d in %q", len(lines), result)
	}
	if lines[0] != "quoted value with spaces" {
		t.Fatalf("quoted argument was not preserved: %q", lines[0])
	}
	if lines[1] != "*.txt" {
		t.Fatalf("quoted wildcard should remain literal, got %q", lines[1])
	}
	expanded := strings.Fields(lines[2])
	sort.Strings(expanded)
	if len(expanded) != 2 || expanded[0] != "alpha.txt" || expanded[1] != "gamma.txt" {
		t.Fatalf("wildcard expansion mismatch: %q", lines[2])
	}
}

func TestBuildTaskCommandUsesShellStdinUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell script execution test")
	}

	cmd, status, cleanup, err := buildTaskCommand("printf done")
	if err != nil {
		t.Fatalf("buildTaskCommand returned error: %v", err)
	}
	defer cleanup()

	if filepath.Base(cmd.Path) != "sh" {
		t.Fatalf("expected sh command, got %q", cmd.Path)
	}
	// 任务不挂在 agent 名下：外层 sh 起一个后台进程就退出，后台进程再从标准输入读脚本
	if len(cmd.Args) != 3 || cmd.Args[1] != "-c" || !strings.Contains(cmd.Args[2], "sh -s") {
		t.Fatalf("expected sh -c with a script that runs sh -s, got %#v", cmd.Args)
	}
	if cmd.Stdin == nil {
		t.Fatal("expected command script to be provided on stdin")
	}
	if status == nil {
		t.Fatal("expected a status pipe that carries the exit code of the detached shell")
	}
}

func TestBuildTaskCommandStartsDirectlyWhenDisguiseIsOffUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell script execution test")
	}
	old := flags.DisableProcessMask
	defer func() { flags.DisableProcessMask = old }()
	flags.DisableProcessMask = true

	cmd, status, cleanup, err := buildTaskCommand("printf done")
	if err != nil {
		t.Fatalf("buildTaskCommand returned error: %v", err)
	}
	defer cleanup()

	if strings.Join(cmd.Args, " ") != "sh -s" {
		t.Fatalf("expected sh -s args, got %#v", cmd.Args)
	}
	if status != nil {
		t.Fatal("expected no status pipe for a directly started shell")
	}
}

func TestRunTaskCommandReportsExitCodeOfDetachedShellUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell script execution test")
	}

	cases := []struct {
		name    string
		command string
		result  string
		code    int
	}{
		{"exit code", "echo before\nexit 7", "before\n", 7},
		{"exec replaces the task shell", "exec sh -c 'exit 5'", "", 5},
		{"false", "false", "", 1},
		{"output without trailing newline", "printf raw", "raw", 0},
		{"shebang line", "#!/bin/sh\necho ok\nexit 3", "ok\n", 3},
		{"command reading standard input sees the end of the script", "echo a\ncat", "a\n", 0},
		{"background job holds the result until it exits", "sleep 0.3 &\necho started", "started\n", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, exitCode := runTaskCommand(tc.command)
			if result != tc.result || exitCode != tc.code {
				t.Fatalf("got %q, %d; want %q, %d", result, exitCode, tc.result, tc.code)
			}
		})
	}
}

func TestRunTaskCommandKeepsStderrAfterStdoutUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell script execution test")
	}

	result, exitCode := runTaskCommand("echo out\necho err >&2")

	// 和直接启动时一样：标准输出后面换一行接标准错误
	if result != "out\n\nerr\n" || exitCode != 0 {
		t.Fatalf("got %q, %d", result, exitCode)
	}
}

func TestRunTaskCommandReportsKilledShellUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell script execution test")
	}

	// 任务 shell 被信号杀死：包装它的 sh 会报一行 KILLED，退出码是 128+信号
	result, exitCode := runTaskCommand("kill -9 $$")
	if exitCode != 137 || (strings.TrimSpace(result) != "" && !strings.Contains(strings.ToUpper(result), "KILL")) {
		t.Fatalf("got %q, %d", result, exitCode)
	}
}

func TestBuildTaskCommandWritesUtf8BomWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows PowerShell script execution test")
	}

	cmd, _, cleanup, err := buildTaskCommand("Write-Output '你好'")
	if err != nil {
		t.Fatalf("buildTaskCommand returned error: %v", err)
	}
	defer cleanup()

	if filepath.Base(cmd.Path) != "powershell.exe" && filepath.Base(cmd.Path) != "powershell" {
		t.Fatalf("expected powershell command, got %q", cmd.Path)
	}
	if len(cmd.Args) == 0 {
		t.Fatal("expected PowerShell script path in command args")
	}

	scriptFile := cmd.Args[len(cmd.Args)-1]
	file, err := filepath.Abs(scriptFile)
	if err != nil {
		t.Fatalf("failed to resolve script path: %v", err)
	}

	f, err := filepath.EvalSymlinks(file)
	if err != nil {
		t.Fatalf("failed to evaluate script path: %v", err)
	}
	contents, err := os.ReadFile(f)
	if err != nil {
		t.Fatalf("failed to read script file: %v", err)
	}
	if len(contents) < 3 || contents[0] != 0xEF || contents[1] != 0xBB || contents[2] != 0xBF {
		t.Fatalf("expected UTF-8 BOM at start of script, got %#v", contents[:min(len(contents), 3)])
	}
}

func TestAppendErrorResultAvoidsLeadingNewline(t *testing.T) {
	if got := appendErrorResult("", "stderr"); got != "stderr" {
		t.Fatalf("expected stderr without leading newline, got %q", got)
	}
	if got := appendErrorResult("stdout", "stderr"); got != "stdout\nstderr" {
		t.Fatalf("expected stdout and stderr separated by newline, got %q", got)
	}
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
