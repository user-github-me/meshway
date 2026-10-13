param(
    [ValidateSet("win-x64", "win-arm64")][string]$Runtime = "win-x64",
    [string]$Daemon = ""
)
$ErrorActionPreference = "Stop"
$ProjectRoot = Resolve-Path (Join-Path $PSScriptRoot "../..")
$Output = Join-Path $ProjectRoot "dist/Meshway-$Runtime"
dotnet publish (Join-Path $PSScriptRoot "Meshway/Meshway.csproj") -c Release -r $Runtime --self-contained true -p:PublishSingleFile=true -o $Output
if ($LASTEXITCODE -ne 0) { throw "dotnet publish failed" }
if (-not $Daemon) {
    $Arch = if ($Runtime -eq "win-arm64") { "arm64" } else { "amd64" }
    $Daemon = Join-Path $ProjectRoot "dist/bin/meshway-windows-$Arch.exe"
}
if (-not (Test-Path $Daemon)) { throw "Build the Go daemon first, or pass -Daemon path/to/meshway.exe" }
Copy-Item $Daemon (Join-Path $Output "meshway-daemon.exe")
Write-Output "Built $Output. Public releases still require Windows signing and device tests."
