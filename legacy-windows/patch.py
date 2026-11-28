#!/usr/bin/env python3
"""Turns a stock Go 1.24.x tree into one whose Windows binaries also run on
Windows 7 / Server 2008 / 2008 R2. Go >= 1.21.5 uses ProcessPrng
(bcryptprimitives.dll, Windows 8+) and LoadLibraryEx with
LOAD_LIBRARY_SEARCH_SYSTEM32 (needs KB2533623 on Windows 7); both crash the
process before main() on those systems. This adds fallbacks, nothing else:
  - random data: ProcessPrng, else RtlGenRandom (advapi32!SystemFunction036)
  - system DLLs : LoadLibraryExW + SEARCH_SYSTEM32, else LoadLibraryW of the
                  absolute path inside the system directory
Usage:
    python3 patch.py --root <GOROOT> [--force-fallback]
--force-fallback makes the legacy paths unconditional so they can be exercised
on a modern Windows box (see README); release toolchains must not use it.
"""
import argparse
import sys
from pathlib import Path

ap = argparse.ArgumentParser()
ap.add_argument("--root", default=str(Path(__file__).resolve().parent))
ap.add_argument("--force-fallback", action="store_true")
args = ap.parse_args()
ROOT = Path(args.root).resolve()
FORCE = args.force_fallback


def edit(path, old, new, count=1):
    p = ROOT / path
    # Go 源码是 UTF-8，别跟着机器 locale 走
    s = p.read_text(encoding="utf-8")
    if s.count(old) != count:
        sys.exit(f"{path}: expected {count} match(es) of:\n{old}\nfound {s.count(old)}")
    p.write_text(s.replace(old, new), encoding="utf-8")
    print(f"patched {path}")


OS_WIN = "src/runtime/os_windows.go"
edit(OS_WIN,
     "\t// Use ProcessPrng to generate cryptographically random data.\n"
     "\t_ProcessPrng stdFunction\n",
     "\t// Use ProcessPrng to generate cryptographically random data.\n"
     "\t// Windows 7 / Server 2008 R2 ship bcryptprimitives.dll without ProcessPrng;\n"
     "\t// there we fall back to RtlGenRandom (advapi32!SystemFunction036).\n"
     "\t_ProcessPrng  stdFunction\n"
     "\t_RtlGenRandom stdFunction\n")
edit(OS_WIN,
     "\tbcryptprimitivesdll = [...]uint16{'b', 'c', 'r', 'y', 'p', 't', 'p', 'r', 'i', 'm', 'i', 't', 'i', 'v', 'e', 's', '.', 'd', 'l', 'l', 0}\n",
     "\tbcryptprimitivesdll = [...]uint16{'b', 'c', 'r', 'y', 'p', 't', 'p', 'r', 'i', 'm', 'i', 't', 'i', 'v', 'e', 's', '.', 'd', 'l', 'l', 0}\n"
     "\tadvapi32dll         = [...]uint16{'a', 'd', 'v', 'a', 'p', 'i', '3', '2', '.', 'd', 'l', 'l', 0}\n")
edit(OS_WIN,
     "func windowsLoadSystemLib(name []uint16) uintptr {\n"
     "\treturn stdcall3(_LoadLibraryExW, uintptr(unsafe.Pointer(&name[0])), 0, _LOAD_LIBRARY_SEARCH_SYSTEM32)\n"
     "}\n",
     "// legacyWinForceFallback exercises the Windows 7 code paths on newer systems.\n"
     f"const legacyWinForceFallback = {'true' if FORCE else 'false'}\n"
     "\n"
     "func windowsLoadSystemLib(name []uint16) uintptr {\n"
     "\tif !legacyWinForceFallback {\n"
     "\t\th := stdcall3(_LoadLibraryExW, uintptr(unsafe.Pointer(&name[0])), 0, _LOAD_LIBRARY_SEARCH_SYSTEM32)\n"
     "\t\tif h != 0 {\n"
     "\t\t\treturn h\n"
     "\t\t}\n"
     "\t}\n"
     "\t// LOAD_LIBRARY_SEARCH_SYSTEM32 is rejected on Windows 7 / Server 2008 R2\n"
     "\t// without KB2533623; load by absolute path from the system directory instead.\n"
     "\tif sysDirectoryLen == 0 {\n"
     "\t\tinitSysDirectory()\n"
     "\t}\n"
     "\tvar abs [_MAX_PATH + 2]uint16\n"
     "\tn := 0\n"
     "\tfor _, c := range sysDirectory[:sysDirectoryLen] {\n"
     "\t\tabs[n] = uint16(c)\n"
     "\t\tn++\n"
     "\t}\n"
     "\tif n+len(name) >= len(abs) {\n"
     "\t\treturn 0\n"
     "\t}\n"
     "\tn += copy(abs[n:], name)\n"
     "\tabs[n] = 0\n"
     "\treturn stdcall1(_LoadLibraryW, uintptr(unsafe.Pointer(&abs[0])))\n"
     "}\n")
edit(OS_WIN,
     "\t_ProcessPrng = windowsFindfunc(bcryptPrimitives, []byte(\"ProcessPrng\\000\"))\n",
     "\t_ProcessPrng = windowsFindfunc(bcryptPrimitives, []byte(\"ProcessPrng\\000\"))\n"
     "\tif _ProcessPrng == nil || legacyWinForceFallback {\n"
     "\t\ta32 := windowsLoadSystemLib(advapi32dll[:])\n"
     "\t\tif a32 == 0 {\n"
     "\t\t\tthrow(\"advapi32.dll not found\")\n"
     "\t\t}\n"
     "\t\t_RtlGenRandom = windowsFindfunc(a32, []byte(\"SystemFunction036\\000\"))\n"
     "\t\tif _RtlGenRandom == nil {\n"
     "\t\t\tthrow(\"SystemFunction036 not found\")\n"
     "\t\t}\n"
     "\t}\n")
edit(OS_WIN,
     "//go:nosplit\n"
     "func readRandom(r []byte) int {\n"
     "\tn := 0\n"
     "\tif stdcall2(_ProcessPrng, uintptr(unsafe.Pointer(&r[0])), uintptr(len(r)))&0xff != 0 {\n"
     "\t\tn = len(r)\n"
     "\t}\n"
     "\treturn n\n"
     "}\n",
     "//go:nosplit\n"
     "func readRandom(r []byte) int {\n"
     "\tn := 0\n"
     "\tif _ProcessPrng != nil && !legacyWinForceFallback {\n"
     "\t\tif stdcall2(_ProcessPrng, uintptr(unsafe.Pointer(&r[0])), uintptr(len(r)))&0xff != 0 {\n"
     "\t\t\tn = len(r)\n"
     "\t\t}\n"
     "\t\treturn n\n"
     "\t}\n"
     "\t// RtlGenRandom returns at most 1<<30 bytes per call.\n"
     "\tfor n < len(r) {\n"
     "\t\tl := len(r) - n\n"
     "\t\tif l > 1<<30 {\n"
     "\t\t\tl = 1 << 30\n"
     "\t\t}\n"
     "\t\tif stdcall2(_RtlGenRandom, uintptr(unsafe.Pointer(&r[n])), uintptr(l))&0xff == 0 {\n"
     "\t\t\treturn n\n"
     "\t\t}\n"
     "\t\tn += l\n"
     "\t}\n"
     "\treturn n\n"
     "}\n")

SYS_WIN = "src/runtime/syscall_windows.go"
edit(SYS_WIN,
     "func syscall_loadsystemlibrary(filename *uint16) (handle, err uintptr) {\n"
     "\thandle, _, err = syscall_SyscallN(uintptr(unsafe.Pointer(_LoadLibraryExW)), uintptr(unsafe.Pointer(filename)), 0, _LOAD_LIBRARY_SEARCH_SYSTEM32)\n",
     "func syscall_loadsystemlibrary(filename *uint16) (handle, err uintptr) {\n"
     "\tif !legacyWinForceFallback {\n"
     "\t\thandle, _, err = syscall_SyscallN(uintptr(unsafe.Pointer(_LoadLibraryExW)), uintptr(unsafe.Pointer(filename)), 0, _LOAD_LIBRARY_SEARCH_SYSTEM32)\n"
     "\t}\n"
     "\tif handle == 0 {\n"
     "\t\t// Windows 7 / Server 2008 R2 without KB2533623 reject the flag above;\n"
     "\t\t// load by absolute path from the system directory instead.\n"
     "\t\tif sysDirectoryLen == 0 {\n"
     "\t\t\tinitSysDirectory()\n"
     "\t\t}\n"
     "\t\tvar abs [_MAX_PATH + 2]uint16\n"
     "\t\tn := 0\n"
     "\t\tfor _, c := range sysDirectory[:sysDirectoryLen] {\n"
     "\t\t\tabs[n] = uint16(c)\n"
     "\t\t\tn++\n"
     "\t\t}\n"
     "\t\tchars := unsafe.Slice(filename, len(abs)-n-1)\n"
     "\t\tfor _, c := range chars {\n"
     "\t\t\tabs[n] = c\n"
     "\t\t\tn++\n"
     "\t\t\tif c == 0 {\n"
     "\t\t\t\tbreak\n"
     "\t\t\t}\n"
     "\t\t}\n"
     "\t\tif n > 0 && abs[n-1] == 0 {\n"
     "\t\t\thandle, _, err = syscall_SyscallN(uintptr(unsafe.Pointer(_LoadLibraryW)), uintptr(unsafe.Pointer(&abs[0])), 0, 0)\n"
     "\t\t}\n"
     "\t}\n")

ZSYS = "src/internal/syscall/windows/zsyscall_windows.go"
edit(ZSYS,
     "\tprocProcessPrng                       = modbcryptprimitives.NewProc(\"ProcessPrng\")\n",
     "\tprocProcessPrng                       = modbcryptprimitives.NewProc(\"ProcessPrng\")\n"
     "\tprocSystemFunction036                 = modadvapi32.NewProc(\"SystemFunction036\")\n")
edit(ZSYS,
     "func ProcessPrng(buf []byte) (err error) {\n"
     "\tvar _p0 *byte\n",
     "func ProcessPrng(buf []byte) (err error) {\n"
     "\tif err := procProcessPrng.Find(); err != nil {\n"
     "\t\t// Windows 7 / Server 2008 R2: bcryptprimitives.dll has no ProcessPrng.\n"
     "\t\treturn RtlGenRandom(buf)\n"
     "\t}\n"
     "\tvar _p0 *byte\n")
edit(ZSYS,
     "func ProcessPrng(buf []byte) (err error) {\n",
     "// RtlGenRandom is advapi32.dll's SystemFunction036, the randomness source\n"
     "// Go used before ProcessPrng existed.\n"
     "func RtlGenRandom(buf []byte) (err error) {\n"
     "\tvar _p0 *byte\n"
     "\tif len(buf) > 0 {\n"
     "\t\t_p0 = &buf[0]\n"
     "\t}\n"
     "\tr1, _, e1 := syscall.Syscall(procSystemFunction036.Addr(), 2, uintptr(unsafe.Pointer(_p0)), uintptr(len(buf)), 0)\n"
     "\tif r1 == 0 {\n"
     "\t\terr = errnoErr(e1)\n"
     "\t}\n"
     "\treturn\n"
     "}\n"
     "\n"
     "func ProcessPrng(buf []byte) (err error) {\n",
     count=1)
print(f"patched {ROOT} (force-fallback: {FORCE})")
