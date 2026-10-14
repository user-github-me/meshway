using System.Diagnostics;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;

namespace Meshway.VPN;

public sealed class NetworkTransaction : IAsyncDisposable
{
    public string ID { get; } = "Meshway-" + Guid.NewGuid().ToString("N");
    private readonly string journal;
    private bool applied;
    public NetworkTransaction(string journal) => this.journal = journal;

    private async Task Run(string script, object input)
    {
        var folder = Path.GetDirectoryName(journal)!;
        Directory.CreateDirectory(folder);
        var scriptPath = Path.Combine(folder, "network-" + Guid.NewGuid().ToString("N") + ".ps1");
        var inputPath = scriptPath + ".json";
        try
        {
            await File.WriteAllTextAsync(scriptPath, script, new UTF8Encoding(false));
            await File.WriteAllTextAsync(inputPath, JsonSerializer.Serialize(input));
            var info = new ProcessStartInfo(Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.System), "WindowsPowerShell", "v1.0", "powershell.exe"))
            { UseShellExecute = false, CreateNoWindow = true, RedirectStandardError = true, RedirectStandardOutput = true };
            foreach (var arg in new[] { "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", scriptPath, "-InputPath", inputPath }) info.ArgumentList.Add(arg);
            using var child = Process.Start(info) ?? throw new IOException("Could not run Windows VPN network setup.");
            var output = child.StandardOutput.ReadToEndAsync(); var errors = child.StandardError.ReadToEndAsync();
            using var limit = new CancellationTokenSource(TimeSpan.FromSeconds(45));
            try { await child.WaitForExitAsync(limit.Token); }
            catch { try { child.Kill(true); } catch { } throw; }
            _ = await output; var error = await errors;
            if (child.ExitCode != 0) throw new IOException("Windows VPN network operation failed: " + error[..Math.Min(error.Length, 1024)]);
        }
        finally { File.Delete(scriptPath); File.Delete(inputPath); }
    }

    private static string EmbeddedText(string name)
    {
        using var input = typeof(NetworkTransaction).Assembly.GetManifestResourceStream(name) ?? throw new IOException("Missing embedded firewall implementation.");
        using var reader = new StreamReader(input);
        return reader.ReadToEnd();
    }

    public async Task Apply(VPNPlan plan)
    {
        if (File.Exists(journal)) await Restore(journal);
        // Write an ownership marker before touching networking. Every rollback
        // operation targets only this random transaction's rules and adapter.
        await File.WriteAllTextAsync(journal, JsonSerializer.Serialize(new { id = ID, interface_name = VPNPlan.InterfaceName, routes = Array.Empty<object>() }));
        applied = true;
        var firewall = EmbeddedText("Meshway.Firewall.ps1");
        var source = EmbeddedText("Meshway.FirewallAddressRanges.cs");
        try { await Run(ApplyScript.Replace("__FIREWALL_HELPERS__", firewall), new { id = ID, journal, plan.Endpoints, blocked = plan.BlockedCIDRs, adapter = VPNPlan.InterfaceName, range_source = source }); }
        catch { await Restore(journal); applied = false; throw; }
    }

    public async Task Restore(string path)
    {
        if (!File.Exists(path)) return;
        var record = JsonSerializer.Deserialize<JsonElement>(await File.ReadAllTextAsync(path));
        var id = record.GetProperty("id").GetString() ?? "";
        if (!id.StartsWith("Meshway-", StringComparison.Ordinal) || id.Length != 40) throw new InvalidDataException("Invalid VPN cleanup ownership marker.");
        await Run(RestoreScript, new { journal = path });
        File.Delete(path);
    }

    public async ValueTask DisposeAsync() { if (applied) { await Restore(journal); applied = false; } }

    private const string ApplyScript = """
param([string]$InputPath)
$ErrorActionPreference = 'Stop'
$p = Get-Content -LiteralPath $InputPath -Raw | ConvertFrom-Json
__FIREWALL_HELPERS__
$adapter = Get-NetAdapter -Name $p.adapter -ErrorAction Stop
$physical = @(Get-NetAdapter | Where-Object { $_.ifIndex -ne $adapter.ifIndex -and $_.Status -eq 'Up' -and $_.ifIndex -ne 1 })
if ($physical.Count -eq 0) { throw 'No active physical underlay interface. VPN setup stopped.' }
$record = @{ id=$p.id; interface_name=$p.adapter; interface_index=$adapter.ifIndex; routes=@(); nrpt=@() }
function Save-Journal { $record | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $p.journal -Encoding UTF8 }
Save-Journal
# Snapshot the best underlay route BEFORE adding VPN defaults.
foreach ($endpoint in $p.Endpoints) {
    $route = Find-NetRoute -RemoteIPAddress $endpoint | Select-Object -Last 1
    if (!$route -or $route.InterfaceIndex -eq $adapter.ifIndex) { throw 'Cannot identify a physical route to server.' }
    $prefix = if ($endpoint.Contains(':')) { "$endpoint/128" } else { "$endpoint/32" }
    if (!(Get-NetRoute -DestinationPrefix $prefix -InterfaceIndex $route.InterfaceIndex -ErrorAction SilentlyContinue)) {
        $owned = @{ DestinationPrefix=$prefix; InterfaceIndex=$route.InterfaceIndex; NextHop=$route.NextHop }
        $record.routes += $owned; Save-Journal
        New-NetRoute @owned -RouteMetric 1 -PolicyStore ActiveStore | Out-Null
    }
}
# Preserve NDP/lease control while blocking TCP, ordinary UDP/DNS and raw IP.
foreach ($nic in $physical) { Add-MeshwayFirewall -Plan $p -Adapter $nic }
Set-NetIPInterface -InterfaceIndex $adapter.ifIndex -AddressFamily IPv4 -NlMtuBytes 1280 -InterfaceMetric 1 -Dhcp Disabled
Set-NetIPInterface -InterfaceIndex $adapter.ifIndex -AddressFamily IPv6 -NlMtuBytes 1280 -InterfaceMetric 1 -Dhcp Disabled
New-NetIPAddress -InterfaceIndex $adapter.ifIndex -IPAddress '10.77.0.2' -PrefixLength 32 -PolicyStore ActiveStore | Out-Null
New-NetIPAddress -InterfaceIndex $adapter.ifIndex -IPAddress 'fd77::2' -PrefixLength 128 -PolicyStore ActiveStore | Out-Null
# Windows may return from address creation before duplicate address detection
# finishes. Activate DNS and routes only once both source addresses are usable.
$addressDeadline = [DateTime]::UtcNow.AddSeconds(15)
do {
    $preferred = @(Get-NetIPAddress -InterfaceIndex $adapter.ifIndex -ErrorAction Stop | Where-Object { $_.IPAddress -in @('10.77.0.2','fd77::2') -and $_.AddressState -eq 'Preferred' })
    if ($preferred.Count -eq 2) { break }
    if ([DateTime]::UtcNow -ge $addressDeadline) { throw 'Tunnel addresses did not become ready. VPN network setup will be rolled back.' }
    Start-Sleep -Milliseconds 200
} while ($true)
Set-DnsClientServerAddress -InterfaceIndex $adapter.ifIndex -ServerAddresses @('10.77.0.1','fd77::1')
# Two /1 routes take precedence without editing any pre-existing default route.
foreach ($prefix in @('0.0.0.0/1','128.0.0.0/1','::/1','8000::/1')) {
    $next = if ($prefix.Contains(':')) { '::' } else { '0.0.0.0' }
    New-NetRoute -InterfaceIndex $adapter.ifIndex -DestinationPrefix $prefix -NextHop $next -RouteMetric 1 -PolicyStore ActiveStore | Out-Null
}
# Windows multihomed DNS must not race DNS over another interface. An owned
# NRPT rule sends every system resolver request to virtual tunnel DNS.
$rule = Add-DnsClientNrptRule -Namespace '.' -NameServers @('10.77.0.1','fd77::1') -Comment $p.id -PassThru
$record.nrpt += $rule.Name; Save-Journal
""";

    private const string RestoreScript = """
param([string]$InputPath)
$ErrorActionPreference = 'Stop'
$p = Get-Content -LiteralPath $InputPath -Raw | ConvertFrom-Json
$r = Get-Content -LiteralPath $p.journal -Raw | ConvertFrom-Json
if ($r.id -notmatch '^Meshway-[a-f0-9]{32}$' -or $r.interface_name -ne 'Meshway') { throw 'Invalid cleanup ownership record.' }
# Remove only the exact journaled underlay bypass routes and owned rules.
foreach ($route in $r.routes) {
    Get-NetRoute -DestinationPrefix $route.DestinationPrefix -InterfaceIndex $route.InterfaceIndex -ErrorAction SilentlyContinue | Where-Object { $_.NextHop -eq $route.NextHop } | Remove-NetRoute -Confirm:$false
}
Get-DnsClientNrptRule | Where-Object { $_.Comment -eq $r.id } | Remove-DnsClientNrptRule -Force
$adapter = Get-NetAdapter -Name $r.interface_name -ErrorAction SilentlyContinue
if ($adapter) {
    Set-DnsClientServerAddress -InterfaceIndex $adapter.ifIndex -ResetServerAddresses
    Get-NetRoute -InterfaceIndex $adapter.ifIndex -ErrorAction SilentlyContinue | Where-Object { $_.DestinationPrefix -in @('0.0.0.0/1','128.0.0.0/1','::/1','8000::/1') } | Remove-NetRoute -Confirm:$false
    Get-NetIPAddress -InterfaceIndex $adapter.ifIndex -ErrorAction SilentlyContinue | Where-Object { $_.IPAddress -in @('10.77.0.2','fd77::2') } | Remove-NetIPAddress -Confirm:$false
}
Get-NetFirewallRule -Group $r.id -ErrorAction SilentlyContinue | Remove-NetFirewallRule
""";
}
