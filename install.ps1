# Windows service installer. Defaults match the agent's built-in disguise.

# Logging functions with colors
function Log-Info { param([string]$Message) Write-Host "$Message"    -ForegroundColor Cyan }
function Log-Success { param([string]$Message) Write-Host "$Message"    -ForegroundColor Green }
function Log-Warning { param([string]$Message) Write-Host "[WARNING] $Message"    -ForegroundColor Yellow }
function Log-Error { param([string]$Message) Write-Host "[ERROR] $Message"    -ForegroundColor Red }
function Log-Step { param([string]$Message) Write-Host "$Message"    -ForegroundColor Magenta }
function Log-Config { param([string]$Message) Write-Host "- $Message"    -ForegroundColor White }

# Default parameters
$InstallDir = Join-Path $Env:ProgramFiles "Rsyslog"
$ServiceName = "rsyslogd"
$ServiceDisplayName = "System Logging Service"
$GitHubRepo = "dann2333/komari-agent"
$GitHubProxy = ""
$KomariArgs = @()
$InstallVersion = ""

# Parse script arguments
for ($i = 0; $i -lt $args.Count; $i++) {
    switch ($args[$i]) {
        "--install-dir" { $InstallDir = $args[$i + 1]; $i++; continue }
        "--install-service-name" { $ServiceName = $args[$i + 1]; $i++; continue }
        "--install-ghproxy" { $GitHubProxy = $args[$i + 1]; $i++; continue }
        "--install-version" { $InstallVersion = $args[$i + 1]; $i++; continue }
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

# Ensure installation directory exists
Log-Step "Ensuring installation directory exists: $InstallDir"
New-Item -ItemType Directory -Path $InstallDir -Force -ErrorAction SilentlyContinue | Out-Null

Log-Step "Installation configuration:"
Log-Config "Service name: $ServiceName"
Log-Config "Install directory: $InstallDir"
Log-Config "GitHub proxy: $ProxyDisplay"
Log-Config "Agent arguments: packed into the service"
if ($InstallVersion -ne "") {
    Log-Config "Specified agent version: $InstallVersion"
} else {
    Log-Config "Agent version: Latest"
}

# Paths
$BinaryName = "komari-agent-windows-$arch.exe"
$AgentPath = Join-Path $InstallDir "rsyslogd.exe"

# Uninstall previous service and binary. sc.exe does not depend on the wrapper name.
function Uninstall-Previous {
    Log-Step "Checking for existing service..."
    $svc = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
    if ($svc) {
        Stop-Service $ServiceName -Force -ErrorAction SilentlyContinue
        sc.exe delete $ServiceName | Out-Null
        Start-Sleep -Seconds 2
    }

    if (Test-Path $AgentPath) {
        Log-Warning "Removing old binary..."
        Remove-Item $AgentPath -Force
    }
    foreach ($junk in @("nssm.exe", "rslhost.exe")) {
        $leftover = Join-Path $InstallDir $junk
        if (Test-Path $leftover) { Remove-Item $leftover -Force -ErrorAction SilentlyContinue }
    }
    if ($ServiceName -ne "komari-agent") {
        $legacy = Get-Service -Name "komari-agent" -ErrorAction SilentlyContinue
        if ($legacy) {
            Stop-Service "komari-agent" -Force -ErrorAction SilentlyContinue
            sc.exe delete "komari-agent" | Out-Null
        }
        $legacyExe = Join-Path (Join-Path $Env:ProgramFiles "Komari") "komari-agent.exe"
        if (Test-Path $legacyExe) { Remove-Item $legacyExe -Force -ErrorAction SilentlyContinue }
    }
}
Uninstall-Previous

function Get-LatestSnapshotVersion {
    param([Parameter(Mandatory = $true)][string]$AssetName)

    $ApiUrl = "https://api.github.com/repos/$GitHubRepo/releases?per_page=100"
    $ApiUrls = @($ApiUrl)
    if ($GitHubProxy -ne "") {
        $ApiUrls = @("$GitHubProxy/$ApiUrl", $ApiUrl)
    }

    for ($i = 0; $i -lt $ApiUrls.Count; $i++) {
        try {
            Log-Info "Fetching snapshot releases from GitHub API..."
            $releases = Invoke-RestMethod -Uri $ApiUrls[$i] -UseBasicParsing
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
                return $latestSnapshot.tag_name
            }
        }

        if ($i -lt ($ApiUrls.Count - 1)) {
            Log-Warning "Failed to resolve snapshot releases through GitHub proxy, retrying directly."
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
    }
}
else {
    $ApiUrl = "https://api.github.com/repos/$GitHubRepo/releases/latest"
    try {
        Log-Step "Fetching latest release version from GitHub API..."
        $release = Invoke-RestMethod -Uri $ApiUrl -UseBasicParsing
        $versionToInstall = $release.tag_name
        Log-Success "Latest version fetched: $versionToInstall"
    }
    catch {
        Log-Error "Failed to fetch latest version: $_"
        exit 1
    }
}
Log-Success "Installing version: $versionToInstall"

# Construct download URL
$BinaryName = "komari-agent-windows-$arch.exe"
$DownloadUrl = if ($GitHubProxy) { "$GitHubProxy/https://github.com/$GitHubRepo/releases/download/$versionToInstall/$BinaryName" } else { "https://github.com/$GitHubRepo/releases/download/$versionToInstall/$BinaryName" }

# Download and install
New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
Log-Info "URL: $DownloadUrl"
try {
    Invoke-WebRequest -Uri $DownloadUrl -OutFile $AgentPath -UseBasicParsing
}
catch {
    Log-Error "Download failed: $_"
    exit 1
}
Log-Success "Downloaded and saved to $AgentPath"

# 程序自己响应服务控制管理器，不再套一层包装进程。
$binaryPath = '"' + $AgentPath + '"'
if ($KomariArgs.Count -gt 0) {
    $packed = ConvertTo-PackedArgs $KomariArgs
    $KomariArgs = @("--x", $packed)
    $binaryPath = $binaryPath + " --x " + $packed
}
$argString = $KomariArgs -join ' '
Log-Step "Configuring Windows service..."
New-Service -Name $ServiceName -BinaryPathName $binaryPath -DisplayName $ServiceDisplayName -StartupType Automatic | Out-Null
sc.exe description $ServiceName "Provides support for system event logging." | Out-Null
sc.exe failure $ServiceName reset= 86400 actions= restart/5000/restart/5000/restart/5000 | Out-Null
Start-Service $ServiceName
Log-Success "Service $ServiceName installed and started."

Log-Success "Installation completed!"
Log-Config "Service name: $ServiceName"
Log-Config "Arguments: $argString"
