//go:build windows || plan9

package main

// Windows/plan9 上没有 uid/gid 这套东西, 属主原样交给系统处理
func fileOwner(string) (int, int, bool) { return 0, 0, false }
func chownFile(string, int, int) error  { return nil }
