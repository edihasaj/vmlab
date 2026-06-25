#Requires -Version 5.1
<#
.SYNOPSIS
  Install the latest vmlab release on Windows.
.DESCRIPTION
  Downloads the newest vmlab_<ver>_windows_<arch>.zip from GitHub Releases,
  extracts vmlab.exe into %LOCALAPPDATA%\Programs\vmlab, and adds that dir to
  the user PATH (idempotent). Run again to upgrade.

  Usage:
    irm https://raw.githubusercontent.com/edihasaj/vmlab/main/scripts/install.ps1 | iex
#>
[CmdletBinding()]
param(
  [string]$Version = "latest",
  [string]$InstallDir = (Join-Path $env:LOCALAPPDATA "Programs\vmlab")
)

$ErrorActionPreference = "Stop"
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocol]::Tls12

$repo = "edihasaj/vmlab"

# amd64 / arm64 — match the goreleaser archive naming (x86_64 / arm64).
$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
  "AMD64" { "x86_64" }
  "ARM64" { "arm64" }
  default { throw "unsupported architecture: $env:PROCESSOR_ARCHITECTURE" }
}

if ($Version -eq "latest") {
  $rel = Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest"
  $Version = $rel.tag_name
}
$ver = $Version.TrimStart("v")
$asset = "vmlab_${ver}_windows_${arch}.zip"
$url = "https://github.com/$repo/releases/download/$Version/$asset"

Write-Host "Installing vmlab $Version ($arch) -> $InstallDir"
$tmp = Join-Path $env:TEMP "vmlab-$([guid]::NewGuid().ToString('N')).zip"
Invoke-WebRequest -Uri $url -OutFile $tmp

New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
Expand-Archive -Path $tmp -DestinationPath $InstallDir -Force
Remove-Item $tmp -Force

# Add InstallDir to the user PATH if it's not already there.
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if (-not ($userPath -split ';' | Where-Object { $_ -eq $InstallDir })) {
  $newPath = if ([string]::IsNullOrEmpty($userPath)) { $InstallDir } else { "$userPath;$InstallDir" }
  [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
  Write-Host "Added $InstallDir to your user PATH (restart your shell to pick it up)."
}

$exe = Join-Path $InstallDir "vmlab.exe"
Write-Host "Installed: $exe"
& $exe --version
