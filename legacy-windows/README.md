# 老系统 Windows 产物线（Windows 7 / Server 2008 / 2008 R2 / 2012）

常规产物用 Go 1.27 编，Go 1.21 起的 runtime 只支持 Windows 10 / Server 2016：它在
`main()` 之前就调 `ProcessPrng`（bcryptprimitives.dll，Windows 8 才有）并用
`LoadLibraryEx` + `LOAD_LIBRARY_SEARCH_SYSTEM32`（Windows 7 要 KB2533623），在老系统上
表现为**进程启动即崩**，服务会被恢复策略拉起三次然后放弃，留下 7031 和 WER 转储。

这里用 **Go 1.24.13**（和 go.mod / uTLS 1.8.2 的最低要求一致）编一条单独的产物线，
只给 runtime 加两个回退（`patch.py`，不改别的东西）：

- 随机数：有 `ProcessPrng` 用它，没有就用 `RtlGenRandom`（advapi32!`SystemFunction036`）
- 系统 DLL：`LoadLibraryExW` + `SEARCH_SYSTEM32` 失败就用系统目录绝对路径 `LoadLibraryW`

产物构建时用 `-X update.legacyAssetSuffix=-legacy` 注入资产名后缀：自更新只找
`komari-agent-windows-<arch>-legacy.exe`，不会把常规产物装到老机器上。

| 文件 | 作用 |
|---|---|
| `patch.py` | 给原版 Go 源码打补丁；`--force-fallback` 让回退路径无条件生效，用来在新系统上验证 |
| `toolchain.sh <原版 GOROOT> <目标目录>` | 复制一份原版 Go 1.24.13，打补丁，用它自己当 bootstrap 重编，再装 garble v0.14.2（最后一个支持 1.24 的） |
| `build.sh <工具链根> <输出> [GOARCH] [版本]` | garble 混淆编 Windows 产物 |
| `install-legacy.cmd` | 老系统只有 PowerShell 2.0 跑不了 install.ps1（2008 R2 / Win7 自带），用 cmd 装服务（参数照常 `-t`/`-e`，agent 自己打成 `--x`） |

安装脚本怎么选：`install.ps1` 在 PowerShell < 3 的机器上直接报错指向 `install-legacy.cmd`；
在 Windows NT < 10（7 / 2008 R2 / 2012 / 2012 R2）上自动改下 `komari-agent-windows-<arch>-legacy.exe`，
并给老的 .NET 打开 TLS 1.2 再去 GitHub 下载。`install-legacy.cmd` 不判断系统，哪个版本都能用。

CI：`.github/workflows/build.yml` / `snapshot.yml` 里的 `build-legacy-windows` job，
产物 `komari-agent-windows-{amd64,386}-legacy.exe` 和其它资产一起进 Snapshot。

## 验证过什么

- 用 `--force-fallback` 的工具链编出来的测试二进制在 Windows 11 上跑：`cmd` `server`
  `dnsresolver` `internal/tlsfp` `update` `monitoring/unit` `monitoring/netstatic` `terminal`
  全过；agent 本体 `--pack`、带 `--x` 常驻无异常——这就是 Windows 7 实际走的那两条路径。
- 本目录的 `toolchain.sh` + `build.sh` 从原版 1.24.13 完整跑通（和 CI 一样的调用），
  amd64 / 386 产物在 Windows 11 上正常，`-legacy` 后缀注入生效。
- `install-legacy.cmd` 在 Windows 11 上用 sc.exe / net.exe 替身完整空跑过（不真建服务）：首次安装、
  覆盖自己的旧服务（stop → delete → 等一拍 → create）、同名但不是自己的服务（拒绝）、`sc create`
  和 `net start` 失败（非零退出），并核对 `sc.exe` 收到的 binPath 正是 `"…\perfsvc.exe" --x <打包值>`。
  文件在仓库里是 CRLF（`.gitattributes` 里标了 `-text`），cmd 对 LF-only 的 goto 标签不可靠。
- **还没在真的 2008 R2 上跑过**，上真机前先在目标上手动跑一次 `perfsvc.exe --pack -t x`
  （能打印 `x.` 开头的值就说明 runtime 已经起来了），再装服务。

## 老机器上的已知差异

- ConPTY 是 Windows 10 1809 才有的：老系统的 Web 终端起不来（`ErrConPtyUnsupported`），任务执行和上报不受影响
- 任务执行走系统自带的 PowerShell 2.0，`-File` 和 `-ExecutionPolicy Bypass` 都有；`[Console]::OutputEncoding` 在 .NET 3.5 里也有
- 2008 R2 的 SChannel 默认没有 TLS 1.2，**从机器上直接下载 agent 大概率失败**，二进制要用别的方式送上去；agent 自身的 TLS 不依赖 SChannel（uTLS + 内置根证书）
- Windows Server 2003 / XP 不在范围内（Go 从 1.11 起就不再支持）

## 本地复现（WSL）

```sh
# 原版 Go 1.24.13：模块代理上的工具链 zip 即可（里面带 src/）
mkdir -p ~/legacy/stock && cd ~/legacy/stock
curl -o tc.zip https://goproxy.cn/golang.org/toolchain/@v/v0.0.1-go1.24.13.linux-amd64.zip
python3 -c "import zipfile; zipfile.ZipFile('tc.zip').extractall('.')"
mv golang.org/toolchain@v0.0.1-go1.24.13.linux-amd64 go && rmdir golang.org
cd <repo>
bash legacy-windows/toolchain.sh ~/legacy/stock/go ~/legacy/go-legacy
bash legacy-windows/build.sh ~/legacy/go-legacy /tmp/komari-agent-windows-amd64-legacy.exe amd64 dev
```
