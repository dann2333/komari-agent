package cmd

import (
	"os"
	"reflect"
	"sort"
	"strings"
	"unsafe"
)

func effectiveProcessName() string {
	if flags.DisableProcessMask {
		return ""
	}
	if flags.ProcessName != "" {
		return flags.ProcessName
	}
	if compiledProcessName != "" {
		return compiledProcessName
	}
	return defaultProcessName
}

func quietIdentity() bool {
	if flags.ShowBanner || compiledQuiet == "0" {
		return false
	}
	return true
}

func cloneLiveStrings() {
	v := reflect.ValueOf(flags).Elem()
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		if f.Kind() == reflect.String && f.CanSet() {
			f.SetString(strings.Clone(f.String()))
		}
	}
	warningPanelHost = strings.Clone(warningPanelHost)
	warningRunAsUser = strings.Clone(warningRunAsUser)
}

func applyProcessName(name string) {
	if name == "" {
		return
	}
	cloneLiveStrings()
	setComm(name)
	maskCmdline(name)
}

type memSpan struct{ start, end uintptr }

func maskCmdline(name string) {
	spans := argvSpans()
	if len(spans) == 0 {
		return
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	contiguous := true
	for i := 1; i < len(spans); i++ {
		if int64(spans[i].start)-int64(spans[i-1].end) > 1 {
			contiguous = false
			break
		}
	}
	lo, hi := spans[0].start, spans[0].end
	for _, s := range spans[1:] {
		if s.end > hi {
			hi = s.end
		}
	}
	if !contiguous || hi <= lo || hi-lo > 1<<20 {
		maskArgv0(name)
		return
	}
	fillBytes(lo, hi-lo, name)
}

func argvSpans() []memSpan {
	spans := make([]memSpan, 0, len(os.Args))
	for _, a := range os.Args {
		if a == "" {
			continue
		}
		p := uintptr(unsafe.Pointer(unsafe.StringData(a)))
		spans = append(spans, memSpan{p, p + uintptr(len(a))})
	}
	return spans
}

func maskArgv0(name string) {
	if len(os.Args) == 0 || len(os.Args[0]) == 0 {
		return
	}
	p := uintptr(unsafe.Pointer(unsafe.StringData(os.Args[0])))
	fillBytes(p, uintptr(len(os.Args[0])), name)
}

func fillBytes(start, n uintptr, name string) {
	buf := unsafe.Slice((*byte)(unsafe.Pointer(start)), n)
	nb := []byte(name)
	if len(nb) > len(buf) {
		nb = nb[:len(buf)]
	}
	copy(buf, nb)
	for i := len(nb); i < len(buf); i++ {
		buf[i] = 0
	}
}
