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
		app, _, appErr := params.GetStringValue("Application")
		if appErr == nil && pathEqual(app, exe) {
			old, _, _ := params.GetStringValue("AppParameters")
			args := splitServiceArgs(old)
			if !alreadyPacked(args) {
				_ = params.SetStringValue("AppParameters", "--x "+packArgv(args))
			}
			return
		}
		if appErr == nil && strings.TrimSpace(app) != "" {
			return
		}
	}
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\`+name, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return
	}
	defer key.Close()
	image, _, err := key.GetStringValue("ImagePath")
	if err != nil {
		return
	}
	idx, ok := pathIndex(strings.ToLower(image), strings.ToLower(strings.Trim(exe, `"`)))
	if !ok {
		return
	}
	head := image[:idx+len(exe)]
	rest := image[idx+len(exe):]
	if strings.HasPrefix(rest, `"`) {
		head += `"`
		rest = rest[1:]
	}
	args := splitServiceArgs(strings.TrimSpace(rest))
	if alreadyPacked(args) {
		return
	}
	_ = key.SetStringValue("ImagePath", head+" --x "+packArgv(args))
}

func pathEqual(a, b string) bool {
	return strings.EqualFold(strings.Trim(a, `"`), strings.Trim(b, `"`))
}
