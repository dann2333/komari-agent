//go:build !windows && !plan9

package main

import (
	"os"
	"syscall"
)

func fileOwner(path string) (uid, gid int, ok bool) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, 0, false
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return int(sys.Uid), int(sys.Gid), true
}

func chownFile(path string, uid, gid int) error {
	if os.Geteuid() != 0 {
		return nil
	}
	return os.Chown(path, uid, gid)
}
