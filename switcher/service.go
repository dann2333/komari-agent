package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// 支持的 init: systemd / systemd-user / openrc / procd / upstart / launchd
type service struct {
	Init   string
	Name   string
	File   string
	Binary string
	Args   []string
}

func userUnitPath(name string) string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "systemd", "user", name+".service")
}

func locateService(name string) *service {
	for _, p := range []string{
		"/etc/systemd/system/" + name + ".service",
		"/lib/systemd/system/" + name + ".service",
	} {
		if isFile(p) {
			return &service{Init: "systemd", Name: name, File: p}
		}
	}
	if p := userUnitPath(name); p != "" && isFile(p) {
		return &service{Init: "systemd-user", Name: name, File: p}
	}
	if p := "/etc/init.d/" + name; isFile(p) {
		init := "openrc"
		if data, err := os.ReadFile(p); err == nil && strings.Contains(string(data), "USE_PROCD") {
			init = "procd"
		}
		return &service{Init: init, Name: name, File: p}
	}
	if p := "/etc/init/" + name + ".conf"; isFile(p) {
		return &service{Init: "upstart", Name: name, File: p}
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{
		"/Library/LaunchDaemons/com.komari." + name + ".plist",
		filepath.Join(home, "Library/LaunchAgents/com.komari."+name+".plist"),
	} {
		if isFile(p) {
			return &service{Init: "launchd", Name: name, File: p}
		}
	}
	return nil
}

// discoverService 先按名字找, 找不到就扫一遍常见目录里带 komari 字样的服务文件
func discoverService(name string) (*service, error) {
	if name != "" {
		if s := locateService(name); s != nil {
			return s, parseService(s)
		}
		return nil, fmt.Errorf("找不到名为 %s 的服务", name)
	}
	if s := locateService("komari-agent"); s != nil {
		return s, parseService(s)
	}

	home, _ := os.UserHomeDir()
	patterns := []string{
		"/etc/systemd/system/*.service",
		"/etc/init.d/*",
		"/etc/init/*.conf",
		"/Library/LaunchDaemons/com.komari.*.plist",
	}
	if p := userUnitPath("*"); p != "" {
		patterns = append(patterns, p)
	}
	if home != "" {
		patterns = append(patterns, filepath.Join(home, "Library/LaunchAgents/com.komari.*.plist"))
	}
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		for _, m := range matches {
			data, err := os.ReadFile(m)
			if err != nil || !strings.Contains(strings.ToLower(string(data)), "komari") {
				continue
			}
			base := filepath.Base(m)
			base = strings.TrimSuffix(base, ".service")
			base = strings.TrimSuffix(base, ".conf")
			base = strings.TrimSuffix(base, ".plist")
			base = strings.TrimPrefix(base, "com.komari.")
			if s := locateService(base); s != nil {
				return s, parseService(s)
			}
		}
	}
	return nil, fmt.Errorf("没有找到已安装的 komari agent 服务")
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines, sc.Err()
}

// parseService 从服务文件里解析出二进制路径和启动参数
func parseService(s *service) error {
	lines, err := readLines(s.File)
	if err != nil {
		return err
	}
	var tokens []string
	switch s.Init {
	case "systemd", "systemd-user":
		for _, l := range lines {
			if v, ok := cutPrefix(strings.TrimSpace(l), "ExecStart="); ok {
				tokens = splitArgs(strings.TrimLeft(v, "-@+!"))
				break
			}
		}
	case "openrc":
		tokens = append(tokens, splitArgs(quotedValue(lines, "command="))...)
		tokens = append(tokens, splitArgs(quotedValue(lines, "command_args="))...)
	case "procd":
		tokens = append(tokens, splitArgs(quotedValue(lines, "PROG="))...)
		tokens = append(tokens, splitArgs(quotedValue(lines, "ARGS="))...)
	case "upstart":
		for _, l := range lines {
			t := strings.TrimSpace(l)
			if v, ok := cutPrefix(t, "exec "); ok {
				tokens = splitArgs(v)
				break
			}
		}
	case "launchd":
		tokens = launchdArgs(lines)
	}
	if len(tokens) == 0 {
		return fmt.Errorf("无法从 %s 解析出启动命令", s.File)
	}
	s.Binary = tokens[0]
	s.Args = tokens[1:]
	return nil
}

func cutPrefix(s, prefix string) (string, bool) {
	if strings.HasPrefix(s, prefix) {
		return s[len(prefix):], true
	}
	return "", false
}

// quotedValue 取 KEY="value" 这种行的值
func quotedValue(lines []string, key string) string {
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if v, ok := cutPrefix(t, key); ok {
			return strings.Trim(v, `"'`)
		}
	}
	return ""
}

func launchdArgs(lines []string) []string {
	var (
		out     []string
		inKey   bool
		inArray bool
	)
	for _, l := range lines {
		t := strings.TrimSpace(l)
		switch {
		case t == "<key>ProgramArguments</key>":
			inKey = true
		case inKey && t == "<array>":
			inKey, inArray = false, true
		case inArray && t == "</array>":
			return out
		case inArray:
			v := strings.TrimSuffix(strings.TrimPrefix(t, "<string>"), "</string>")
			if v != t {
				out = append(out, v)
			}
		}
	}
	return out
}

// writeArgs 把新的启动参数写回服务文件, 只改那一行, 其它内容原样保留
func (s *service) writeArgs(binary string, args []string) error {
	lines, err := readLines(s.File)
	if err != nil {
		return err
	}
	cmdline := joinArgs(append([]string{binary}, args...))
	replaced := false
	replaceFirst := func(match func(string) bool, newline func(string) string) {
		for i, l := range lines {
			if match(strings.TrimSpace(l)) {
				lines[i] = newline(l)
				replaced = true
				return
			}
		}
	}

	switch s.Init {
	case "systemd", "systemd-user":
		replaceFirst(
			func(t string) bool { return strings.HasPrefix(t, "ExecStart=") },
			func(l string) string {
				// ExecStart=-/path 里那个 - 是 systemd 的前缀修饰符, 得留着
				rest := strings.TrimPrefix(strings.TrimSpace(l), "ExecStart=")
				modifiers := ""
				for _, c := range rest {
					if !strings.ContainsRune("-@+!", c) {
						break
					}
					modifiers += string(c)
				}
				return "ExecStart=" + modifiers + cmdline
			})
	case "openrc":
		replaceFirst(
			func(t string) bool { return strings.HasPrefix(t, "command=") },
			func(string) string { return `command="` + binary + `"` })
		if !replaced {
			return fmt.Errorf("%s 里没有 command= 行", s.File)
		}
		replaced = false
		replaceFirst(
			func(t string) bool { return strings.HasPrefix(t, "command_args=") },
			func(string) string { return `command_args="` + joinArgs(args) + `"` })
		if !replaced {
			lines = append(lines, `command_args="`+joinArgs(args)+`"`)
			replaced = true
		}
	case "procd":
		replaceFirst(
			func(t string) bool { return strings.HasPrefix(t, "PROG=") },
			func(string) string { return `PROG="` + binary + `"` })
		replaced = false
		replaceFirst(
			func(t string) bool { return strings.HasPrefix(t, "ARGS=") },
			func(string) string { return `ARGS="` + joinArgs(args) + `"` })
		if !replaced {
			lines = append(lines, `ARGS="`+joinArgs(args)+`"`)
			replaced = true
		}
	case "upstart":
		replaceFirst(
			func(t string) bool { return strings.HasPrefix(t, "exec ") },
			func(string) string { return "exec " + cmdline })
	case "launchd":
		lines, replaced = rewriteLaunchdArgs(lines, binary, args)
	}
	if !replaced {
		return fmt.Errorf("没能在 %s 里找到要改写的那一行", s.File)
	}
	return writeLinesPreservingMode(s.File, lines)
}

func rewriteLaunchdArgs(lines []string, binary string, args []string) ([]string, bool) {
	var (
		out     []string
		inKey   bool
		inArray bool
		done    bool
	)
	for _, l := range lines {
		t := strings.TrimSpace(l)
		switch {
		case t == "<key>ProgramArguments</key>":
			inKey = true
			out = append(out, l)
		case inKey && t == "<array>":
			inKey, inArray = false, true
			out = append(out, l)
			for _, a := range append([]string{binary}, args...) {
				out = append(out, "        <string>"+a+"</string>")
			}
			done = true
		case inArray && t == "</array>":
			inArray = false
			out = append(out, l)
		case inArray:
			// 旧的 <string> 全部丢掉, 上面已经写了新的
		default:
			out = append(out, l)
		}
	}
	return out, done
}

func writeLinesPreservingMode(path string, lines []string) error {
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp := path + ".komari-switch.tmp"
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(tmp, []byte(body), mode); err != nil {
		return err
	}
	if uid, gid, ok := fileOwner(path); ok {
		chownFile(tmp, uid, gid)
	}
	if err := os.Chmod(tmp, mode); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func (s *service) launchdDomain() string {
	if strings.HasPrefix(s.File, "/Library/LaunchDaemons/") {
		return "system"
	}
	return fmt.Sprintf("gui/%d", os.Getuid())
}

func (s *service) control(action string) error {
	switch s.Init {
	case "systemd":
		return run("systemctl", action, s.Name+".service")
	case "systemd-user":
		return run("systemctl", "--user", action, s.Name+".service")
	case "openrc":
		return run("rc-service", s.Name, action)
	case "procd":
		return run(s.File, action)
	case "upstart":
		return run("initctl", action, s.Name)
	case "launchd":
		if action == "stop" {
			run("launchctl", "bootout", s.launchdDomain(), s.File)
			return nil
		}
		return run("launchctl", "bootstrap", s.launchdDomain(), s.File)
	}
	return fmt.Errorf("不认识的 init 系统: %s", s.Init)
}

func (s *service) reloadDefinition() {
	switch s.Init {
	case "systemd":
		run("systemctl", "daemon-reload")
	case "systemd-user":
		run("systemctl", "--user", "daemon-reload")
	}
}

func (s *service) isRunning() bool {
	switch s.Init {
	case "systemd":
		return run("systemctl", "is-active", "--quiet", s.Name+".service") == nil
	case "systemd-user":
		return run("systemctl", "--user", "is-active", "--quiet", s.Name+".service") == nil
	case "openrc":
		return run("rc-service", s.Name, "status") == nil
	case "upstart":
		out, err := exec.Command("initctl", "status", s.Name).Output()
		return err == nil && strings.Contains(string(out), "start/running")
	case "launchd":
		return run("launchctl", "print", s.launchdDomain()+"/com.komari."+s.Name) == nil
	case "procd":
		return processRunning(s.Binary)
	}
	return false
}

// processRunning 找命令行以这个二进制开头的进程。
// 用 /proc 自己看而不是 pgrep: 一来不依赖外部命令, 二来 pgrep -f 不锚定的话,
// 命令行里碰巧带这个路径的其它进程会让挂掉的服务被误判成还活着。
func processRunning(binary string) bool {
	if runtime.GOOS == "linux" {
		entries, err := os.ReadDir("/proc")
		if err == nil {
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				data, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
				if err != nil || len(data) == 0 {
					continue
				}
				argv0 := string(data[:clen(data)])
				if argv0 == binary {
					return true
				}
			}
			return false
		}
	}
	out, err := exec.Command("pgrep", "-f", "^"+binary).Output()
	return err == nil && len(strings.TrimSpace(string(out))) > 0
}

func clen(b []byte) int {
	for i, c := range b {
		if c == 0 {
			return i
		}
	}
	return len(b)
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}
