# FakeScript + fkspm Installer for Windows (PowerShell)
# Usage: irm https://raw.githubusercontent.com/ChaiMaii/fakescript/main/install.ps1 | iex

$ErrorActionPreference = 'Stop'

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Write-Host "ERROR: Go 1.22+ is required to install FakeScript and fkspm." -ForegroundColor Red
    Write-Host "Download Go from https://go.dev/dl/ and re-run this script."
    exit 1
}

$tmpDir = Join-Path ([System.IO.Path]::GetTempPath()) ("fakescript_setup_" + [System.Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tmpDir | Out-Null

try {
    $installerGo = Join-Path $tmpDir "main.go"
    $installerUrl = "https://raw.githubusercontent.com/ChaiMaii/fakescript/main/installer/main.go"
    Write-Host "Fetching FakeScript visual installer..." -ForegroundColor Cyan
    Invoke-WebRequest -Uri $installerUrl -OutFile $installerGo -UseBasicParsing

    $installerBin = Join-Path $tmpDir "fakescript-installer.exe"
    & go build -o $installerBin $installerGo
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to build installer."
    }

    & $installerBin @args
} finally {
    Remove-Item -Recurse -Force $tmpDir -ErrorAction SilentlyContinue
}
