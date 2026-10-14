# Embedded in the elevated helper and reused by the disposable Windows CI lab.
# All rules belong to a random Meshway transaction and one explicit adapter.
function Add-MeshwayFirewall {
    param($Plan, $Adapter)
    if (!('Meshway.VPN.FirewallAddressRanges' -as [type])) { Add-Type -TypeDefinition $Plan.range_source -ReferencedAssemblies @('System.dll','System.Core.dll','System.Numerics.dll') -ErrorAction Stop }
    $policy = New-Object -ComObject HNetCfg.FwPolicy2
    $script:meshwayFirewallSerial = 0
    function Block-Remote {
        param([string]$Protocol, [string[]]$Addresses, [string[]]$LocalPorts, [string[]]$RemotePorts, [string[]]$IcmpTypes)
        for ($index=0; $index -lt $Addresses.Count; $index+=100) {
            $last = [Math]::Min($index+99,$Addresses.Count-1)
            $arguments = @{ DisplayName="$($Plan.id)-$($Adapter.ifIndex)-transport-$script:meshwayFirewallSerial"; Group=$Plan.id; Direction='Outbound'; Action='Block'; InterfaceAlias=$Adapter.Name; RemoteAddress=$Addresses[$index..$last]; Profile='Any'; Protocol=$Protocol }
            if ($LocalPorts) { $arguments.LocalPort=$LocalPorts }
            if ($RemotePorts) { $arguments.RemotePort=$RemotePorts }
            if ($IcmpTypes) { $arguments.IcmpType=$IcmpTypes }
            try { New-NetFirewallRule @arguments | Out-Null }
            catch {
                $sample = ($Addresses[$index..$last] | Select-Object -First 6) -join ', '
                throw "Firewall protocol $Protocol rejected a $($last-$index+1)-prefix batch [$sample]: $($_.Exception.Message)"
            }
            $script:meshwayFirewallSerial++
        }
    }
    # COM avoids hundreds of expensive NetSecurity cmdlet round trips. Raw IP
    # protocols are blocked on this adapter regardless of destination. TCP/UDP
    # are handled separately so required link control can survive lease/cache
    # refresh. Ordinary applications receive no broad link-local exemption.
    foreach ($protocol in 0..255) {
        if ($protocol -in @(6,17,58)) { continue }
        $rule = New-Object -ComObject HNetCfg.FWRule
        $rule.Name = "$($Plan.id)-$($Adapter.ifIndex)-protocol-$protocol"
        $rule.Grouping = $Plan.id
        $rule.Protocol = $protocol
        $rule.Direction = 2
        $rule.Action = 0
        $rule.Interfaces = @($Adapter.Name)
        $rule.Profiles = 2147483647
        $rule.Enabled = $true
        $policy.Rules.Add($rule)
    }
    Block-Remote -Protocol TCP -Addresses $Plan.blocked
    foreach ($ipv6 in @($false,$true)) {
        $addresses = @($Plan.blocked | Where-Object { $_.Contains(':') -eq $ipv6 })
        $local = if ($ipv6) { 546 } else { 68 }
        $remote = if ($ipv6) { 547 } else { 67 }
        $otherLocal = if ($ipv6) { @('0-545','547-65535') } else { @('0-67','69-65535') }
        $otherRemote = if ($ipv6) { @('0-546','548-65535') } else { @('0-66','68-65535') }
        Block-Remote -Protocol UDP -Addresses $addresses -LocalPorts $otherLocal
        Block-Remote -Protocol UDP -Addresses $addresses -LocalPorts @("$local") -RemotePorts $otherRemote
        $control = if ($ipv6) { @('fe80::/10','ff02::/16') } else { @('255.255.255.255/32') }
        if (!$ipv6) {
            $lease = Get-CimInstance Win32_NetworkAdapterConfiguration -Filter "InterfaceIndex = $($Adapter.ifIndex)" -ErrorAction Stop
            if ($lease.DHCPServer) { $control += $lease.DHCPServer }
        }
        $excluded = @($Plan.Endpoints) + @($control)
        $nonControl = [Meshway.VPN.FirewallAddressRanges]::Complement([string[]]$excluded,$ipv6)
        Block-Remote -Protocol UDP -Addresses $nonControl -LocalPorts @("$local") -RemotePorts @("$remote")
    }
    # ICMPv6 NDP control types only. Neighbor advertisements may be unicast to
    # an on-link global address, so include physical on-link IPv6 prefixes.
    $ndp = @('fe80::/10','ff02::/16')
    $ndp += @(Get-NetRoute -InterfaceIndex $Adapter.ifIndex -AddressFamily IPv6 -ErrorAction Stop | Where-Object { $_.NextHop -eq '::' -and $_.DestinationPrefix -notin @('::/0','::/1','8000::/1') } | ForEach-Object { $_.DestinationPrefix })
    $outsideLink = [Meshway.VPN.FirewallAddressRanges]::Complement([string[]]$ndp,$true)
    $otherTypes = @(0..255 | Where-Object { $_ -notin @(133,134,135,136) } | ForEach-Object { "$_" })
    # Windows PowerShell 5.1 NetSecurity rejects the all-IPv6 ::/0 prefix
    # on an ICMPv6 rule; protocol 58 already supplies its address family.
    Block-Remote -Protocol ICMPv6 -Addresses @('Any') -IcmpTypes $otherTypes
    Block-Remote -Protocol ICMPv6 -Addresses $outsideLink -IcmpTypes @('133','134','135','136')
}
