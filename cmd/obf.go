package cmd

import (
	"encoding/base64"
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
