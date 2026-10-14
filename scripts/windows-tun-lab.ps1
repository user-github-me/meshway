param(
    [string]$Package = "dist/Meshway-win-x64",
    [string]$Output = "artifacts/windows-wintun-validation.json"
)
$ErrorActionPreference = "Stop"
if ($env:OS -ne "Windows_NT") { throw "This lab requires a disposable Windows runner." }
$Identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$Principal = [Security.Principal.WindowsPrincipal]::new($Identity)
if (-not $Principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw "A disposable administrator Windows runner is required for Wintun." }
$Driver = Join-Path $Package "wintun.dll"
if (-not (Test-Path -LiteralPath $Driver)) { throw "Prepare the pinned upstream Wintun DLL first." }
$Signature = Get-AuthenticodeSignature -LiteralPath $Driver
if ($Signature.Status -ne "Valid") { throw "Wintun vendor signature verification failed." }
$BeforeRoutes = Get-NetRoute | Where-Object { $_.DestinationPrefix -eq "0.0.0.0/0" -or $_.DestinationPrefix -eq "::/0" } | Select-Object InterfaceIndex, DestinationPrefix, NextHop, RouteMetric | Sort-Object InterfaceIndex, DestinationPrefix | ConvertTo-Json -Compress
$BeforeDns = Get-DnsClientServerAddress | Select-Object InterfaceIndex, AddressFamily, ServerAddresses | Sort-Object InterfaceIndex, AddressFamily | ConvertTo-Json -Compress
$Binary = Join-Path $Package "meshway-vpn-test.exe"
& go test -c -o $Binary ./internal/vpn
if ($LASTEXITCODE -ne 0) { throw "Windows VPN test compilation failed." }
try {
    $env:MESHWAY_WINDOWS_TUN_LAB = "1"
    & $Binary -test.run TestWindowsWintunKernelIPv4IPv6EncryptedTCP -test.v -test.timeout 90s
    if ($LASTEXITCODE -ne 0) { throw "Actual Wintun kernel packet validation failed." }
} finally {
    Remove-Item Env:MESHWAY_WINDOWS_TUN_LAB -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $Binary -ErrorAction SilentlyContinue
}
$AfterRoutes = Get-NetRoute | Where-Object { $_.DestinationPrefix -eq "0.0.0.0/0" -or $_.DestinationPrefix -eq "::/0" } | Select-Object InterfaceIndex, DestinationPrefix, NextHop, RouteMetric | Sort-Object InterfaceIndex, DestinationPrefix | ConvertTo-Json -Compress
$AfterDns = Get-DnsClientServerAddress | Where-Object { $_.InterfaceAlias -notlike "MeshwayLab*" } | Select-Object InterfaceIndex, AddressFamily, ServerAddresses | Sort-Object InterfaceIndex, AddressFamily | ConvertTo-Json -Compress
if ($BeforeRoutes -ne $AfterRoutes) { throw "The isolated driver lab changed a default route." }
if ($BeforeDns -ne $AfterDns) { throw "The isolated driver lab changed a DNS configuration." }
$Result = @{ platform = "Windows"; signed_wintun_driver = "PASS"; actual_kernel_tcp_ipv4 = "PASS"; actual_kernel_tcp_ipv6 = "PASS"; encrypted_peer_packet_round_trip = "PASS"; default_routes_unchanged = $true; dns_unchanged = $true; full_device_firewall_crash_protection = "Separate native helper runtime gate"; time = [DateTime]::UtcNow.ToString("o") }
New-Item -ItemType Directory -Force -Path (Split-Path $Output -Parent) | Out-Null
$Result | ConvertTo-Json | Set-Content -LiteralPath $Output
