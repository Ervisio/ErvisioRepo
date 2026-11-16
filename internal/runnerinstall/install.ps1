# Ervisio runner installer for Windows, served by Ervisio Repo at
# /runner/install.ps1. Run it in an elevated PowerShell:
#
#   & ([scriptblock]::Create((irm https://<host>/runner/install.ps1))) -Url https://<host> -Token <errt_...>
#
# or set $env:ERVISIO_RUNNER_TOKEN first and use: irm https://<host>/runner/install.ps1 | iex
#
# The runner is installed in %ProgramFiles%\ErvisioRunner, its config and
# state in %ProgramData%\ervisio-runner, and it starts at boot through the
# scheduled task "ErvisioRunner" (runs as SYSTEM, restarts on failure).
param(
  [string]$Url = $(if ($env:ERVISIO_RUNNER_URL) { $env:ERVISIO_RUNNER_URL } else { "@@URL@@" }),
  [string]$Token = $env:ERVISIO_RUNNER_TOKEN,
  [string]$Name = $env:ERVISIO_RUNNER_NAME,
  [string]$Labels = $env:ERVISIO_RUNNER_LABELS,
  [int]$Capacity = 0,
  [switch]$HostMode,
  [string]$Version = "@@VERSION@@",
  [switch]$Uninstall,
  [switch]$Purge
)
$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

$Repo = "https://github.com/Ervisio/ErvisioRepo"
$TaskName = "ErvisioRunner"
$BinDir = Join-Path $env:ProgramFiles "ErvisioRunner"
$Bin = Join-Path $BinDir "ervisio-runner.exe"
$DataDir = Join-Path $env:ProgramData "ervisio-runner"
$Cfg = Join-Path $DataDir "config.toml"

$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
  throw "Run this in an elevated PowerShell (Run as administrator)."
}

if ($Uninstall) {
  Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
  Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false -ErrorAction SilentlyContinue
  Get-Process ervisio-runner -ErrorAction SilentlyContinue | Stop-Process -Force
  Remove-Item -Recurse -Force $BinDir -ErrorAction SilentlyContinue
  if ($Purge) { Remove-Item -Recurse -Force $DataDir -ErrorAction SilentlyContinue }
  Write-Host "ervisio-runner removed. Delete it from the Runners page of the server too."
  return
}

if (-not $Url) { throw "-Url is required" }
$Url = $Url.TrimEnd("/")
if ($Url -notmatch '^https?://') { throw "-Url must start with http:// or https://" }

if (-not $HostMode -and -not (Get-Command docker -ErrorAction SilentlyContinue)) {
  throw "Docker is not installed. Install Docker Desktop (Linux containers) and run again, or pass -HostMode to run jobs directly on this machine (no isolation: trusted repositories only)."
}

switch ($env:PROCESSOR_ARCHITECTURE) {
  "AMD64" { $Arch = "amd64" }
  "ARM64" { $Arch = "arm64" }
  default { throw "unsupported architecture $($env:PROCESSOR_ARCHITECTURE)" }
}

if ($Version -notmatch '^\d+\.\d+\.\d+') {
  $rel = Invoke-RestMethod "https://api.github.com/repos/Ervisio/ErvisioRepo/releases/latest"
  $Version = $rel.tag_name
}
$Version = $Version.TrimStart("v")

$Asset = "ervisio-runner_${Version}_windows_${Arch}.zip"
$Base = "$Repo/releases/download/v$Version"
$Tmp = Join-Path ([IO.Path]::GetTempPath()) ("ervisio-runner-" + [guid]::NewGuid())
New-Item -ItemType Directory -Force $Tmp | Out-Null
try {
  Write-Host "==> Downloading $Asset"
  Invoke-WebRequest -UseBasicParsing "$Base/$Asset" -OutFile (Join-Path $Tmp $Asset)
  Invoke-WebRequest -UseBasicParsing "$Base/ervisio-runner_${Version}_checksums.txt" -OutFile (Join-Path $Tmp "checksums.txt")
  $want = (Get-Content (Join-Path $Tmp "checksums.txt") | Where-Object { $_ -match "\s\*?$([regex]::Escape($Asset))$" } | ForEach-Object { ($_ -split '\s+')[0] }) | Select-Object -First 1
  if (-not $want) { throw "$Asset is not listed in the checksums file" }
  $got = (Get-FileHash -Algorithm SHA256 (Join-Path $Tmp $Asset)).Hash.ToLower()
  if ($got -ne $want.ToLower()) { throw "checksum mismatch for $Asset" }
  Expand-Archive -Force (Join-Path $Tmp $Asset) $Tmp
  Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
  Get-Process ervisio-runner -ErrorAction SilentlyContinue | Stop-Process -Force
  New-Item -ItemType Directory -Force $BinDir | Out-Null
  Copy-Item -Force (Join-Path $Tmp "ervisio-runner.exe") $Bin
} finally {
  Remove-Item -Recurse -Force $Tmp -ErrorAction SilentlyContinue
}
Write-Host "==> Installed $Bin ($(& $Bin version))"

New-Item -ItemType Directory -Force $DataDir | Out-Null
# Only SYSTEM and Administrators may read the config and the credential.
icacls $DataDir /inheritance:r /grant:r "*S-1-5-18:(OI)(CI)F" "*S-1-5-32-544:(OI)(CI)F" | Out-Null

if (Test-Path (Join-Path $DataDir ".runner")) {
  Write-Host "==> Already registered; run with -Uninstall -Purge to start over"
} else {
  if (-not $Token) { throw "-Token is required" }
  $argv = @("register", "--config", $Cfg, "--data-dir", $DataDir, "--url", $Url)
  if ($Name) { $argv += @("--name", $Name) }
  if ($Labels) { $argv += @("--labels", $Labels) }
  if ($Capacity -gt 0) { $argv += @("--capacity", "$Capacity") }
  if ($HostMode) { $argv += "--host" }
  $env:ERVISIO_RUNNER_TOKEN = $Token
  try { & $Bin @argv; if ($LASTEXITCODE -ne 0) { throw "registration failed" } }
  finally { Remove-Item Env:\ERVISIO_RUNNER_TOKEN -ErrorAction SilentlyContinue }
}

$action = New-ScheduledTaskAction -Execute $Bin -Argument "daemon --config `"$Cfg`""
$trigger = New-ScheduledTaskTrigger -AtStartup
$settings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit ([TimeSpan]::Zero) -RestartCount 999 `
  -RestartInterval (New-TimeSpan -Minutes 1) -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable
$task = New-ScheduledTaskPrincipal -UserId "SYSTEM" -LogonType ServiceAccount -RunLevel Highest
Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger -Settings $settings -Principal $task -Force | Out-Null
Start-ScheduledTask -TaskName $TaskName
Write-Host "==> Scheduled task $TaskName registered and started (runs at boot as SYSTEM)"
