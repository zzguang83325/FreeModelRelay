# FreeModelRelay build script
# Usage:
#   .\build.ps1            build current platform -> FreeModelRelay(.exe)
#   .\build.ps1 -Test      run go test before building
#   .\build.ps1 -All       cross-compile all 6 release targets -> dist/ (same as CI)
param(
    [switch]$All,
    [switch]$Test
)

$ErrorActionPreference = "Stop"
$ldflags = "-s -w"

# Keep in sync with the release matrix in .github/workflows/build.yml
$targets = @(
    @{ goos = "windows"; goarch = "amd64"; ext = ".exe" },
    @{ goos = "windows"; goarch = "arm64"; ext = ".exe" },
    @{ goos = "linux";   goarch = "amd64"; ext = "" },
    @{ goos = "linux";   goarch = "arm64"; ext = "" },
    @{ goos = "darwin";  goarch = "amd64"; ext = "" },
    @{ goos = "darwin";  goarch = "arm64"; ext = "" }
)

function Invoke-Build([string]$goos, [string]$goarch, [string]$out) {
    Write-Host "==> Building $out"
    $env:CGO_ENABLED = "0"
    if ($goos)   { $env:GOOS = $goos }     else { Remove-Item Env:GOOS -ErrorAction SilentlyContinue }
    if ($goarch) { $env:GOARCH = $goarch } else { Remove-Item Env:GOARCH -ErrorAction SilentlyContinue }
    go build -ldflags $ldflags -o $out .
    if ($LASTEXITCODE -ne 0) { Write-Host "==> Build failed: $out" -ForegroundColor Red; exit 1 }
    Write-Host "==> OK: $out"
}

if ($Test) {
    Write-Host "==> Running tests"
    go test ./...
    if ($LASTEXITCODE -ne 0) { Write-Host "==> Tests failed" -ForegroundColor Red; exit 1 }
}

if ($All) {
    if (Test-Path dist) { Remove-Item dist -Recurse -Force }
    New-Item -ItemType Directory -Force -Path dist | Out-Null
    foreach ($t in $targets) {
        Invoke-Build $t.goos $t.goarch ("dist/FreeModelRelay-$($t.goos)-$($t.goarch)$($t.ext)")
    }
    Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue
    Write-Host ""
    Write-Host "==> All 6 targets done, output in dist/"
} else {
    # Current platform: leave GOOS/GOARCH unset, let Go decide
    $out = "FreeModelRelay"
    if ($PSVersionTable.PSVersion.Major -lt 6 -or $IsWindows) { $out = "FreeModelRelay.exe" }
    Invoke-Build "" "" $out
}
