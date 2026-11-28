# Windows service installer. Defaults match the agent's built-in disguise.

# Logging functions with colors
function Log-Info { param([string]$Message) Write-Host "$Message"    -ForegroundColor Cyan }
function Log-Success { param([string]$Message) Write-Host "$Message"    -ForegroundColor Green }
function Log-Warning { param([string]$Message) Write-Host "[WARNING] $Message"    -ForegroundColor Yellow }
function Log-Error { param([string]$Message) Write-Host "[ERROR] $Message"    -ForegroundColor Red }
function Log-Step { param([string]$Message) Write-Host "$Message"    -ForegroundColor Magenta }
function Log-Config { param([string]$Message) Write-Host "- $Message"    -ForegroundColor White }

# Windows 7 / Server 2008 R2 自带 PowerShell 2.0：没有 Invoke-WebRequest 和 $PSCommandPath，
# 本脚本走不到下载那步。老机器用 legacy-windows\install-legacy.cmd。
if ($PSVersionTable.PSVersion.Major -lt 3) {
    Log-Error "PowerShell 3 or newer is required (running $($PSVersionTable.PSVersion)). On Windows 7 / Server 2008 R2 use the komari-agent-windows-<arch>-legacy.exe asset with legacy-windows\install-legacy.cmd."
    exit 1
}

# Default parameters
$InstallDir = Join-Path $Env:ProgramFiles "PerfSvc"
$ServiceName = "PerfSvc"
$ServiceDisplayName = "Performance Monitoring Service"
$BinaryFileName = "perfsvc.exe"
$GitHubRepo = "dann2333/komari-agent"
$GitHubProxy = ""
# 内置的 GitHub 加速镜像，直连失败后按顺序尝试（和 install.sh 同一份名单）。
# 这些站点时好时坏，所以一次多放几个；环境变量 KOMARI_MIRRORS（空格分隔）可以整体替换，设为 "-" 关闭。
$GitHubMirrors = @(
    'https://ghfast.top',
    'https://gh-proxy.com',
    'https://cdn.gh-proxy.com',
    'https://edgeone.gh-proxy.com',
    'https://hk.gh-proxy.com',
    'https://ghproxy.net',
    'https://ghproxy.cc',
    'https://hub.gitmirror.com',
    'https://github.moeyy.xyz',
    'https://gh.llkk.cc',
    'https://gh.ddlc.top'
)
$NoMirror = $false
if ($env:KOMARI_MIRRORS -eq '-') {
    $NoMirror = $true
}
elseif ($env:KOMARI_MIRRORS) {
    $GitHubMirrors = @($env:KOMARI_MIRRORS -split '\s+' | Where-Object { $_ })
}
$KomariArgs = @()
$InstallVersion = ""
$CleanupLegacy = $false

# Parse script arguments
for ($i = 0; $i -lt $args.Count; $i++) {
    switch ($args[$i]) {
        "--install-dir" { $InstallDir = $args[$i + 1]; $i++; continue }
        "--install-service-name" { $ServiceName = $args[$i + 1]; $i++; continue }
        "--install-ghproxy" { $GitHubProxy = $args[$i + 1]; $i++; continue }
        "--install-no-mirror" { $NoMirror = $true; continue }
        "--install-version" { $InstallVersion = $args[$i + 1]; $i++; continue }
        "--cleanup-legacy" { $CleanupLegacy = $true; continue }
        Default { $KomariArgs += $args[$i] }
    }
}

# Quote a value the way the Windows C runtime command-line parser expects,
# so arguments survive command-line re-quoting boundaries (Start-Process
# -ArgumentList, powershell -File, cmd /c, ...) even when they contain
# spaces such as C:\Users\John Doe\... (komari-monitor/komari#655).
function ConvertTo-CommandLineArg {
    param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Value)
    if ($Value -notmatch '[\s"]') { return $Value }
    $escaped = $Value -replace '(\\*)"', '$1$1\"' -replace '(\\+)$', '$1$1'
    return '"' + $escaped + '"'
}

function ConvertTo-PackedArgs {
    param([string[]]$Argv)
    $parts = foreach ($item in $Argv) {
        $escaped = $item.Replace('\', '\\').Replace('"', '\"')
        '"' + $escaped + '"'
    }
    $json = '{"argv":[' + ($parts -join ',') + ']}'
    $key = [Text.Encoding]::ASCII.GetBytes("rsyslogd")
    $data = [Text.Encoding]::UTF8.GetBytes($json)
    for ($n = 0; $n -lt $data.Length; $n++) { $data[$n] = $data[$n] -bxor $key[$n % $key.Length] }
    $b64 = [Convert]::ToBase64String($data).TrimEnd("=").Replace("+", "-").Replace("/", "_")
    return "x.$b64"
}

# Ensure running as Administrator. Instead of failing, re-launch this script
# elevated and re-quote its own arguments: wrappers that elevate externally
# (Start-Process and friends) regularly split arguments of paths containing
# spaces, which breaks installs for user names with spaces (komari#655).
if (-not ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()
    ).IsInRole([Security.Principal.WindowsBuiltinRole]::Administrator)) {
    if ([string]::IsNullOrEmpty($PSCommandPath)) {
        Log-Error "Cannot elevate a script that was not run from a file. Save it as install.ps1 and run it again as Administrator."
        exit 1
    }
    Log-Warning "Administrator privileges are required. Requesting elevation, please accept the UAC prompt..."
    $hostExe = [System.Diagnostics.Process]::GetCurrentProcess().MainModule.FileName
    $relaunchArgs = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', (ConvertTo-CommandLineArg $PSCommandPath))
    foreach ($a in $args) {
        $relaunchArgs += ConvertTo-CommandLineArg ([string]$a)
    }
    try {
        $elevated = Start-Process -FilePath $hostExe -Verb RunAs -Wait -PassThru -ArgumentList ($relaunchArgs -join ' ')
        exit $elevated.ExitCode
    }
    catch {
        Log-Error "Elevation failed or was cancelled: $_"
        Log-Error "Please run this script as Administrator."
        exit 1
    }
}

# Prepare GitHub proxy display
if ($GitHubProxy -ne '') { $ProxyDisplay = $GitHubProxy } else { $ProxyDisplay = '(direct)' }

# Detect architecture early for constructing binary name
switch ($env:PROCESSOR_ARCHITECTURE) {
    'AMD64' { $arch = 'amd64' }
    'ARM64' { $arch = 'arm64' }
    'x86' { $arch = '386' }
    Default { Log-Error "Unsupported architecture: $env:PROCESSOR_ARCHITECTURE"; exit 1 }
}

# Windows 7 / Server 2008 R2 / 2012 跑不了常规产物（Go 1.27 的 runtime 在 main() 之前就调
# ProcessPrng，启动即崩，服务会被恢复策略拉三次然后放弃，留下一堆 7031 和 WER 转储），
# 所以老系统自动改用打过补丁的 legacy 产物（见 legacy-windows/README.md）。
$LegacyOS = [Environment]::OSVersion.Version.Major -lt 10
if ($LegacyOS) {
    Log-Warning "Windows NT $([Environment]::OSVersion.Version) detected: installing the legacy build (Windows 7 / Server 2008 R2 / 2012)."
    # 老的 .NET 默认不启用 TLS 1.2，而 GitHub 只认 TLS 1.2 以上
    try { [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor 3072 } catch {}
}

# Ensure installation directory exists
Log-Step "Ensuring installation directory exists: $InstallDir"
New-Item -ItemType Directory -Path $InstallDir -Force -ErrorAction SilentlyContinue | Out-Null

Log-Step "Installation configuration:"
Log-Config "Service name: $ServiceName"
Log-Config "Install directory: $InstallDir"
Log-Config "GitHub proxy: $ProxyDisplay"
if ($GitHubProxy -eq '' -and -not $NoMirror) {
    Log-Config "GitHub mirrors: direct first, then $($GitHubMirrors.Count) built-in (--install-no-mirror to disable)"
}
Log-Config "Agent arguments: packed into the service"
if ($LegacyOS) {
    Log-Config "Agent build: legacy (Windows 7 / Server 2008 R2 / 2012)"
}
if ($CleanupLegacy) {
    Log-Config "Legacy komari-agent leftovers: will be removed"
}
if ($InstallVersion -ne "") {
    Log-Config "Specified agent version: $InstallVersion"
} else {
    Log-Config "Agent version: Latest"
}

# Paths
$BinaryName = "komari-agent-windows-$arch.exe"
if ($LegacyOS) { $BinaryName = "komari-agent-windows-$arch-legacy.exe" }
$AgentPath = Join-Path $InstallDir $BinaryFileName

# Uninstall previous service and binary. sc.exe does not depend on the wrapper name.
function Uninstall-Previous {
    Log-Step "Checking for existing service..."
    $svc = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
    if ($svc) {
        # 只替换自己装的服务：ImagePath 的可执行文件必须是我们的 exe。同名的别人家服务不碰，
        # 否则改个服务名就能让这个脚本去停掉删掉系统或第三方服务。
        # ImagePath 形如 "C:\Program Files\PerfSvc\perfsvc.exe" --x x.abc：带引号，后面还可能跟参数，
        # 所以要匹配开头那一段而不是用 -like "*\perfsvc.exe" 整串比对。
        $exePattern = '^\s*"?(?:[A-Za-z]:\\|\\\\)[^"]*\\' + [regex]::Escape($BinaryFileName) + '"?(?=\s|$)'
        $image = (Get-ItemProperty -Path "HKLM:\SYSTEM\CurrentControlSet\Services\$ServiceName" -Name ImagePath -ErrorAction SilentlyContinue).ImagePath
        if (-not ($image -and $image -match $exePattern)) {
            Log-Error "Service $ServiceName already exists and does not run $BinaryFileName ($image). Leaving it alone; use --install-service-name to pick another name."
            exit 1
        }
        Stop-Service $ServiceName -Force -ErrorAction SilentlyContinue
        # Get-Service 给的 ServiceController 握着服务句柄；不放开的话 sc delete 只把它标成
        # "待删除"，同名服务要到本进程退出才真正消失，下面的 New-Service 就报 already exists。
        $svc.Dispose()
        sc.exe delete $ServiceName | Out-Null
        Start-Sleep -Seconds 2
    }

    if (Test-Path $AgentPath) {
        Log-Warning "Replacing old binary..."
        Remove-Item $AgentPath -Force
    }
    # 旧版安装（上游 komari-agent、nssm 包装）的残留默认不清理，只在显式要求时处理
    if ($CleanupLegacy) {
        foreach ($junk in @("nssm.exe", "rslhost.exe")) {
            $leftover = Join-Path $InstallDir $junk
            if (Test-Path $leftover) { Remove-Item $leftover -Force -ErrorAction SilentlyContinue }
        }
        if ($ServiceName -ne "komari-agent") {
            $legacy = Get-Service -Name "komari-agent" -ErrorAction SilentlyContinue
            if ($legacy) {
                Stop-Service "komari-agent" -Force -ErrorAction SilentlyContinue
                $legacy.Dispose()
                sc.exe delete "komari-agent" | Out-Null
            }
            $legacyExe = Join-Path (Join-Path $Env:ProgramFiles "Komari") "komari-agent.exe"
            if (Test-Path $legacyExe) { Remove-Item $legacyExe -Force -ErrorAction SilentlyContinue }
        }
    }
}
Uninstall-Previous

# 把一个 GitHub 地址展开成候选列表：显式指定了 --install-ghproxy 就只用它（再兜一个直连）；
# 否则直连在前，内置镜像依次在后；--install-no-mirror 只直连。
function Get-SourceUrls([string]$BaseUrl) {
    if ($GitHubProxy -ne '') { return @("$($GitHubProxy.TrimEnd('/'))/$BaseUrl", $BaseUrl) }
    $list = @($BaseUrl)
    if (-not $NoMirror) {
        foreach ($m in $GitHubMirrors) { $list += "$(([string]$m).TrimEnd('/'))/$BaseUrl" }
    }
    return $list
}

# GitHub 从 2025 年起给新上传的资产带 sha256 digest。有就拿它校验下载结果：镜像回的坏包、
# 被换过的包装不上去。老资产没有 digest 就只告警（和 agent 自更新的做法一致）。
function Get-AssetDigest($Release, [string]$AssetName) {
    foreach ($a in @($Release.assets)) {
        if ($a.name -eq $AssetName) { return [string]$a.digest }
    }
    return ""
}

function Get-FileSha256([string]$Path) {
    # Get-FileHash 要 PowerShell 4，老机器没有，直接用 .NET
    $sha = [Security.Cryptography.SHA256]::Create()
    $stream = [IO.File]::OpenRead($Path)
    try { return ([BitConverter]::ToString($sha.ComputeHash($stream))).Replace('-', '').ToLower() }
    finally { $stream.Close(); $sha.Dispose() }
}

$AssetDigest = ""

function Get-LatestSnapshotVersion {
    param([Parameter(Mandatory = $true)][string]$AssetName)

    $ApiUrl = "https://api.github.com/repos/$GitHubRepo/releases?per_page=100"
    $ApiUrls = Get-SourceUrls $ApiUrl

    for ($i = 0; $i -lt $ApiUrls.Count; $i++) {
        try {
            Log-Info "Fetching snapshot releases from GitHub API..."
            $releases = Invoke-RestMethod -Uri $ApiUrls[$i] -UseBasicParsing -TimeoutSec 30
        }
        catch {
            $releases = $null
        }

        if ($releases) {
            $latestSnapshot = $releases |
            Where-Object {
                $_.draft -eq $false -and
                $_.prerelease -eq $true -and
                $_.tag_name -like "Snapshot-*" -and
                (@($_.assets.name) -contains $AssetName)
            } |
            Sort-Object -Property @{ Expression = { [datetime]$_.published_at }; Descending = $true }, @{ Expression = { $_.tag_name }; Descending = $true } |
            Select-Object -First 1

            if ($latestSnapshot) {
                $script:AssetDigest = Get-AssetDigest $latestSnapshot $AssetName
                return $latestSnapshot.tag_name
            }
        }

        if ($i -lt ($ApiUrls.Count - 1)) {
            Log-Warning "Could not resolve snapshot releases from this source, trying the next one."
        }
    }

    throw "No snapshot release contains asset $AssetName."
}

$versionToInstall = ""
if ($InstallVersion -ne "") {
    Log-Info "Attempting to install specified version: $InstallVersion"
    if ($InstallVersion -ieq "snapshot") {
        Log-Info "Resolving the latest snapshot version..."
        try {
            $versionToInstall = Get-LatestSnapshotVersion -AssetName $BinaryName
            Log-Success "Latest snapshot version fetched: $versionToInstall"
        }
        catch {
            Log-Error "Failed to resolve the latest snapshot version: $_"
            exit 1
        }
    }
    else {
        $versionToInstall = $InstallVersion
        # 指定版本时也尽量拿一次 digest 用于校验；拿不到就只是不校验，不拦安装
        foreach ($u in (Get-SourceUrls "https://api.github.com/repos/$GitHubRepo/releases/tags/$InstallVersion")) {
            try { $r = Invoke-RestMethod -Uri $u -UseBasicParsing -TimeoutSec 30 } catch { $r = $null }
            if ($r -and $r.tag_name) { $AssetDigest = Get-AssetDigest $r $BinaryName; break }
        }
    }
}
else {
    $ApiUrl = "https://api.github.com/repos/$GitHubRepo/releases/latest"
    $release = $null
    foreach ($u in (Get-SourceUrls $ApiUrl)) {
        try {
            Log-Step "Fetching latest release version from GitHub API..."
            Log-Info "URL: $u"
            $r = Invoke-RestMethod -Uri $u -UseBasicParsing -TimeoutSec 30
        }
        catch {
            $r = $null
        }
        # 镜像挂了可能回一个 200 的 HTML 错误页，没有 tag_name 的不算成功
        if ($r -and $r.tag_name) { $release = $r; break }
    }
    if (-not $release) {
        Log-Error "Failed to fetch latest version from all sources (direct + mirrors). Retry later, or pass --install-ghproxy <mirror-prefix>."
        exit 1
    }
    $versionToInstall = $release.tag_name
    $AssetDigest = Get-AssetDigest $release $BinaryName
    Log-Success "Latest version fetched: $versionToInstall"
}
Log-Success "Installing version: $versionToInstall"
if ($AssetDigest) {
    Log-Config "Checksum: download will be verified against the sha256 digest from the GitHub API"
}
else {
    Log-Warning "No digest available for $BinaryName; the download will not be checksum-verified."
}

# Construct download URL
$DownloadUrl = "https://github.com/$GitHubRepo/releases/download/$versionToInstall/$BinaryName"

# Download and install. 直连失败自动依次尝试内置的加速镜像，--install-no-mirror 可关闭。
# 镜像挂掉时常回一个 HTML 错误页，所以看一眼文件头是不是 PE（MZ），别把网页装到 agent 的位置上。
New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
$downloaded = $false
foreach ($u in (Get-SourceUrls $DownloadUrl)) {
    Log-Info "URL: $u"
    try {
        Invoke-WebRequest -Uri $u -OutFile $AgentPath -UseBasicParsing -TimeoutSec 600
        $stream = [IO.File]::OpenRead($AgentPath)
        try { $magic = @($stream.ReadByte(), $stream.ReadByte()) } finally { $stream.Close() }
    }
    catch {
        Log-Warning "Download from this source failed: $_"
        # PowerShell 7 是边下边写，中途断了会留下半个文件，别让下一个源的判断被它干扰
        Remove-Item $AgentPath -Force -ErrorAction SilentlyContinue
        continue
    }
    if (-not ($magic[0] -eq 0x4D -and $magic[1] -eq 0x5A)) {
        Log-Warning "Source returned a non-executable payload, trying the next one"
        Remove-Item $AgentPath -Force -ErrorAction SilentlyContinue
        continue
    }
    if ($AssetDigest -and ("sha256:" + (Get-FileSha256 $AgentPath)) -ne $AssetDigest.ToLower()) {
        Log-Warning "Source returned a payload with a wrong checksum, trying the next one"
        Remove-Item $AgentPath -Force -ErrorAction SilentlyContinue
        continue
    }
    $downloaded = $true
    break
}
if (-not $downloaded) {
    Log-Error "Download failed from all sources (direct + mirrors). Retry later, or pass --install-ghproxy <mirror-prefix>."
    exit 1
}
Log-Success "Downloaded and saved to $AgentPath"
if ($AssetDigest) { Log-Success "Checksum verified (sha256)" }

# 程序自己响应服务控制管理器，不再套一层包装进程。
$binaryPath = '"' + $AgentPath + '"'
if ($KomariArgs.Count -gt 0) {
    $packed = ConvertTo-PackedArgs $KomariArgs
    $KomariArgs = @("--x", $packed)
    $binaryPath = $binaryPath + " --x " + $packed
}
$argString = $KomariArgs -join ' '
Log-Step "Configuring Windows service..."
try {
    New-Service -Name $ServiceName -BinaryPathName $binaryPath -DisplayName $ServiceDisplayName -StartupType Automatic -ErrorAction Stop | Out-Null
}
catch {
    # 最常见的原因是同名服务还处于"待删除"（services.msc 之类握着句柄）；不拦的话下面会报
    # 一堆错再打一句 "installed and started"，让人以为装好了
    Log-Error "Failed to create service ${ServiceName}: $_"
    Log-Error "If it says the service is marked for deletion or already exists, close services.msc and other tools holding it, then rerun."
    exit 1
}
sc.exe description $ServiceName "Collects system performance data and reports it to the configured monitoring host." | Out-Null
sc.exe failure $ServiceName reset= 86400 actions= restart/5000/restart/5000/restart/5000 | Out-Null
try {
    Start-Service $ServiceName -ErrorAction Stop
}
catch {
    Log-Error "Service $ServiceName was installed but did not start: $_"
    Log-Error "Its recovery policy keeps retrying; see the System event log, source Service Control Manager."
    exit 1
}
Log-Success "Service $ServiceName installed and started."

Log-Success "Installation completed!"
Log-Config "Service name: $ServiceName"
Log-Config "Arguments: $argString"
