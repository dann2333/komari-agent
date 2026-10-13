package cmd

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// migrateInstalledArgs rewrites an already installed service so the next
// start carries every flag inside a single --x value. Plaintext flags and
// the previous per-flag obfuscation are both folded in.
func migrateInstalledArgs() {
	if startupAlreadyPacked() {
		return
	}
	exe := currentExe()
	if exe == "" {
		return
	}
	switch runtime.GOOS {
	case "windows":
		migrateWindows(exe)
	default:
		if migrateUnix(exe) {
			reloadSystemd()
		}
	}
}

func startupAlreadyPacked() bool {
	args := os.Args[1:]
	return len(args) == 2 && args[0] == "--x"
}

func currentExe() string {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		return ""
	}
	if abs, err := filepath.Abs(exe); err == nil {
		exe = abs
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		return real
	}
	return exe
}

func migrateUnix(exe string) bool {
	home, _ := os.UserHomeDir()
	dirs := []string{
		"/etc/systemd/system",
		"/lib/systemd/system",
		"/usr/lib/systemd/system",
		"/etc/init.d",
		"/etc/init",
		"/Library/LaunchDaemons",
		"/Library/LaunchAgents",
	}
	if home != "" {
		dirs = append(dirs,
			filepath.Join(home, ".config/systemd/user"),
			filepath.Join(home, "Library/LaunchAgents"),
		)
	}
	changed := false
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			body, err := os.ReadFile(path)
			if err != nil || !strings.Contains(string(body), exe) {
				continue
			}
			next, ok := rewriteServiceText(string(body), exe)
			if !ok || next == string(body) {
				continue
			}
			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			if err := writeText(path, next, info.Mode().Perm()); err == nil {
				changed = true
			}
		}
	}
	return changed
}

func reloadSystemd() {
	if exec.Command("systemctl", "daemon-reload").Run() != nil {
		_ = exec.Command("sudo", "-n", "systemctl", "daemon-reload").Run()
	}
	_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
}

func writeText(path, text string, mode os.FileMode) error {
	if err := os.WriteFile(path, []byte(text), mode); err == nil {
		return nil
	}
	cmd := exec.Command("sudo", "-n", "tee", path)
	cmd.Stdin = strings.NewReader(text)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}

func rewriteServiceText(text, exe string) (string, bool) {
	if strings.Contains(text, "<key>ProgramArguments</key>") {
		return rewriteLaunchd(text, exe)
	}
	lines := strings.Split(text, "\n")
	changed := false
	ownsArgs := commandMatchesExe(text, exe)
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trim, "ExecStart="):
			if next, ok := rewriteExecStart(line, exe); ok {
				lines[i] = next
				changed = true
			}
		case strings.HasPrefix(trim, "exec "):
			if next, ok := rewriteUpstart(line, exe); ok {
				lines[i] = next
				changed = true
			}
		case ownsArgs && (strings.HasPrefix(trim, "command_args=") || strings.HasPrefix(trim, "ARGS=")):
			if next, ok := rewriteArgAssignment(line); ok {
				lines[i] = next
				changed = true
			}
		}
	}
	if !changed {
		return text, false
	}
	return strings.Join(lines, "\n"), true
}

func commandMatchesExe(text, exe string) bool {
	for _, line := range strings.Split(text, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "command=") || strings.HasPrefix(trim, "PROG=") {
			if _, ok := pathIndex(trim, exe); ok {
				return true
			}
		}
	}
	return false
}

func rewriteExecStart(line, exe string) (string, bool) {
	idx, ok := pathIndex(line, exe)
	if !ok {
		return line, false
	}
	head := line[:idx+len(exe)]
	rest := line[idx+len(exe):]
	if strings.HasPrefix(rest, "\"") {
		head += "\""
		rest = rest[1:]
	}
	args := splitServiceArgs(strings.TrimSpace(rest))
	if alreadyPacked(args) {
		return line, false
	}
	return head + " --x " + packArgv(args), true
}

func rewriteUpstart(line, exe string) (string, bool) {
	return rewriteExecStart(line, exe)
}

func pathIndex(line, exe string) (int, bool) {
	if exe == "" {
		return 0, false
	}
	start := 0
	for start < len(line) {
		idx := strings.Index(line[start:], exe)
		if idx < 0 {
			return 0, false
		}
		idx += start
		after := idx + len(exe)
		beforeOK := idx == 0 || isBeforeBoundary(line[idx-1])
		afterOK := after == len(line) || isAfterBoundary(line[after])
		if beforeOK && afterOK {
			return idx, true
		}
		start = idx + 1
	}
	return 0, false
}

func isBeforeBoundary(b byte) bool {
	switch b {
	case ' ', '\t', '"', '\'', '=', '-', '@', '+', '!':
		return true
	default:
		return false
	}
}

func isAfterBoundary(b byte) bool {
	switch b {
	case ' ', '\t', '"', '\'':
		return true
	default:
		return false
	}
}

func rewriteArgAssignment(line string) (string, bool) {
	key, value, quoted := splitAssignment(line)
	args := splitServiceArgs(value)
	if alreadyPacked(args) {
		return line, false
	}
	blob := "--x " + packArgv(args)
	if quoted {
		return key + "\"" + blob + "\"", true
	}
	return key + blob, true
}

func splitAssignment(line string) (key, value string, quoted bool) {
	eq := strings.Index(line, "=")
	if eq < 0 {
		return line, "", false
	}
	key = line[:eq+1]
	value = strings.TrimSpace(line[eq+1:])
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		return key, value[1 : len(value)-1], true
	}
	return key, value, false
}

func alreadyPacked(args []string) bool {
	return len(args) == 2 && args[0] == "--x"
}

func rewriteLaunchd(text, exe string) (string, bool) {
	lines := strings.Split(text, "\n")
	inKey, inArray := false, false
	var args []string
	var arrayIdx []int
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		switch {
		case trim == "<key>ProgramArguments</key>":
			inKey = true
		case inKey && trim == "<array>":
			inKey, inArray = false, true
		case inArray && trim == "</array>":
			inArray = false
		case inArray:
			value := strings.TrimSuffix(strings.TrimPrefix(trim, "<string>"), "</string>")
			if value != trim {
				args = append(args, value)
				arrayIdx = append(arrayIdx, i)
			}
		}
	}
	if len(args) == 0 || !strings.Contains(strings.Join(args, "\n"), exe) {
		return text, false
	}
	rest := args[1:]
	if args[0] != exe {
		for i, arg := range args {
			if arg == exe {
				rest = args[i+1:]
				break
			}
		}
	}
	if alreadyPacked(rest) {
		return text, false
	}
	blob := packArgv(rest)
	indent := "        "
	if len(arrayIdx) > 0 {
		indent = lines[arrayIdx[0]][:strings.Index(lines[arrayIdx[0]], "<")]
	}
	var out []string
	skip := map[int]bool{}
	for _, i := range arrayIdx {
		skip[i] = true
	}
	inserted := false
	for i, line := range lines {
		if skip[i] {
			if !inserted {
				out = append(out,
					indent+"<string>"+exe+"</string>",
					indent+"<string>--x</string>",
					indent+"<string>"+blob+"</string>",
				)
				inserted = true
			}
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n"), true
}

func splitServiceArgs(line string) []string {
	var (
		out   []string
		cur   strings.Builder
		quote rune
		has   bool
	)
	flush := func() {
		if has {
			out = append(out, cur.String())
			cur.Reset()
			has = false
		}
	}
	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			has = true
		case r == ' ' || r == '\t':
			flush()
		default:
			cur.WriteRune(r)
			has = true
		}
	}
	flush()
	return out
}
