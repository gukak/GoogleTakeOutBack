# takeOutBack installer for Windows.
# Usage: irm <URL>/install.ps1 | iex
# Set $env:TAKEOUTBACK_VERSION to override the release tag.

param(
    [switch]$Force,
    [string]$Version = $env:TAKEOUTBACK_VERSION,
    [switch]$NoBoth
)

if (-not $Version) { $Version = "v0.6.6" }
$OwnerRepo = "gukak/GoogleTakeOutBack"
$FetchBoth = if ($NoBoth) { $false } else { $true }

$ErrorActionPreference = "Stop"

$Root = (Get-Location).Path

if (-not $Force) {
    $existing = Get-ChildItem -Force | Where-Object { $_.Name -ne ".takeoutback-root" }
    if ($existing -and -not (Test-Path "$Root\.takeoutback-root")) {
        Write-Error "Directory is not empty. Run with -Force or use an empty directory."
    }
}

$dirs = @(
    "Incoming",
    "Archive",
    "Backup",
    "TakeOutBack/app",
    "TakeOutBack/tools/linux",
    "TakeOutBack/tools/windows",
    "TakeOutBack/temp",
    "TakeOutBack/logs",
    "TakeOutBack/config",
    "TakeOutBack/scripts",
    "TakeOutBack/docs"
)
foreach ($d in $dirs) {
    New-Item -ItemType Directory -Force -Path "$Root\$d" | Out-Null
}
"TakeOutBack project root" | Out-File -Encoding utf8 -FilePath "$Root\.takeoutback-root"

$Base = "https://github.com/$OwnerRepo/releases/download/$Version"

function Download($url, $out) {
    Invoke-WebRequest -Uri $url -OutFile $out -UseBasicParsing -MaximumRedirection 5
}

function FetchBinary($name, $out) {
    $tmp = "$out.tmp"
    Download "$Base/$name" $tmp
    Download "$Base/$name.sha256" "$tmp.sha256"
    $expected = ((Get-Content "$tmp.sha256") -split '\s+')[0]
    $got = (Get-FileHash -Algorithm SHA256 $tmp).Hash.ToLower()
    if ($got -ne $expected) {
        throw "Checksum mismatch for $name"
    }
    Remove-Item "$tmp.sha256"
    Move-Item $tmp $out -Force
}

FetchBinary "takeoutback-windows-amd64.exe" "$Root\TakeOutBack\tools\windows\takeoutback.exe"
if ($FetchBoth) {
    FetchBinary "takeoutback-linux-amd64" "$Root\TakeOutBack\tools\linux\takeoutback"
}

Download "$Base/takeOutBack.bat" "$Root\takeOutBack.bat"
Download "$Base/takeOutBack.sh" "$Root\takeOutBack.sh"
Download "$Base/settings.json" "$Root\TakeOutBack\config\settings.json"
Download "$Base/policy.json" "$Root\TakeOutBack\config\policy.json"
Download "$Base/README.md" "$Root\TakeOutBack\docs\README.md"

Write-Host "takeOutBack $Version installed in $Root"
Write-Host "Place Google Takeout ZIP files in $Root\Incoming and run $Root\takeOutBack.bat"
