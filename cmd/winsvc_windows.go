//go:build windows

package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	pkg_flags "github.com/komari-monitor/komari-agent/cmd/flags"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
)

// 自己登记成服务，不再套 nssm。ImagePath 就是这个程序。
var underSCM bool

func serviceControlled() bool { return underSCM }

func runAsWindowsService(start func() error) (bool, error) {
	isService, err := svc.IsWindowsService()
	if err != nil || !isService {
		return false, nil
	}
	underSCM = true
	err = svc.Run(detectServiceName(), &scmHandler{start: start})
	return true, err
}

type scmHandler struct {
	start func() error
}

func (h *scmHandler) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}
	done := make(chan error, 1)
	go func() { done <- h.start() }()
	status := svc.Status{State: svc.Running, Accepts: accepted}
	changes <- status
	for {
		select {
		case err := <-done:
			if err != nil {
				return false, 1
			}
			return false, 0
		case req := <-requests:
			switch req.Cmd {
			case svc.Interrogate:
				changes <- status
			case svc.Stop, svc.Shutdown:
				status.State = svc.StopPending
				status.WaitHint = 15000
				changes <- status
				pkg_flags.RequestStop()
				select {
				case <-done:
				case <-time.After(15 * time.Second):
				}
				return false, 0
			}
		}
	}
}

func detectServiceName() string {
	const fallback = "rsyslogd"
	exe, err := os.Executable()
	if err != nil {
		return fallback
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	root, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services`, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return fallback
	}
	defer root.Close()
	names, err := root.ReadSubKeyNames(-1)
	if err != nil {
		return fallback
	}
	exe = strings.ToLower(exe)
	for _, name := range names {
		key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\`+name, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		image, _, err := key.GetStringValue("ImagePath")
		key.Close()
		if err != nil {
			continue
		}
		if _, ok := pathIndex(strings.ToLower(image), exe); ok {
			return name
		}
	}
	return fallback
}
