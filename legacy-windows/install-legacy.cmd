@echo off
rem Windows 7 / Server 2008 / 2008 R2 / 2012 installer. Those systems ship
rem PowerShell 2.0, which cannot run install.ps1 (no Invoke-WebRequest), so the
rem service is installed from cmd. Keep this file ASCII with CRLF line endings:
rem cmd reads batch files in the OEM code page, chokes on UTF-8 comments, and
rem has been seen to miss goto labels in LF-only files.
rem
rem Take komari-agent-windows-<arch>-legacy.exe from the release, rename it to
rem perfsvc.exe and put it next to this script. Run from an elevated cmd:
rem   install-legacy.cmd -t <token> -e <https://panel:443> [more agent args...]
rem The agent packs the arguments into --x itself, so the service command line
rem only shows the packed value. Only our own PerfSvc service (ImagePath points
rem at perfsvc.exe) gets replaced; anything else with that name is left alone.
rem No parenthesized blocks on purpose: cmd does not delay-expand variables
rem inside them, so PACKED would read back empty.
setlocal DisableDelayedExpansion
set NAME=PerfSvc
set DISPLAY=Performance Monitoring Service
set DIR=%ProgramFiles%\PerfSvc
set EXE=%DIR%\perfsvc.exe
set ARGS=%*
set PACKED=

if not exist "%~dp0perfsvc.exe" goto :noexe
sc qc %NAME% >nul 2>&1
if errorlevel 1 goto :install
sc qc %NAME% | find /i "%EXE%" >nul
if errorlevel 1 goto :notours
net stop %NAME% >nul 2>&1
sc delete %NAME% >nul 2>&1
rem Give the SCM a moment to drop the deleted service record before recreating
rem it. ping instead of timeout.exe: timeout exits at once when stdin is redirected.
ping -n 3 127.0.0.1 >nul

:install
if not exist "%DIR%" mkdir "%DIR%"
copy /y "%~dp0perfsvc.exe" "%EXE%" >nul
if errorlevel 1 goto :copyfail
if "%ARGS%"=="" goto :create
"%EXE%" --pack %ARGS% > "%TEMP%\perfsvc-packed.txt"
set /p PACKED=<"%TEMP%\perfsvc-packed.txt"
del /q "%TEMP%\perfsvc-packed.txt" >nul 2>&1
if "%PACKED%"=="" goto :packfail
set PACKED= --x %PACKED%

:create
sc create %NAME% binPath= "\"%EXE%\"%PACKED%" start= auto DisplayName= "%DISPLAY%"
if errorlevel 1 exit /b 1
sc description %NAME% "Collects system performance data and reports it to the configured monitoring host."
sc failure %NAME% reset= 86400 actions= restart/5000/restart/5000/restart/5000
net start %NAME%
if errorlevel 1 goto :startfail
echo [OK] Service %NAME% installed and started.
exit /b 0

:noexe
echo [ERROR] Rename the legacy asset to perfsvc.exe and put it next to this script.
exit /b 1
:notours
echo [ERROR] Service %NAME% already exists and does not run %EXE%. Leaving it alone.
exit /b 1
:copyfail
echo [ERROR] Could not write %EXE% (file still in use or not elevated?). Run from an elevated cmd.
exit /b 1
:packfail
echo [ERROR] Failed to pack the arguments. On Windows 7 / Server 2008 R2 perfsvc.exe must be the -legacy asset.
exit /b 1
:startfail
echo [ERROR] Service %NAME% was installed but did not start. See the System event log, source Service Control Manager.
exit /b 1
