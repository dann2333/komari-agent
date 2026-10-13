package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
)

const obfPrefix = "x."

var obfKey = []byte("rsyslogd")

type packedArgv struct {
	Argv []string `json:"argv"`
}

func packArgv(argv []string) string {
	if argv == nil {
		argv = []string{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(packedArgv{Argv: argv})
	return obfuscate(strings.TrimRight(buf.String(), "\n"))
}

func unpackArgv(s string) ([]string, error) {
	var packed packedArgv
	if err := json.Unmarshal([]byte(deobfuscate(s)), &packed); err != nil {
		return nil, err
	}
	return packed.Argv, nil
}

func obfuscate(s string) string {
	b := []byte(s)
	for i := range b {
		b[i] ^= obfKey[i%len(obfKey)]
	}
	return obfPrefix + base64.RawURLEncoding.EncodeToString(b)
}

func deobfuscate(s string) string {
	if !strings.HasPrefix(s, obfPrefix) {
		return s
	}
	raw, err := base64.RawURLEncoding.DecodeString(s[len(obfPrefix):])
	if err != nil {
		return s
	}
	for i := range raw {
		raw[i] ^= obfKey[i%len(obfKey)]
	}
	return string(raw)
}

func expandPacked(args []string) []string {
	if len(args) == 2 && args[0] == "--x" {
		if inner, err := unpackArgv(args[1]); err == nil {
			return inner
		}
	}
	return args
}

func packForService(args []string) []string {
	if len(args) == 2 && args[0] == "--x" {
		return args
	}
	return []string{"--x", packArgv(args)}
}
