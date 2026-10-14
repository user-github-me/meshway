param(
    [Parameter(Mandatory = $true)][string]$Daemon,
    [string]$Desktop = "",
    [string]$Output = "artifacts/windows-validation.json"
)
$ErrorActionPreference = "Stop"
if ($env:OS -ne "Windows_NT") { throw "This runtime validation must execute on Windows." }
$Directory = Join-Path ([IO.Path]::GetTempPath()) ("meshway-validation-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $Directory | Out-Null
$Processes = @()
function FreePort {
    $Listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
    $Listener.Start(); $Port = $Listener.LocalEndpoint.Port; $Listener.Stop(); return $Port
}
try {
    $ConfigPath = Join-Path $Directory "config.json"
    & $Daemon init --config $ConfigPath | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "Native Windows initialization failed." }
    $Config = Get-Content -Raw -LiteralPath $ConfigPath | ConvertFrom-Json
    $Config.listen_addresses = @("/ip4/127.0.0.1/tcp/0")
    $Config.socks_listen = "127.0.0.1:$(FreePort)"
    $Config.api_listen = "127.0.0.1:$(FreePort)"
    $Config | ConvertTo-Json -Depth 20 | Set-Content -LiteralPath $ConfigPath
    $Process = Start-Process -FilePath $Daemon -ArgumentList @("daemon", "--config", ('"' + $ConfigPath + '"')) -PassThru -WindowStyle Hidden -RedirectStandardOutput (Join-Path $Directory "stdout.log") -RedirectStandardError (Join-Path $Directory "stderr.log")
    $Processes += $Process
    $Headers = @{ Authorization = "Bearer " + $Config.api_token }
    $Status = $null
    for ($Attempt = 0; $Attempt -lt 100; $Attempt++) {
        if ($Process.HasExited) { throw "Native Windows daemon stopped during startup." }
        try { $Status = Invoke-RestMethod -Uri ("http://" + $Config.api_listen + "/v1/status") -Headers $Headers -TimeoutSec 2; break } catch { Start-Sleep -Milliseconds 100 }
    }
    if (-not $Status.peer_id) { throw "Daemon API readiness failed." }
    $UnauthorizedRejected = $false
    try { Invoke-RestMethod -Uri ("http://" + $Config.api_listen + "/v1/status") -TimeoutSec 2 | Out-Null } catch { $UnauthorizedRejected = $_.Exception.Response.StatusCode -eq 401 }
    if (-not $UnauthorizedRejected) { throw "Unauthenticated API unexpectedly succeeded." }
    Invoke-RestMethod -Method Post -Uri ("http://" + $Config.api_listen + "/v1/shutdown") -Headers $Headers -ContentType 'application/json' -Body '{}' | Out-Null
    if (-not $Process.WaitForExit(10000)) { throw "Windows daemon failed graceful shutdown." }
    if ($Process.ExitCode -ne 0) { throw "Windows daemon failed lifecycle test." }
    $DesktopStarted = $false
    if ($Desktop) {
        $DesktopProcess = Start-Process -FilePath $Desktop -PassThru
        $Processes += $DesktopProcess
        Start-Sleep -Seconds 3
        if ($DesktopProcess.HasExited) { throw "Windows desktop exited during startup." }
        $DesktopStarted = $DesktopProcess.MainWindowHandle -ne 0
        if (-not $DesktopStarted) { throw "Windows desktop did not create its window." }
        $DesktopProcess.CloseMainWindow() | Out-Null
        if (-not $DesktopProcess.WaitForExit(10000)) { throw "Desktop did not close cleanly." }
    }
    $Result = @{ platform = 'Windows'; daemon_runtime = 'PASS'; api_authentication = 'PASS'; graceful_shutdown = 'PASS'; desktop_window = $DesktopStarted; physical_vpn = 'Separate isolated Wintun routing test required'; host_network_modified = $false; time = [DateTime]::UtcNow.ToString('o') }
    New-Item -ItemType Directory -Force -Path (Split-Path $Output -Parent) | Out-Null
    $Result | ConvertTo-Json | Set-Content -LiteralPath $Output
    Write-Output 'Windows runtime validation passed. No routes, DNS or proxy settings were changed.'
} finally {
    foreach ($Process in $Processes) { if (-not $Process.HasExited) { Stop-Process -Id $Process.Id -Force -ErrorAction SilentlyContinue } }
    Remove-Item -LiteralPath $Directory -Recurse -Force -ErrorAction SilentlyContinue
}
