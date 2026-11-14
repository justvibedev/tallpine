<#
.SYNOPSIS
Silent-install smoke test for the Tallpine Windows beta installer.

.DESCRIPTION
Installs the given installer silently into a scratch directory, starts the
bundled daemon on a scratch data dir and port, and checks that:
  1. the install laid down tallpine.exe and resources\daemon\tallpine.exe,
  2. the daemon answers GET /healthz as tallpine-daemon,
  3. a shell terminal (ConPTY on Windows) opens, is listed, and closes.
Then it stops the daemon and uninstalls silently. Its data lives under a temp
directory and it never touches ~/.tallpine.

Run it where Tallpine is NOT installed (CI, or the VM before the manual E2E
install): the installer shares the real app's appId, so NSIS would uninstall
an existing Tallpine and close a running one. The script refuses to start
when it finds an installed or running Tallpine, unless -ReplaceExistingInstall
is passed.

Exit code 0 means every check passed.

.EXAMPLE
pwsh scripts/windows/smoke.ps1 -Installer "frontend\out\windows-beta\Tallpine Setup 0.13.14-beta.1.exe"
#>
#Requires -Version 7
[CmdletBinding()]
param(
	[Parameter(Mandatory = $true)][string]$Installer,
	# Loopback port for the smoke daemon; away from the app's 3101 and dev 3102.
	[int]$Port = 3191,
	# Seconds to wait for /healthz.
	[int]$TimeoutSeconds = 90,
	# Leave the install and scratch data in place for inspection.
	[switch]$Keep,
	# Run even though Tallpine is installed or running; that install is replaced and then removed.
	[switch]$ReplaceExistingInstall
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

if (-not $IsWindows) { throw "smoke.ps1 tests the Windows installer and must run on Windows." }
$Installer = (Resolve-Path $Installer).Path
$scratch = Join-Path ([System.IO.Path]::GetTempPath()) "tallpine-smoke-$PID"
$installDir = Join-Path $scratch "app"
$dataDir = Join-Path $scratch "data"
$runFile = Join-Path $dataDir "running.json"
New-Item -ItemType Directory -Force -Path $installDir, $dataDir | Out-Null
$daemon = $null
$failures = [System.Collections.Generic.List[string]]::new()

function Pass([string]$what) { Write-Host "PASS  $what" }
function Fail([string]$what) { Write-Host "FAIL  $what"; $script:failures.Add($what) }

function Stop-InstalledProcesses {
	# The installer may start the app after a silent install; stop anything
	# running from the scratch install so it cannot hold files or ports.
	Get-Process -ErrorAction SilentlyContinue | Where-Object { $_.Path -and $_.Path.StartsWith($installDir, [System.StringComparison]::OrdinalIgnoreCase) } | Stop-Process -Force -ErrorAction SilentlyContinue
}

function Find-ExistingTallpine {
	# electron-builder registers the app under an Uninstall key whose
	# DisplayName starts with the product name, per user or per machine.
	$roots = @(
		"HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall",
		"HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall",
		"HKLM:\Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall"
	)
	$found = @()
	foreach ($root in $roots) {
		Get-ChildItem -Path $root -ErrorAction SilentlyContinue | ForEach-Object {
			$name = (Get-ItemProperty -Path $_.PSPath -ErrorAction SilentlyContinue).PSObject.Properties["DisplayName"]
			if ($name -and $name.Value -like "Tallpine*") { $found += "installed: $($name.Value) ($($_.Name))" }
		}
	}
	Get-Process -Name "tallpine" -ErrorAction SilentlyContinue | Where-Object { -not ($_.Path -and $_.Path.StartsWith($installDir, [System.StringComparison]::OrdinalIgnoreCase)) } | ForEach-Object {
		$found += "running: tallpine.exe pid $($_.Id) ($($_.Path))"
	}
	return $found
}

$existing = @(Find-ExistingTallpine)
if ($existing.Count -gt 0 -and -not $ReplaceExistingInstall) {
	Write-Host "Tallpine is already on this machine:"; $existing | ForEach-Object { Write-Host "  - $_" }
	Write-Host "The smoke installer would replace and then remove it. Uninstall Tallpine (and quit it) first, or pass -ReplaceExistingInstall."
	Remove-Item -Recurse -Force $scratch -ErrorAction SilentlyContinue
	exit 2
}

try {
	Write-Host "Installing $Installer into $installDir"
	# NSIS: /S silent, /currentuser per-user, /D= target dir (must be last, unquoted).
	$setup = Start-Process -FilePath $Installer -ArgumentList @("/S", "/currentuser", "/D=$installDir") -Wait -PassThru
	if ($setup.ExitCode -ne 0) { throw "Installer exited with $($setup.ExitCode)." }
	Start-Sleep -Seconds 2
	Stop-InstalledProcesses

	$appExe = Join-Path $installDir "tallpine.exe"
	$daemonExe = Join-Path $installDir "resources\daemon\tallpine.exe"
	if (Test-Path $appExe) { Pass "app installed ($appExe)" } else { Fail "app executable missing: $appExe" }
	if (-not (Test-Path $daemonExe)) { throw "bundled daemon missing: $daemonExe" }
	Pass "bundled daemon present"

	# Same launch the app uses (shared/daemon-launch.ts): "<resources>\daemon\tallpine.exe daemon".
	$env:TALLPINE_DATA_DIR = $dataDir
	$env:TALLPINE_RUN_FILE = $runFile
	$env:TALLPINE_PORT = "$Port"
	$env:TALLPINE_OWNER = "persistent"
	# Shell terminals are listed per app run, as the desktop app scopes them.
	$env:TALLPINE_APP_RUN_ID = "smoke-$PID"
	$logOut = Join-Path $scratch "daemon.out.log"
	$logErr = Join-Path $scratch "daemon.err.log"
	$daemon = Start-Process -FilePath $daemonExe -ArgumentList "daemon" -WorkingDirectory $dataDir -PassThru -WindowStyle Hidden -RedirectStandardOutput $logOut -RedirectStandardError $logErr

	$base = "http://127.0.0.1:$Port"
	$health = $null
	$deadline = (Get-Date).AddSeconds($TimeoutSeconds)
	while ((Get-Date) -lt $deadline -and -not $daemon.HasExited) {
		try { $health = Invoke-RestMethod -Uri "$base/healthz" -TimeoutSec 5; break } catch { Start-Sleep -Milliseconds 500 }
	}
	if (-not $health) {
		Write-Host "--- daemon stderr ---"; Get-Content $logErr -Tail 40 -ErrorAction SilentlyContinue | Write-Host
		throw "daemon did not answer $base/healthz within $TimeoutSeconds s (exited: $($daemon.HasExited))."
	}
	if ($health.service -eq "tallpine-daemon" -and $health.status -eq "ok") { Pass "/healthz ok (pid $($health.pid))" } else { Fail "/healthz returned $($health | ConvertTo-Json -Compress)" }

	# The daemon writes its token next to the run file (daemontoken.Path).
	$tokenFile = Join-Path (Split-Path $runFile) "daemon.token"
	$deadline = (Get-Date).AddSeconds(15)
	while (-not (Test-Path $tokenFile) -and (Get-Date) -lt $deadline) { Start-Sleep -Milliseconds 250 }
	if (-not (Test-Path $tokenFile)) { throw "daemon token not found at $tokenFile" }
	$headers = @{ "X-VO-Token" = (Get-Content $tokenFile -Raw).Trim() }

	# A shell terminal runs on the Windows runtime (ConPTY); cmd.exe is always present.
	$opened = Invoke-RestMethod -Method Post -Uri "$base/api/v1/shell-terminals" -Headers $headers -ContentType "application/json" -Body (@{ shell = "cmd"; title = "smoke" } | ConvertTo-Json)
	$handle = $opened.shellTerminal.handleId
	if ($handle) { Pass "shell terminal opened (handle $handle)" } else { throw "opening a shell terminal returned no handle: $($opened | ConvertTo-Json -Compress)" }
	Start-Sleep -Seconds 2
	$listed = (Invoke-RestMethod -Uri "$base/api/v1/shell-terminals" -Headers $headers).shellTerminals | Where-Object { $_.handleId -eq $handle }
	if ($listed) { Pass "shell terminal alive after 2 s (ConPTY child-liveness probe)" } else { Fail "shell terminal $handle disappeared (the ConPTY shell exited)" }
	Invoke-RestMethod -Method Delete -Uri "$base/api/v1/shell-terminals/$handle" -Headers $headers | Out-Null
	Pass "shell terminal closed"
} catch {
	Fail $_.Exception.Message
} finally {
	if ($daemon -and -not $daemon.HasExited) { Stop-Process -Id $daemon.Id -Force -ErrorAction SilentlyContinue }
	Stop-InstalledProcesses
	foreach ($name in @("TALLPINE_DATA_DIR", "TALLPINE_RUN_FILE", "TALLPINE_PORT", "TALLPINE_OWNER", "TALLPINE_APP_RUN_ID")) { Remove-Item "Env:$name" -ErrorAction SilentlyContinue }
	if (-not $Keep) {
		$uninstaller = Get-ChildItem -Path $installDir -Filter "Uninstall*.exe" -File -ErrorAction SilentlyContinue | Select-Object -First 1
		if ($uninstaller) {
			$un = Start-Process -FilePath $uninstaller.FullName -ArgumentList @("/S", "/currentuser") -Wait -PassThru
			if ($un.ExitCode -eq 0) { Pass "silent uninstall" } else { Fail "uninstaller exited with $($un.ExitCode)" }
		}
		Start-Sleep -Seconds 2
		Remove-Item -Recurse -Force $scratch -ErrorAction SilentlyContinue
	} else {
		Write-Host "Kept $scratch"
	}
}

if ($failures.Count -gt 0) {
	Write-Host "Smoke FAILED ($($failures.Count)):"; $failures | ForEach-Object { Write-Host "  - $_" }
	exit 1
}
Write-Host "Smoke passed."
exit 0
