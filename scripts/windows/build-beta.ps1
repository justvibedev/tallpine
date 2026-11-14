<#
.SYNOPSIS
Builds the Tallpine Windows beta installer (x64) and optionally publishes it
as a GitHub prerelease.

.DESCRIPTION
Run from a clean checkout on Windows 11 x64 with Git for Windows, Node, npm
and Go on PATH (see docs/windows-beta.md). The installer version is
<frontend/package.json version>-beta.<Build>. The script stamps that version
into frontend/package.json for the build and restores the file afterwards,
writes the installer and a .sha256 file to -OutDir, and prints the installer
path as its only pipeline output.

With -Publish it uploads both files as a prerelease to -Repo with the gh CLI.
It never creates the repository: the owner creates it first.

.EXAMPLE
pwsh scripts/windows/build-beta.ps1 -Build 1
pwsh scripts/windows/build-beta.ps1 -Build 2 -Publish
pwsh scripts/windows/build-beta.ps1 -Build 2 -Publish -Installer "frontend\out\windows-beta\Tallpine Setup 0.13.14-beta.2.exe"
#>
#Requires -Version 7
[CmdletBinding()]
param(
	# Beta build number N in <version>-beta.N. Bump it for every build you share.
	[Parameter(Mandatory = $true)][ValidateRange(1, 999999)][int]$Build,
	# Upload the installer and checksum as a GitHub prerelease.
	[switch]$Publish,
	# Releases-only repository that receives the prerelease.
	[string]$Repo = "justvibedev/tallpine-releases",
	# Where the installer and checksum are copied.
	[string]$OutDir,
	# Reuse frontend/node_modules instead of running npm ci.
	[switch]$SkipNpmCi,
	# Build from a checkout with uncommitted changes (the build is marked dirty).
	[switch]$AllowDirty,
	# With -Publish: skip the build and publish this already built (and smoked)
	# installer. It must be the "Tallpine Setup <version>-beta.<Build>.exe" of this checkout.
	[string]$Installer
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Invoke-Native {
	param([string]$Name, [string[]]$Arguments)
	# Native output goes to the host so the pipeline carries only the installer path.
	& $Name @Arguments | Out-Host
	if ($LASTEXITCODE -ne 0) { throw "$Name $($Arguments -join ' ') failed with exit code $LASTEXITCODE" }
}

if (-not $IsWindows) { throw "build-beta.ps1 builds the Windows installer and must run on Windows." }
if ($env:PROCESSOR_ARCHITECTURE -ne "AMD64") { throw "The Windows beta is x64 only; this machine is $($env:PROCESSOR_ARCHITECTURE)." }
foreach ($tool in @("git", "node", "npm", "go") + $(if ($Publish) { @("gh") } else { @() })) {
	if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) { throw "$tool is not on PATH (see docs/windows-beta.md, Prerequisites)." }
}

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$frontend = Join-Path $repoRoot "frontend"
$packageJson = Join-Path $frontend "package.json"
if (-not $OutDir) { $OutDir = Join-Path $frontend "out\windows-beta" }

$commit = (& git -C $repoRoot rev-parse HEAD).Trim()
$dirty = [bool](& git -C $repoRoot status --porcelain)
if ($dirty -and -not $AllowDirty) { throw "The checkout has uncommitted changes. Commit or stash them, or pass -AllowDirty for a local test build." }

$baseVersion = (Get-Content $packageJson -Raw | ConvertFrom-Json).version
if ($baseVersion -notmatch '^\d+\.\d+\.\d+$') { throw "frontend/package.json version '$baseVersion' is not a plain x.y.z release version." }
$version = "$baseVersion-beta.$Build"
$tag = "v$version"

if ($Publish) {
	& gh auth status *> $null
	if ($LASTEXITCODE -ne 0) { throw "gh is not signed in; run 'gh auth login' first." }
	& gh repo view $Repo --json name *> $null
	if ($LASTEXITCODE -ne 0) { throw "Cannot see $Repo. The owner creates the releases repository (with one commit, e.g. a README) before the first publish." }
	& gh release view $tag --repo $Repo *> $null
	if ($LASTEXITCODE -eq 0) { throw "$Repo already has release $tag; pass a higher -Build." }
}

$installerName = "Tallpine Setup $version.exe"
if ($Installer) {
	# Publish the exact installer that was smoked instead of building a new one.
	if (-not $Publish) { throw "-Installer only publishes an existing build; add -Publish." }
	$installerPath = (Resolve-Path $Installer).Path
	if ((Split-Path -Leaf $installerPath) -ne $installerName) { throw "$installerPath is not $installerName; pass the -Build number it was built with." }
} else {
	Write-Host "Building Tallpine $version (commit $commit, dirty=$($dirty.ToString().ToLower()))"
	$originalPackageJson = [System.IO.File]::ReadAllBytes($packageJson)
	$envNames = @("TALLPINE_BUILD_VERSION", "TALLPINE_BUILD_COMMIT", "TALLPINE_BUILD_DIRTY")
	$savedEnv = @{}
	foreach ($name in $envNames) { $savedEnv[$name] = [Environment]::GetEnvironmentVariable($name) }
	Push-Location $frontend
	try {
		# electron-builder names and versions the installer from package.json, so the
		# beta version must be in the file; the build info reads the same values from
		# the environment (dirty is pinned to the state before this edit).
		Invoke-Native npm @("pkg", "set", "version=$version")
		$env:TALLPINE_BUILD_VERSION = $version
		$env:TALLPINE_BUILD_COMMIT = $commit
		$env:TALLPINE_BUILD_DIRTY = $dirty.ToString().ToLower()
		if (-not $SkipNpmCi) { Invoke-Native npm @("ci", "--no-audit", "--no-fund") }
		Invoke-Native npm @("run", "make", "--", "--platform", "win32", "--arch", "x64")
	} finally {
		Pop-Location
		[System.IO.File]::WriteAllBytes($packageJson, $originalPackageJson)
		foreach ($name in $envNames) { [Environment]::SetEnvironmentVariable($name, $savedEnv[$name]) }
	}

	$built = Get-ChildItem -Path (Join-Path $frontend "out\make") -Recurse -File -Filter $installerName | Select-Object -First 1
	if (-not $built) { throw "The build finished but '$installerName' is not under frontend\out\make." }

	New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
	$installerPath = Join-Path $OutDir $installerName
	Copy-Item -Force $built.FullName $installerPath
}
$hash = (Get-FileHash -Algorithm SHA256 $installerPath).Hash.ToLower()
$checksum = "$installerPath.sha256"
# sha256sum format, so `sha256sum -c` and Get-FileHash comparisons both work.
Set-Content -NoNewline -Encoding ascii -Path $checksum -Value "$hash  $installerName`n"
Write-Host "Installer: $installerPath"
Write-Host "SHA-256:   $hash"

if ($Publish) {
	$notes = @"
Tallpine $version, Windows x64 beta (unsigned). Built from commit $commit.

SHA-256 ($installerName): $hash

Windows SmartScreen warns about unsigned installers: choose More info, then Run anyway.
"@
	Invoke-Native gh @("release", "create", $tag, $installerPath, $checksum, "--repo", $Repo, "--prerelease", "--title", "Tallpine $version (Windows beta)", "--notes", $notes)
	Write-Host "Published https://github.com/$Repo/releases/tag/$tag"
}

Write-Output $installerPath
