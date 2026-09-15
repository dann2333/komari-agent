package main

import (
	"strings"
	"unicode"
)

// 启动参数在服务文件里是一整行字符串, 这里统一按 []string 处理,
// 写回去的时候再拼成一行。

// splitArgs 按空白切分命令行, 但尊重引号 —— systemd 的 ExecStart 里
// 带空格的 token 是用引号括起来的, 按空白硬切会把它拆坏。
func splitArgs(line string) []string {
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
		case unicode.IsSpace(r):
			flush()
		default:
			cur.WriteRune(r)
			has = true
		}
	}
	flush()
	return out
}

// joinArgs 把参数拼回一行, 含空格的重新加上引号
func joinArgs(args []string) string {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		if strings.ContainsAny(a, " \t") {
			a = "\"" + a + "\""
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}

// flagIndex 返回参数名所在的下标, 找不到返回 -1。
// 同时认 "--flag value" 和 "--flag=value" 两种写法。
func flagIndex(args []string, name string) int {
	for i, a := range args {
		if a == name || strings.HasPrefix(a, name+"=") {
			return i
		}
	}
	return -1
}

func hasFlag(args []string, name string) bool { return flagIndex(args, name) >= 0 }

// flagValue 取参数的值; 布尔开关返回空字符串
func flagValue(args []string, name string) string {
	i := flagIndex(args, name)
	if i < 0 {
		return ""
	}
	if eq := strings.Index(args[i], "="); eq > 0 {
		return args[i][eq+1:]
	}
	if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
		return args[i+1]
	}
	return ""
}

// unsetFlag 删掉参数及其取值
func unsetFlag(args []string, name string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, name+"=") {
			continue
		}
		if a == name {
			// 后面跟的那个如果不是新参数, 就是它的取值, 一起删
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
			}
			continue
		}
		out = append(out, a)
	}
	return out
}

// setFlag 设置参数; value 为空表示布尔开关。原来有就先删掉, 统一追加到末尾。
func setFlag(args []string, name, value string) []string {
	out := unsetFlag(args, name)
	out = append(out, name)
	if value != "" {
		out = append(out, value)
	}
	return out
}

// maskArgs 把 token 打码后返回, 用于打印
func maskArgs(args []string) string {
	out := make([]string, len(args))
	copy(out, args)
	for i, a := range out {
		switch {
		case a == "--token" || a == "-t":
			if i+1 < len(out) {
				out[i+1] = maskSecret(out[i+1])
			}
		case strings.HasPrefix(a, "--token="):
			out[i] = "--token=" + maskSecret(a[len("--token="):])
		case strings.HasPrefix(a, "-t="):
			out[i] = "-t=" + maskSecret(a[len("-t="):])
		}
	}
	return joinArgs(out)
}

func maskSecret(v string) string {
	if len(v) <= 8 {
		return "********"
	}
	return v[:4] + "****" + v[len(v)-4:]
}
