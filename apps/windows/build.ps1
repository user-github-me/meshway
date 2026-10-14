param(
    [ValidateSet("win-x64", "win-arm64")][string]$Runtime = "win-x64",
    [string]$Daemon = "",
    [string]$Wintun = "",
    [string]$Output = "",
    [string]$AppVersion = "0.1.0-alpha.2"
)
$ErrorActionPreference = "Stop"
$ProjectRoot = Resolve-Path (Join-Path $PSScriptRoot "../..")
if (!$Output) { $Output = Join-Path $ProjectRoot "dist/Meshway-$Runtime" }
if ($AppVersion -notmatch '^\d+\.\d+\.\d+(-[A-Za-z0-9.]+)?$') { throw 'Use an explicit semantic app version' }
dotnet publish (Join-Path $PSScriptRoot "Meshway/Meshway.csproj") -c Release -r $Runtime --self-contained true -p:PublishSingleFile=true "-p:Version=$AppVersion" -o $Output
if ($LASTEXITCODE -ne 0) { throw "dotnet publish failed" }
dotnet publish (Join-Path $PSScriptRoot "Meshway.VpnHelper/Meshway.VpnHelper.csproj") -c Release -r $Runtime --self-contained true -p:PublishSingleFile=true "-p:Version=$AppVersion" -o $Output
if ($LASTEXITCODE -ne 0) { throw "VPN helper publish failed" }
if (-not $Daemon) {
    $Arch = if ($Runtime -eq "win-arm64") { "arm64" } else { "amd64" }
    $Daemon = Join-Path $ProjectRoot "dist/bin/meshway-windows-$Arch.exe"
}
if (-not (Test-Path $Daemon)) { throw "Build the Go daemon first, or pass -Daemon path/to/meshway.exe" }
Copy-Item $Daemon (Join-Path $Output "meshway-daemon.exe")
Write-Output "Built $Output. Public releases still require Windows signing and device tests."

if ($Wintun) {
    if (-not (Test-Path $Wintun)) { throw "Specify the signed wintun.dll matching $Runtime from https://www.wintun.net/" }
    Copy-Item $Wintun (Join-Path $Output "wintun.dll")
} else { Write-Output "SOCKS package built. Supply -Wintun matching signed wintun.dll to enable Windows device VPN." }
# The Wintun license must travel with every permitted API redistribution.
if ($Wintun) {
    $WintunLicense = Join-Path (Split-Path $Wintun -Parent) "WINTUN-LICENSE.txt"
    if (Test-Path $WintunLicense) { Copy-Item $WintunLicense (Join-Path $Output "WINTUN-LICENSE.txt") }
}
