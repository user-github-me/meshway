param(
    [string]$Package = "dist/Meshway-win-x64",
    [string]$Output = "artifacts/windows-firewall-validation.json"
)
$ErrorActionPreference = 'Stop'
if ($env:OS -ne 'Windows_NT') { throw 'This lab requires a disposable Windows runner.' }
$principal = [Security.Principal.WindowsPrincipal]::new([Security.Principal.WindowsIdentity]::GetCurrent())
if (!$principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw 'A disposable administrator runner is required.' }
$driver = Join-Path $Package 'wintun.dll'
if ((Get-AuthenticodeSignature -LiteralPath $driver).Status -ne 'Valid') { throw 'Pinned Wintun signature verification failed.' }
$root = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$beforeRoutes = Get-NetRoute | Where-Object { $_.DestinationPrefix -in @('0.0.0.0/0','::/0') } | Select-Object InterfaceIndex,DestinationPrefix,NextHop,RouteMetric | Sort-Object InterfaceIndex,DestinationPrefix | ConvertTo-Json -Compress
$beforeDns = Get-DnsClientServerAddress | Select-Object InterfaceIndex,AddressFamily,ServerAddresses | Sort-Object InterfaceIndex,AddressFamily | ConvertTo-Json -Compress
$directory = Join-Path ([IO.Path]::GetTempPath()) ('meshway-firewall-lab-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $directory | Out-Null
$binary = Join-Path (Resolve-Path $Package).Path 'meshway-firewall-test.exe'
$groups = @(); $child = $null; $adapter = $null
$previousFixtureDirectory = $env:MESHWAY_WINDOWS_FIREWALL_LAB_DIR
$evidence = Join-Path $root 'artifacts/windows-firewall-debug'
New-Item -ItemType Directory -Force -Path $evidence | Out-Null
$script:probeResults = @()
function Assert-Lab([bool]$condition,[string]$message) { if (!$condition) { throw $message } }
function Invoke-PacketProbe([int]$family,[bool]$peer,[bool]$expected) {
    $response = Join-Path $directory 'response.json'
    Remove-Item -LiteralPath $response -ErrorAction SilentlyContinue
    $tag = 'meshway-' + [Guid]::NewGuid().ToString('N')
    $requestTemp = Join-Path $directory 'request.tmp'
    @{ family=$family; peer=$peer; tag=$tag } | ConvertTo-Json | Set-Content -LiteralPath $requestTemp -Encoding UTF8
    Move-Item -LiteralPath $requestTemp -Destination (Join-Path $directory 'request.json') -Force
    $deadline = [DateTime]::UtcNow.AddSeconds(5)
    while (!(Test-Path -LiteralPath $response)) {
        if ($child.HasExited -or [DateTime]::UtcNow -ge $deadline) { throw 'Kernel packet probe timed out.' }
        Start-Sleep -Milliseconds 50
    }
    $result = Get-Content -LiteralPath $response -Raw | ConvertFrom-Json
    $script:probeResults += @{ family=$family; peer=$peer; expected=$expected; observed=$result.observed; write_error=$result.write_error }
    $script:probeResults | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $evidence 'kernel-probes.json')
    Assert-Lab ($result.observed -eq $expected) "Unexpected IPv$family kernel UDP result (peer=$peer, expected=$expected)."
}
function Invoke-ProductionFirewall($plan,[bool]$expectSuccess) {
    $inputPath = Join-Path $directory 'firewall-plan.json'
    $runPath = Join-Path $directory 'apply.ps1'
    $plan | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $inputPath -Encoding UTF8
    @'
param([string]$PlanPath,[string]$SourcePath,[string]$AdapterName)
$ErrorActionPreference='Stop'
$plan=Get-Content -LiteralPath $PlanPath -Raw | ConvertFrom-Json
if ($AdapterName -notmatch '^MeshwayFirewallLab[0-9]+$') { throw 'Only the owned lab adapter may be used.' }
$adapter=Get-NetAdapter -Name $AdapterName -ErrorAction Stop
. $SourcePath
Add-MeshwayFirewall -Plan $plan -Adapter $adapter
'@ | Set-Content -LiteralPath $runPath -Encoding UTF8
    $source = Join-Path $root 'apps/windows/Meshway.VpnHelper/firewall.ps1'
    $start = [Diagnostics.ProcessStartInfo]::new((Join-Path $env:SystemRoot 'System32/WindowsPowerShell/v1.0/powershell.exe'))
    $start.UseShellExecute=$false; $start.CreateNoWindow=$true; $start.RedirectStandardOutput=$true; $start.RedirectStandardError=$true
    foreach ($arg in @('-NoProfile','-NonInteractive','-ExecutionPolicy','Bypass','-File',$runPath,'-PlanPath',$inputPath,'-SourcePath',$source,'-AdapterName',$adapter.Name)) { $start.ArgumentList.Add($arg) }
    $process=[Diagnostics.Process]::Start($start)
    $stdout=$process.StandardOutput.ReadToEndAsync(); $stderr=$process.StandardError.ReadToEndAsync()
    $watch=[Diagnostics.Stopwatch]::StartNew()
    $completed=$process.WaitForExit(45000)
    if (!$completed) { $process.Kill($true); $process.WaitForExit(5000) | Out-Null }
    $errorText=$stderr.GetAwaiter().GetResult(); $outputText=$stdout.GetAwaiter().GetResult()
    $attempt=if ($expectSuccess) {'valid'} else {'invalid'}
    $errorText | Set-Content -LiteralPath (Join-Path $evidence "$attempt-error.log")
    $outputText | Set-Content -LiteralPath (Join-Path $evidence "$attempt-output.log")
    Copy-Item -LiteralPath $inputPath -Destination (Join-Path $evidence "$attempt-plan.json")
    if (!$completed) { throw 'Production firewall creation exceeded the helper timeout.' }
    if ($expectSuccess -and $process.ExitCode -ne 0) { throw "Production firewall creation failed: $errorText" }
    if (!$expectSuccess -and $process.ExitCode -eq 0) { throw 'Malformed firewall prefix unexpectedly succeeded.' }
    return $watch.Elapsed.TotalSeconds
}
function Get-ProductionComplement([string[]]$endpoints) {
    # Use the same C# source and PowerShell compiler as the production helper.
    # NetSecurity rejects several address-range spellings; the production
    # complement returns the exact CIDR representation used for the app.
    $run=Join-Path $directory 'complement.ps1'
    $endpointsPath=Join-Path $directory 'endpoints.json'
    $resultPath=Join-Path $directory 'blocked.json'
    $endpoints | ConvertTo-Json | Set-Content -LiteralPath $endpointsPath -Encoding UTF8
    @'
param([string]$SourcePath,[string]$EndpointsPath,[string]$ResultPath)
$ErrorActionPreference='Stop'
Add-Type -TypeDefinition (Get-Content -LiteralPath $SourcePath -Raw) -ReferencedAssemblies @('System.dll','System.Core.dll','System.Numerics.dll')
$allowed=[string[]](Get-Content -LiteralPath $EndpointsPath -Raw | ConvertFrom-Json)
$blocked=@([Meshway.VPN.FirewallAddressRanges]::Complement($allowed,$false)) + @([Meshway.VPN.FirewallAddressRanges]::Complement($allowed,$true))
$blocked | ConvertTo-Json | Set-Content -LiteralPath $ResultPath -Encoding UTF8
'@ | Set-Content -LiteralPath $run -Encoding UTF8
    & (Join-Path $env:SystemRoot 'System32/WindowsPowerShell/v1.0/powershell.exe') -NoProfile -NonInteractive -ExecutionPolicy Bypass -File $run -SourcePath (Join-Path $root 'apps/windows/Meshway.VpnHelper/FirewallAddressRanges.cs') -EndpointsPath $endpointsPath -ResultPath $resultPath
    if ($LASTEXITCODE -ne 0) { throw 'Production complement generation failed.' }
    $blocked=@(Get-Content -LiteralPath $resultPath -Raw | ConvertFrom-Json)
    Assert-Lab ($blocked.Count -gt 100 -and @($blocked | Where-Object { !($_.Contains('/')) -or $_.Contains('-') }).Count -eq 0) 'Production complement did not return full IPv4/IPv6 CIDR coverage.'
    return $blocked
}
try {
    & go test -c -o $binary ./internal/vpn
    if ($LASTEXITCODE -ne 0) { throw 'Firewall fixture compilation failed.' }
    $env:MESHWAY_WINDOWS_FIREWALL_LAB_DIR=$directory
    $child=Start-Process -FilePath $binary -ArgumentList @('-test.run=TestWindowsFirewallAdapterFixture','-test.v','-test.timeout=180s') -PassThru -RedirectStandardOutput (Join-Path $directory 'fixture.log') -RedirectStandardError (Join-Path $directory 'fixture-error.log')
    $deadline=[DateTime]::UtcNow.AddSeconds(30)
    $ready=Join-Path $directory 'ready.json'
    while (!(Test-Path -LiteralPath $ready)) {
        if ($child.HasExited -or [DateTime]::UtcNow -ge $deadline) { throw 'Owned Wintun firewall fixture did not start.' }
        Start-Sleep -Milliseconds 100
    }
    $name=(Get-Content -LiteralPath $ready -Raw | ConvertFrom-Json).adapter
    Assert-Lab ($name -match '^MeshwayFirewallLab[0-9]+$') 'Invalid fixture adapter name.'
    $adapter=Get-NetAdapter -Name $name -ErrorAction Stop
    # Prove the actual kernel can emit packets before applying its scoped rules.
    Invoke-PacketProbe 4 $false $true; Invoke-PacketProbe 6 $false $true
    $group='Meshway-' + [Guid]::NewGuid().ToString('N'); $groups += $group
    $endpoints=@('10.74.0.12','fd74::12')
    $blocked=Get-ProductionComplement $endpoints
    $plan=@{ id=$group; range_source=(Get-Content -LiteralPath (Join-Path $root 'apps/windows/Meshway.VpnHelper/FirewallAddressRanges.cs') -Raw); Endpoints=$endpoints; blocked=$blocked }
    $creationSeconds=Invoke-ProductionFirewall $plan $true
    $rules=@(Get-NetFirewallRule -Group $group -ErrorAction Stop)
    Assert-Lab ($rules.Count -ge 260) 'Required protocol/transport firewall rules are missing.'
    Assert-Lab (@($rules | Where-Object { $_.Action -ne 'Block' -or $_.Direction -ne 'Outbound' -or $_.Enabled -ne 'True' }).Count -eq 0) 'Firewall rules are not enabled outbound blocks.'
    $interfaces=@($rules | Get-NetFirewallInterfaceFilter)
    $interfaces | Select-Object InstanceID,InterfaceAlias | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $evidence 'interface-filters.json')
    Assert-Lab (@($interfaces | Where-Object { @($_.InterfaceAlias).Count -ne 1 -or $_.InterfaceAlias -ne $name }).Count -eq 0) 'Firewall rules escaped the owned disposable adapter.'
    $raw=@($rules | Where-Object { $_.DisplayName -like '*-protocol-*' })
    Assert-Lab ($raw.Count -eq 253) 'Raw IP protocol coverage is incomplete.'
    $policy=New-Object -ComObject HNetCfg.FwPolicy2
    foreach ($number in @(0,1,127,255)) {
        $sample=$raw | Where-Object { $_.DisplayName -eq "$group-$($adapter.ifIndex)-protocol-$number" }
        $filter=$sample | Get-NetFirewallPortFilter
        $actual=$policy.Rules.Item("$group-$($adapter.ifIndex)-protocol-$number")
        Assert-Lab ($null -ne $filter -and $actual.Protocol -eq $number) "Raw protocol $number did not create its actual Windows filter."
    }
    $transport=@($rules | Where-Object { $_.DisplayName -like '*-transport-*' } | Get-NetFirewallPortFilter)
    $transport | Select-Object InstanceID,Protocol,LocalPort,RemotePort,IcmpType | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $evidence 'transport-filters.json')
    Assert-Lab (@($transport | Where-Object { "$($_.Protocol)" -in @('TCP','6') }).Count -gt 0) 'TCP blocking filters missing.'
    $udp=@($transport | Where-Object { "$($_.Protocol)" -in @('UDP','17') })
    Assert-Lab (@($udp | Where-Object { '68' -in $_.LocalPort -and '67' -in $_.RemotePort }).Count -gt 0) 'Scoped DHCPv4 blocking complement missing.'
    Assert-Lab (@($udp | Where-Object { '546' -in $_.LocalPort -and '547' -in $_.RemotePort }).Count -gt 0) 'Scoped DHCPv6 blocking complement missing.'
    $icmp=@($transport | Where-Object { "$($_.Protocol)" -in @('ICMPv6','58') })
    $control=@($icmp | Where-Object { (@($_.IcmpType | ForEach-Object { "$_".Split(':')[0] }) -join ',') -eq '133,134,135,136' })
    Assert-Lab ($control.Count -gt 0) 'IPv6 neighbor/router discovery complement filters missing.'
    Assert-Lab (@($icmp | Where-Object { @($_.IcmpType | ForEach-Object { "$_".Split(':')[0] }) -contains '128' }).Count -gt 0) 'Ordinary IPv6 ICMP traffic is not blocked.'
    # Real Windows firewall enforcement: ordinary packets disappear at Wintun,
    # while exact peer transport packets continue to reach the same adapter.
    Invoke-PacketProbe 4 $false $false; Invoke-PacketProbe 6 $false $false
    Invoke-PacketProbe 4 $true $true; Invoke-PacketProbe 6 $true $true
    $rules | Remove-NetFirewallRule
    Assert-Lab (@(Get-NetFirewallRule -Group $group -ErrorAction SilentlyContinue).Count -eq 0) 'Owned firewall rules remained after cleanup.'
    Invoke-PacketProbe 4 $false $true; Invoke-PacketProbe 6 $false $true
    # Force a partial setup failure and use the exact production group cleanup.
    $badGroup='Meshway-' + [Guid]::NewGuid().ToString('N'); $groups += $badGroup
    $bad=@{} + $plan; $bad.id=$badGroup; $bad.blocked=@('invalid-prefix')
    $null=Invoke-ProductionFirewall $bad $false
    $partial=@(Get-NetFirewallRule -Group $badGroup -ErrorAction SilentlyContinue)
    Assert-Lab ($partial.Count -gt 0) 'Negative case did not exercise partial creation.'
    $partial | Remove-NetFirewallRule
    Assert-Lab (@(Get-NetFirewallRule -Group $badGroup -ErrorAction SilentlyContinue).Count -eq 0) 'Partial failure rules remained after cleanup.'
    New-Item -ItemType File -Path (Join-Path $directory 'stop') | Out-Null
    if (!$child.WaitForExit(10000) -or $child.ExitCode -ne 0) { throw 'Owned adapter fixture did not stop cleanly.' }
    $afterRoutes=Get-NetRoute | Where-Object { $_.DestinationPrefix -in @('0.0.0.0/0','::/0') } | Select-Object InterfaceIndex,DestinationPrefix,NextHop,RouteMetric | Sort-Object InterfaceIndex,DestinationPrefix | ConvertTo-Json -Compress
    $afterDns=Get-DnsClientServerAddress | Where-Object { $_.InterfaceAlias -notlike 'MeshwayFirewallLab*' } | Select-Object InterfaceIndex,AddressFamily,ServerAddresses | Sort-Object InterfaceIndex,AddressFamily | ConvertTo-Json -Compress
    Assert-Lab ($beforeRoutes -eq $afterRoutes) 'Firewall lab changed default routes.'
    Assert-Lab ($beforeDns -eq $afterDns) 'Firewall lab changed DNS.'
    $result=@{ platform='Windows'; production_firewall_creation='PASS'; raw_ip_protocol_filters='PASS'; ndp_dhcp_control_filters='PASS'; actual_kernel_udp_ipv4_ipv6_block='PASS'; exact_peer_udp_bypass='PASS'; cleanup_restores_kernel_udp='PASS'; partial_failure_cleanup='PASS'; only_owned_adapter=$true; default_routes_unchanged=$true; dns_unchanged=$true; creation_seconds=$creationSeconds; time=[DateTime]::UtcNow.ToString('o') }
    New-Item -ItemType Directory -Force -Path (Split-Path $Output -Parent) | Out-Null
    $result | ConvertTo-Json | Set-Content -LiteralPath $Output
    Write-Output 'PASS production Windows firewall: actual adapter-scoped filters, kernel UDP blocking/bypass, control protocol filters and cleanup.'
} finally {
    foreach ($group in $groups) { Get-NetFirewallRule -Group $group -ErrorAction SilentlyContinue | Remove-NetFirewallRule -ErrorAction SilentlyContinue }
    if ($child -and !$child.HasExited) { New-Item -ItemType File -Force -Path (Join-Path $directory 'stop') -ErrorAction SilentlyContinue | Out-Null; if (!$child.WaitForExit(10000)) { $child.Kill($true) } }
    if (Test-Path -LiteralPath (Join-Path $directory 'fixture.log')) { Get-Content -LiteralPath (Join-Path $directory 'fixture.log') }
    if (Test-Path -LiteralPath (Join-Path $directory 'fixture-error.log')) { Get-Content -LiteralPath (Join-Path $directory 'fixture-error.log') }
    foreach ($file in @('fixture.log','fixture-error.log','blocked.json','apply.ps1','complement.ps1')) { if (Test-Path -LiteralPath (Join-Path $directory $file)) { Copy-Item -LiteralPath (Join-Path $directory $file) -Destination (Join-Path $evidence $file) -Force } }
    Copy-Item -LiteralPath (Join-Path $root 'apps/windows/Meshway.VpnHelper/firewall.ps1') -Destination (Join-Path $evidence 'production-firewall.ps1') -Force
    Copy-Item -LiteralPath (Join-Path $root 'apps/windows/Meshway.VpnHelper/FirewallAddressRanges.cs') -Destination (Join-Path $evidence 'production-address-ranges.cs') -Force
    if ($previousFixtureDirectory) { $env:MESHWAY_WINDOWS_FIREWALL_LAB_DIR=$previousFixtureDirectory } else { Remove-Item Env:MESHWAY_WINDOWS_FIREWALL_LAB_DIR -ErrorAction SilentlyContinue }
    Remove-Item -LiteralPath $binary -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $directory -Recurse -Force -ErrorAction SilentlyContinue
}
