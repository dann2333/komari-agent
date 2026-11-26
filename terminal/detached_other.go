//go:build !linux

package terminal

// RunSpawnHelper 在 Linux 之外没有可做的：agent 不会带着 spawnEnv 启动副本。
func RunSpawnHelper() (bool, int) { return false, 0 }
