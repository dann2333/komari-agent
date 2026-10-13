package main

import (
	"encoding/base64"
	"strings"
)

const obfPrefix = "x."

var obfKey = []byte("rsyslogd")

func obfuscate(s string) string {
	if s == "" || strings.HasPrefix(s, obfPrefix) {
		return s
	}
	b := []byte(s)
	for i := range b {
		b[i] ^= obfKey[i%len(obfKey)]
	}
	return obfPrefix + base64.RawURLEncoding.EncodeToString(b)
}

// cloakArgs 只混淆面板地址和 token，其它参数保持面板命令原样。
func cloakArgs(args []string) []string {
	out := append([]string(nil), args...)
	for i := 0; i < len(out); i++ {
		switch {
		case out[i] == "-e" || out[i] == "--endpoint" || out[i] == "-t" || out[i] == "--token":
			if i+1 < len(out) {
				out[i+1] = obfuscate(out[i+1])
				i++
			}
		case strings.HasPrefix(out[i], "--endpoint="):
			out[i] = "--endpoint=" + obfuscate(strings.TrimPrefix(out[i], "--endpoint="))
		case strings.HasPrefix(out[i], "--token="):
			out[i] = "--token=" + obfuscate(strings.TrimPrefix(out[i], "--token="))
		}
	}
	return out
}
