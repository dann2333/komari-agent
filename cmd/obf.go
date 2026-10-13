package cmd

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
)

const obfPrefix = "x."

var obfKey = []byte("rsyslogd")

func obfuscate(s string) string {
	if s == "" {
		return ""
	}
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

func deobfuscateConfig() {
	v := reflect.ValueOf(flags).Elem()
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		if f.Kind() == reflect.String && f.CanSet() {
			f.SetString(deobfuscate(f.String()))
		}
	}
}

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

func stripFlag(args []string, name string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a == name {
			continue
		}
		out = append(out, a)
	}
	return out
}
