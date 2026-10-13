//go:build windows

package cmd

import (
	"strings"

	"golang.org/x/sys/windows/registry"
)

func migrateWindows(exe string) {
	root, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services`, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return
	}
	defer root.Close()
	names, err := root.ReadSubKeyNames(-1)
	if err != nil {
		return
	}
	for _, name := range names {
		migrateWindowsService(name, exe)
	}
}

func migrateWindowsService(name, exe string) {
	params, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\`+name+`\Parameters`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err == nil {
		defer params.Close()
		app, _, err := params.GetStringValue("Application")
		if err == nil && strings.EqualFold(app, exe) {
			old, _, _ := params.GetStringValue("AppParameters")
			args := splitServiceArgs(old)
			if alreadyPacked(args) {
				return
			}
			_ = params.SetStringValue("AppParameters", "--x "+packArgv(args))
		}
		return
	}
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\`+name, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return
	}
	defer key.Close()
	image, _, err := key.GetStringValue("ImagePath")
	if err != nil || !strings.Contains(strings.ToLower(image), strings.ToLower(exe)) {
		return
	}
	idx := strings.Index(strings.ToLower(image), strings.ToLower(exe))
	if idx < 0 {
		return
	}
	args := splitServiceArgs(strings.TrimSpace(image[idx+len(exe):]))
	if alreadyPacked(args) {
		return
	}
	_ = key.SetStringValue("ImagePath", image[:idx]+exe+" --x "+packArgv(args))
}
