using System.Diagnostics;
using System.IO;
using System.Text.Json;

namespace Meshway;

public sealed class DeviceVPNController
{
    private Process? helper;
    private string? requestPath;
    public bool Running => helper is { HasExited: false };

    public async Task Start(string configPath, string peerID, string address)
    {
        if (Running) throw new InvalidOperationException("Disconnect the current device VPN before selecting another server.");
        var directory = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "Meshway");
        Directory.CreateDirectory(directory);
        requestPath = Path.Combine(directory, "vpn-" + Guid.NewGuid().ToString("N") + ".json");
        var executable = Path.Combine(AppContext.BaseDirectory, "meshway-vpn-helper.exe");
        if (!File.Exists(executable) || !File.Exists(Path.Combine(AppContext.BaseDirectory, "meshway-daemon.exe")) || !File.Exists(Path.Combine(AppContext.BaseDirectory, "wintun.dll")))
            throw new FileNotFoundException("Install the complete Windows VPN package with its helper, daemon, and signed Wintun driver library.");
        await File.WriteAllTextAsync(requestPath, JsonSerializer.Serialize(new { config = configPath, peer_id = peerID, address, parent_pid = Environment.ProcessId }));
        // UAC is the operating system's consent for network changes. The
        // helper receives a private file path, never secrets on its command line.
        var start = new ProcessStartInfo(executable) { UseShellExecute = true, Verb = "runas", WorkingDirectory = AppContext.BaseDirectory };
        start.ArgumentList.Add(requestPath);
        helper = Process.Start(start) ?? throw new IOException("Windows could not launch the VPN helper.");
        var statusPath = requestPath + ".status";
        for (var i = 0; i < 180; i++)
        {
            if (File.Exists(statusPath))
            {
                using var status = JsonDocument.Parse(await File.ReadAllTextAsync(statusPath));
                if (status.RootElement.GetProperty("active").GetBoolean()) return;
                throw new IOException(status.RootElement.GetProperty("message").GetString());
            }
            if (helper.HasExited) throw new IOException("VPN helper stopped before connecting. Check administrator consent and the server's VPN exit role.");
            await Task.Delay(250);
        }
        await Stop();
        throw new IOException("VPN setup timed out before confirming routes and encrypted transport.");
    }

    public async Task Stop()
    {
        if (requestPath != null) await File.WriteAllTextAsync(requestPath + ".stop", "stop");
        if (helper is { HasExited: false })
        {
            var exit = helper.WaitForExitAsync();
            if (await Task.WhenAny(exit, Task.Delay(15000)) != exit)
                throw new IOException("VPN cleanup is still running. Retry Disconnect; the helper retains its ownership journal.");
        }
        helper?.Dispose(); helper = null;
        if (requestPath != null)
        {
            foreach (var suffix in new[] { "", ".status", ".stop" }) File.Delete(requestPath + suffix);
            requestPath = null;
        }
    }

    public void SignalStop() { if (requestPath != null) File.WriteAllText(requestPath + ".stop", "stop"); }
}
