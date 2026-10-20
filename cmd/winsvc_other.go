//go:build !windows

package cmd

func serviceControlled() bool { return false }

func runAsWindowsService(func() error) (bool, error) { return false, nil }
