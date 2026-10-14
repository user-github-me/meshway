using System.ComponentModel;
using System.Diagnostics;
using System.IO;
using System.Net.Http;
using System.Net.Http.Headers;
using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Threading;
using Microsoft.Win32;
using Meshway.Security;

namespace Meshway;

public partial class MainWindow : Window
{
    private readonly string configPath = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "Meshway", "config.json");
    private readonly HttpClient client = new() { Timeout = TimeSpan.FromSeconds(5) };
    private NativeConfiguration config = null!;
    private Process? ownedProcess;
    private readonly DeviceVPNController deviceVPN = new();
    private bool busy;
    private bool refreshing;
    private bool live;
    private int previousMode;
    private readonly string modePath = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "Meshway", "connection-mode.txt");
    private readonly DispatcherTimer timer = new() { Interval = TimeSpan.FromSeconds(3) };
    private readonly object logLock = new();

    public MainWindow()
    {
        InitializeComponent();
        try
        {
            config = NativeConfiguration.LoadOrCreate(configPath);
            StateDirectory.Text = config.String("state_dir");
            SocksListen.Text = config.String("socks_listen");
            ApiListen.Text = config.String("api_listen");
            ApiToken.Password = config.String("api_token");
            UpstreamPeer.Text = config.String("upstream_peer");
            BootstrapPeers.Text = config.Lines("bootstrap_peers");
            AllowedPeers.Text = config.Lines("allowed_peers");
            RelayRole.IsChecked = config.Boolean("relay");
            PersonalServer.IsChecked = config.Boolean("personal_server");
            ExitRole.IsChecked = config.Boolean("exit");
            PrivateTargets.IsChecked = config.Boolean("allow_private_targets");
            ExecutablePath.Text = File.Exists(Path.Combine(AppContext.BaseDirectory, "meshway-daemon.exe")) ? Path.Combine(AppContext.BaseDirectory, "meshway-daemon.exe") : "";
            var preferences = Path.Combine(Path.GetDirectoryName(configPath)!, "executable.txt");
            if (File.Exists(preferences)) ExecutablePath.Text = File.ReadAllText(preferences);
            if (File.Exists(modePath) && int.TryParse(File.ReadAllText(modePath), out var mode) && mode is >= 0 and <= 2) ConnectionMode.SelectedIndex = mode;
            ConnectionModeChanged(ConnectionMode, null!);
            if (config.Data["personal_transport"] is JsonObject transport)
            {
                UseSSH.IsChecked = transport["kind"]?.GetValue<string>() == "ssh";
                SSHAlias.Text = transport["ssh_alias"]?.GetValue<string>() ?? "";
                SSHPeer.Text = transport["peer_id"]?.GetValue<string>() ?? "";
                SSHLocalPort.Text = transport["local_port"]?.ToString() ?? "44001";
                SSHRemotePort.Text = transport["remote_port"]?.ToString() ?? "44001";
                if (UseSSH.IsChecked == true) PersonalDeviceVPN.IsChecked = false;
            }
            RenderTunnels();
            timer.Tick += async (_, _) => await Refresh(false);
            timer.Start();
            Loaded += async (_, _) => await Refresh(false);
        }
        catch (Exception error) { MessageText.Text = "Cannot load configuration: " + error.Message + ". Existing file has been preserved."; StartButton.IsEnabled = false; SettingsGroup.IsEnabled = false; }
    }

    private void Save()
    {
        config.Data["state_dir"] = StateDirectory.Text.Trim();
        config.Data["socks_listen"] = SocksListen.Text.Trim();
        config.Data["api_listen"] = ApiListen.Text.Trim();
        config.Data["api_token"] = ApiToken.Password;
        config.Data["upstream_peer"] = UpstreamPeer.Text.Trim();
        config.Data["relay"] = RelayRole.IsChecked == true;
        config.Data["personal_server"] = PersonalServer.IsChecked == true;
        if (PersonalServer.IsChecked == true) config.Data["advertise"] = true;
        config.Data["exit"] = ExitRole.IsChecked == true;
        config.Data["allow_private_targets"] = PrivateTargets.IsChecked == true;
        config.SetLines("bootstrap_peers", BootstrapPeers.Text);
        config.SetLines("allowed_peers", AllowedPeers.Text);
        config.Save(configPath);
        File.WriteAllText(Path.Combine(Path.GetDirectoryName(configPath)!, "executable.txt"), ExecutablePath.Text.Trim());
    }

    private async Task<JsonNode?> Request(string route, string? json = null, HttpMethod? method = null)
    {
        using var request = new HttpRequestMessage(method ?? (json == null ? HttpMethod.Get : HttpMethod.Post), new Uri(config.ApiUri(), route));
        request.Headers.Authorization = new AuthenticationHeaderValue("Bearer", config.String("api_token"));
        if (json != null) request.Content = new StringContent(json, Encoding.UTF8, "application/json");
        using var response = await client.SendAsync(request);
        var data = await response.Content.ReadAsStringAsync();
        if (!response.IsSuccessStatusCode) throw new HttpRequestException($"Daemon HTTP {(int)response.StatusCode}: {data[..Math.Min(data.Length, 512)]}");
        return JsonNode.Parse(data);
    }

    private async Task Refresh(bool reportError)
    {
        if (config == null || refreshing) return;
        refreshing = true;
        try
        {
            var status = await Request("v1/status");
            live = true;
            StateText.Text = "Daemon running";
            if (status?["vpn"]?["active"]?.GetValue<bool>() == true && deviceVPN.Running) StateText.Text = "Device VPN connected";
            else if (deviceVPN.Running) StateText.Text = "VPN reconnecting · traffic blocked";
            else if (status?["upstream_peer"]?.GetValue<string>() is { Length: > 0 }) StateText.Text = status?["upstream_connected"]?.GetValue<bool>() == true ? "Selected peer connection is live" : "Selected peer offline · reconnects on traffic";
            var listener = status?["socks_listen"]?.GetValue<string>() ?? "";
            var peerConnected = status?["upstream_connected"]?.GetValue<bool>() == true;
            ConnectionSummary.Text = deviceVPN.Running ? StateText.Text : listener.Length == 0 ? "SOCKS listener disabled" : $"SOCKS {listener} · {(peerConnected ? "selected exit reachable" : "choose a reachable approved exit")}";
            var forwards = status?["configuration_summary"]?["forwards"]?.AsArray();
            if (forwards is { Count: > 0 }) ConnectionSummary.Text += $" · {forwards.Count} configured TCP listener(s)";
            TrafficSummary.Text = $"Traffic: ↑ {Bytes(status?["bytes_sent"])}  ↓ {Bytes(status?["bytes_received"])} · SOCKS payload + VPN packets; transport overhead excluded";
            var transport = status?["transport"];
            TransportSummary.Text = transport?["kind"]?.GetValue<string>() == "ssh"
                ? transport?["authenticated"]?.GetValue<bool>() == true ? "SSH transport · authenticated Meshway peer" : transport?["running"]?.GetValue<bool>() == true ? "SSH transport · waiting for the pinned Meshway peer" : "SSH transport unavailable · " + (transport?["error"]?.GetValue<string>() ?? "start Meshway after saving")
                : config.Data["personal_transport"] != null ? "Personal SSH transport configured · restart Meshway to apply" : "Direct Meshway TCP/QUIC transport";
            StatusText.Text = $"Peer: {status?["peer_id"]}\nSOCKS5: {status?["socks_listen"]}\nPeers: {status?["peers"]?.AsArray().Count ?? 0}\nSent: {status?["bytes_sent"]} bytes · Received: {status?["bytes_received"]} bytes\n" + string.Join("\n", status?["addresses"]?.AsArray().Select(x => x!.GetValue<string>()) ?? []);
            try { await RefreshServers(status?["upstream_peer"]?.GetValue<string>() ?? "", peerConnected); }
            catch (Exception error) { CatalogMessage.Text = "Server list unavailable: " + error.Message; ServerList.Children.Clear(); }
        }
        catch (Exception error) { live = false; StateText.Text = deviceVPN.Running ? "VPN helper running · status unavailable" : "Daemon offline"; ConnectionSummary.Text = StateText.Text; TrafficSummary.Text = "Traffic: —"; StatusText.Text = ""; if (reportError) MessageText.Text = error.Message; }
        finally { refreshing = false; UpdateButtons(); }
    }

    private void UpdateButtons()
    {
        StartButton.IsEnabled = !busy && !live && ownedProcess == null && config != null;
        StopButton.IsEnabled = !busy && ownedProcess != null;
        SettingsGroup.IsEnabled = !busy && !live && ownedProcess == null && config != null;
        ConnectionMode.IsEnabled = !busy;
        foreach (var field in new Control[] { TunnelName, TunnelListen, TunnelPeer, TunnelService, AddTunnelButton, UseSSH, SSHAlias, SSHPeer, SSHLocalPort, SSHRemotePort, SaveSSHButton, PersonalDeviceVPN }) field.IsEnabled = !busy && !live && ownedProcess == null && !deviceVPN.Running;
        RenderTunnels();
    }

    private async Task RefreshServers(string selectedPeer, bool selectedReachable)
    {
        var catalog = await Request("v1/servers");
        var servers = catalog?["servers"]?.AsArray();
        ServerList.Children.Clear();
        CatalogMessage.Text = servers is { Count: > 0 } ? "Choose a server to connect." : "No servers yet. Add an invitation address from a friend or your server.";
        if (servers == null) return;
        foreach (var server in servers)
        {
            if (ConnectionMode.SelectedIndex == 2 && server?["personal"]?.GetValue<bool>() != true) continue;
            var id = server?["peer_id"]?.GetValue<string>() ?? "";
            var label = server?["label"]?.GetValue<string>() ?? "Peer server";
            var country = server?["country"]?.GetValue<string>() ?? "";
            var address = server?["address"]?.GetValue<string>() ?? "";
            var row = new DockPanel { Margin = new Thickness(0, 0, 0, 12) };
            var wantsDeviceVPN = ConnectionMode.SelectedIndex == 0 || (ConnectionMode.SelectedIndex == 2 && PersonalDeviceVPN.IsChecked == true);
            var selectedAndActive = id == selectedPeer && (wantsDeviceVPN ? deviceVPN.Running : selectedReachable);
            var connect = new Button { Content = selectedAndActive ? "Selected" : "Connect", Padding = new Thickness(14, 7, 14, 7), IsEnabled = !busy && !selectedAndActive && server?["available"]?.GetValue<bool>() == true && server?["can_connect"]?.GetValue<bool>() == true };
            DockPanel.SetDock(connect, Dock.Right);
            connect.Click += async (_, _) =>
            {
                if (busy) return;
                busy = true; UpdateButtons();
                try {
                    if (ConnectionMode.SelectedIndex == 0 || (ConnectionMode.SelectedIndex == 2 && PersonalDeviceVPN.IsChecked == true)) {
                        if (config.Data["personal_transport"]?["kind"]?.GetValue<string>() == "ssh") throw new InvalidOperationException("Use SOCKS capture for the SSH personal transport in this build. Full-device VPN needs the SSH endpoint included in its route/firewall bypass.");
                        var foreground = ownedProcess ?? throw new InvalidOperationException("Start Meshway from this app before connecting a device VPN.");
                        await Request("v1/shutdown", "{}");
                        var exited = foreground.WaitForExitAsync();
                        if (await Task.WhenAny(exited, Task.Delay(5000)) != exited) throw new IOException("The Meshway daemon is still stopping. Retry after it stops; the device VPN was not started.");
                        await exited;
                        live = false;
                        await deviceVPN.Start(configPath, id, address);
                        MessageText.Text = "Device VPN connected to " + label + ".";
                        await Refresh(true);
                        return;
                    }
                    if (deviceVPN.Running) throw new InvalidOperationException("Disconnect the device VPN before selecting SOCKS or tunnel mode.");
                    await Request("v1/select", JsonSerializer.Serialize(new { peer_id = id, address })); MessageText.Text = "Selected " + label + ". Applications can use the Meshway SOCKS connection."; await Refresh(true); }
                catch (Exception error) { MessageText.Text = error.Message; }
                finally { busy = false; UpdateButtons(); }
            };
            row.Children.Add(connect);
            row.Children.Add(new TextBlock { Text = label + (country.Length == 0 ? "" : " · " + country), FontSize = 16, VerticalAlignment = VerticalAlignment.Center });
            ServerList.Children.Add(row);
        }
        if (ConnectionMode.SelectedIndex == 2 && ServerList.Children.Count == 0) CatalogMessage.Text = "No personal server found. Import the same recovery key on your server, then add its Meshway invitation.";
    }

    private async void StartClicked(object sender, RoutedEventArgs e)
    {
        if (busy || ownedProcess != null) return;
        busy = true; UpdateButtons();
        try
        {
            Save();
            try { _ = await Request("v1/status"); live = true; MessageText.Text = "Viewing an already running daemon. Stop it with its original launcher."; return; } catch (HttpRequestException) { }
            var executable = ExecutablePath.Text.Trim();
            if (!File.Exists(executable)) throw new FileNotFoundException("Choose the built Meshway daemon executable first.");
            var info = new ProcessStartInfo(executable) { UseShellExecute = false, CreateNoWindow = true, RedirectStandardError = true, RedirectStandardOutput = true };
            info.Environment["MESHWAY_STATE_KEY"] = Convert.ToBase64String(StateKey.LoadOrCreate(configPath));
            info.ArgumentList.Add("daemon"); info.ArgumentList.Add("--config"); info.ArgumentList.Add(configPath);
            var process = new Process { StartInfo = info, EnableRaisingEvents = true };
            process.OutputDataReceived += (_, args) => Log(args.Data);
            process.ErrorDataReceived += (_, args) => Log(args.Data);
            process.Exited += (_, _) => Dispatcher.InvokeAsync(() => { if (ownedProcess != process) return; ownedProcess = null; live = false; StateText.Text = "Daemon offline"; MessageText.Text = $"Daemon stopped (exit {process.ExitCode}). Inspect daemon.log if unexpected."; process.Dispose(); UpdateButtons(); });
            if (!process.Start()) throw new IOException("Could not start daemon.");
            ownedProcess = process;
            process.BeginOutputReadLine(); process.BeginErrorReadLine();
            for (var retry = 0; retry < 20; retry++) { await Refresh(false); if (live || ownedProcess == null) break; await Task.Delay(250); }
            MessageText.Text = live ? ConnectionMode.SelectedIndex == 0 ? "Meshway is ready. Choose an approved server to connect this device VPN." : ConnectionMode.SelectedIndex == 2 ? "Meshway is ready. Choose your personal server below to connect the local SOCKS proxy." : "SOCKS is ready. Choose a server, then configure applications with its local SOCKS address." : "Daemon did not become ready within 5 seconds. Inspect daemon.log, then stop and retry.";
        }
        catch (Exception error) { MessageText.Text = error.Message; }
        finally { busy = false; UpdateButtons(); }
    }

    private void Log(string? message)
    {
        if (message == null) return;
        lock (logLock) { try { File.AppendAllText(Path.Combine(Path.GetDirectoryName(configPath)!, "daemon.log"), message + Environment.NewLine); } catch (IOException) { } }
    }

    private async void StopClicked(object sender, RoutedEventArgs e)
    {
        var process = ownedProcess;
        if (process == null || busy) return;
        busy = true; UpdateButtons();
        try
        {
            try { await Request("v1/shutdown", "{}"); } catch (HttpRequestException) { }
            var exit = process.WaitForExitAsync();
            if (await Task.WhenAny(exit, Task.Delay(3000)) != exit && !process.HasExited) process.Kill(true);
        }
        catch (Exception error) { MessageText.Text = error.Message; }
        finally { busy = false; UpdateButtons(); }
    }
    private async void RefreshClicked(object sender, RoutedEventArgs e) => await Refresh(true);
    private static string Bytes(JsonNode? value)
    {
        var bytes = value?.GetValue<ulong>() ?? 0;
        return bytes >= 1024 * 1024 ? $"{bytes / (1024d * 1024):0.0} MiB" : bytes >= 1024 ? $"{bytes / 1024d:0.0} KiB" : $"{bytes} B";
    }
    private void ConnectionModeChanged(object sender, SelectionChangedEventArgs e)
    {
        if (ModeDescription == null || TunnelPanel == null || config == null) return;
        if (deviceVPN.Running && ConnectionMode.SelectedIndex != previousMode)
        {
            ConnectionMode.SelectedIndex = previousMode;
            MessageText.Text = "Disconnect the active device VPN before changing connection mode.";
            return;
        }
        previousMode = ConnectionMode.SelectedIndex;
        ModeDescription.Text = previousMode switch {
            0 => "VPN routes IPv4, IPv6 and DNS through an approved server. Choose a server below; Windows asks for administrator consent.",
            1 => "SOCKS connects only applications configured to use the local proxy. Your device's normal routes and DNS remain in use.",
            _ => "Connect the local SOCKS proxy to your own same-account Meshway server. SSH wraps the Meshway transport when configured; it does not itself enable device-wide VPN capture."
        };
        TunnelPanel.Visibility = previousMode == 2 ? Visibility.Visible : Visibility.Collapsed;
        if (config != null) { File.WriteAllText(modePath, previousMode.ToString()); RenderTunnels(); _ = Refresh(false); }
    }
    private void RenderTunnels()
    {
        if (config == null || TunnelList == null) return;
        TunnelList.Children.Clear();
        var forwards = config.Data["forwards"]?.AsArray();
        if (forwards == null || forwards.Count == 0) { TunnelList.Children.Add(new TextBlock { Text = "No saved tunnels yet.", Margin = new Thickness(0, 0, 0, 12) }); return; }
        foreach (var forward in forwards)
        {
            if (forward == null) continue;
            var name = forward["name"]?.GetValue<string>() ?? "Tunnel";
            var row = new DockPanel { Margin = new Thickness(0, 0, 0, 10) };
            var remove = new Button { Content = "Remove", IsEnabled = !busy && !live && ownedProcess == null && !deviceVPN.Running, Padding = new Thickness(10, 4, 10, 4) };
            DockPanel.SetDock(remove, Dock.Right);
            remove.Click += (_, _) => { try { if (busy || live || ownedProcess != null || deviceVPN.Running) throw new InvalidOperationException("Stop Meshway before changing tunnels."); var updated = (JsonArray)forwards.DeepClone(); var index = forwards.IndexOf(forward); updated.RemoveAt(index); config.Data["forwards"] = updated; config.Save(configPath); RenderTunnels(); } catch (Exception error) { MessageText.Text = error.Message; } };
            row.Children.Add(remove);
            row.Children.Add(new TextBlock { Text = $"{name} · {forward["listen"]} → {forward["service"] ?? forward["target"]}", TextWrapping = TextWrapping.Wrap });
            TunnelList.Children.Add(row);
        }
    }
    private void AddTunnelClicked(object sender, RoutedEventArgs e)
    {
        try
        {
            if (busy || live || ownedProcess != null || deviceVPN.Running) throw new InvalidOperationException("Stop Meshway before changing tunnels.");
            var name = TunnelName.Text.Trim(); var listen = TunnelListen.Text.Trim(); var peer = TunnelPeer.Text.Trim(); var service = TunnelService.Text.Trim();
            if (new[] { name, peer, service }.Any(x => x.Length == 0 || x.Any(char.IsWhiteSpace))) throw new InvalidDataException("Enter a tunnel name, server peer ID and service name without spaces.");
            if (!Uri.TryCreate("http://" + listen, UriKind.Absolute, out var uri) || !uri.IsLoopback || !listen.Contains(':') || uri.Port is < 1 or > 65535 || uri.AbsolutePath != "/" || uri.UserInfo.Length != 0 || uri.Query.Length != 0 || uri.Fragment.Length != 0) throw new InvalidDataException("Use a local listener such as 127.0.0.1:8081.");
            var forwards = config.Data["forwards"]?.AsArray() ?? new JsonArray();
            if (forwards.Any(f => f?["name"]?.GetValue<string>() == name || f?["listen"]?.GetValue<string>() == listen)) throw new InvalidDataException("Use a unique tunnel name and local listener.");
            forwards.Add(new JsonObject { ["name"] = name, ["listen"] = listen, ["peer"] = peer, ["service"] = service });
            config.Data["forwards"] = forwards;
            Save(); RenderTunnels(); MessageText.Text = "Tunnel saved. Start Meshway to open its local listener.";
        }
        catch (Exception error) { MessageText.Text = error.Message; }
    }
    private void SaveSSHClicked(object sender, RoutedEventArgs e)
    {
        try
        {
            if (busy || live || ownedProcess != null || deviceVPN.Running) throw new InvalidOperationException("Stop Meshway before changing its personal transport.");
            if (UseSSH.IsChecked != true) config.Data.Remove("personal_transport");
            else
            {
                var alias = SSHAlias.Text.Trim(); var peer = SSHPeer.Text.Trim();
                if (alias.Length is < 1 or > 128 || !char.IsLetterOrDigit(alias[0]) || alias.Any(c => !(char.IsLetterOrDigit(c) || c is '.' or '_' or '-'))) throw new InvalidDataException("Use an existing SSH host alias with letters, numbers, dots, underscores or hyphens.");
                if (peer.Length == 0 || peer.Any(char.IsWhiteSpace)) throw new InvalidDataException("Enter your server's pinned Meshway peer ID.");
                if (!int.TryParse(SSHLocalPort.Text, out var local) || local is < 1024 or > 65535 || !int.TryParse(SSHRemotePort.Text, out var remote) || remote is < 1 or > 65535) throw new InvalidDataException("Use a local port from 1024–65535 and server port from 1–65535.");
                config.Data["personal_transport"] = new JsonObject { ["kind"] = "ssh", ["ssh_alias"] = alias, ["local_port"] = local, ["remote_port"] = remote, ["peer_id"] = peer };
                PersonalDeviceVPN.IsChecked = false;
            }
            Save(); MessageText.Text = "Personal transport saved. Start Meshway to establish the authenticated connection.";
        }
        catch (Exception error) { MessageText.Text = error.Message; }
    }
    private async void DisconnectClicked(object sender, RoutedEventArgs e)
    {
        try { if (deviceVPN.Running) { await deviceVPN.Stop(); MessageText.Text = "Device VPN disconnected; network settings restored."; await Refresh(false); return; } await Request("v1/disconnect", "{}"); MessageText.Text = "Disconnected from the selected exit."; await Refresh(true); }
        catch (Exception error) { MessageText.Text = error.Message; }
    }
    private async Task RefreshAccount()
    {
        var data = await Task.WhenAll(Request("v1/account/status"), Request("v1/account/devices"), Request("v1/account/history"));
        var labels = new[] { "Account", "Devices", "Local history" };
        AccountText.Text = string.Join("\n\n", data.Select((value, index) => labels[index] + "\n" + value?.ToJsonString(new JsonSerializerOptions { WriteIndented = true })));
    }
    private async void AccountClicked(object sender, RoutedEventArgs e)
    {
        try { await RefreshAccount(); }
        catch (Exception error) { MessageText.Text = "Account unavailable: " + error.Message; }
    }
    private async void AccountSyncClicked(object sender, RoutedEventArgs e)
    {
        try
        {
            var peer = AccountSyncPeer.Text.Trim();
            if (peer.Length == 0) throw new InvalidDataException("Enter the account device peer ID first.");
            await Request("v1/account/sync", JsonSerializer.Serialize(new { peer_id = peer }));
            await RefreshAccount();
            MessageText.Text = "Account records synchronized with the chosen peer.";
        }
        catch (Exception error) { MessageText.Text = error.Message; }
    }
    private async void ConnectClicked(object sender, RoutedEventArgs e)
    {
        try { await Request("v1/connect", JsonSerializer.Serialize(new { address = ConnectAddress.Text.Trim() })); MessageText.Text = "Peer connected."; await Refresh(true); }
        catch (Exception error) { MessageText.Text = error.Message; }
    }
    private void SaveClicked(object sender, RoutedEventArgs e) { try { Save(); MessageText.Text = "Settings saved. They apply on next start."; } catch (Exception error) { MessageText.Text = error.Message; } }
    private async Task RunAccountFileCommand(string command, string file, bool replaceAccount = false)
    {
        if (live || ownedProcess != null || busy || deviceVPN.Running) throw new InvalidDataException("Stop Meshway before exporting or importing your recovery key.");
        Save();
        var executable = ExecutablePath.Text.Trim();
        if (!File.Exists(executable)) throw new FileNotFoundException("Choose the daemon executable first.");
        busy = true; UpdateButtons();
        try
        {
            async Task Run(string action, bool withFile)
            {
                var info = new ProcessStartInfo(executable) { UseShellExecute = false, CreateNoWindow = true, RedirectStandardError = true, RedirectStandardOutput = true };
                info.Environment["MESHWAY_STATE_KEY"] = Convert.ToBase64String(StateKey.LoadOrCreate(configPath));
                info.ArgumentList.Add("account"); info.ArgumentList.Add(action); info.ArgumentList.Add("--config"); info.ArgumentList.Add(configPath);
                if (withFile) { info.ArgumentList.Add((action == "export" || action == "rotate") ? "--output" : "--input"); info.ArgumentList.Add(file); }
                if (withFile && replaceAccount) info.ArgumentList.Add("--force");
                using var child = Process.Start(info) ?? throw new IOException("Could not start account operation.");
                var stdout = child.StandardOutput.ReadToEndAsync(); var stderr = child.StandardError.ReadToEndAsync();
                await child.WaitForExitAsync();
                var output = await stdout + await stderr;
                if (child.ExitCode != 0) throw new InvalidDataException(output[..Math.Min(output.Length, 2048)]);
            }
            if (command == "export") await Run("create", false);
            await Run(command, true);
        }
        finally { busy = false; UpdateButtons(); }
    }
    private async void ExportRecoveryClicked(object sender, RoutedEventArgs e)
    {
        var dialog = new SaveFileDialog { Title = "Save your private Meshway recovery key", FileName = "meshway-recovery.json", Filter = "Recovery file|*.json", OverwritePrompt = true };
        if (dialog.ShowDialog() != true) return;
        try { await RunAccountFileCommand("export", dialog.FileName); MessageText.Text = "Recovery key saved. Keep this file private."; }
        catch (Exception error) { MessageText.Text = error.Message; }
    }
    private async void RotateRecoveryClicked(object sender, RoutedEventArgs e)
    {
        if (MessageBox.Show("Stop Meshway and its VPN first. Replacing the recovery key creates a new account ID and clears local device/history records. Import the new backup on every trusted device and personal server before reconnecting. The old key still controls the old account.", "Replace recovery key?", MessageBoxButton.OKCancel, MessageBoxImage.Warning) != MessageBoxResult.OK) return;
        var dialog = new SaveFileDialog { Title = "Save your new private recovery key", FileName = "meshway-new-recovery.json", Filter = "Recovery file|*.json", OverwritePrompt = true };
        if (dialog.ShowDialog() != true) return;
        try { await RunAccountFileCommand("rotate", dialog.FileName); AccountText.Text = "New account created. Import this backup on trusted devices and private servers."; MessageText.Text = "Recovery key replaced. Your account ID changed."; }
        catch (Exception error) { MessageText.Text = error.Message; }
    }
    private async void RevokeDeviceClicked(object sender, RoutedEventArgs e)
    {
        var peer = RevokePeer.Text.Trim();
        if (peer.Length == 0) { MessageText.Text = "Enter the device peer ID first."; return; }
        if (MessageBox.Show("Revoke this device? The signed revocation reaches other devices when they synchronize. Rotate the account if the recovery key is compromised.", "Revoke account device?", MessageBoxButton.OKCancel, MessageBoxImage.Warning) != MessageBoxResult.OK) return;
        try { await Request("v1/account/revoke", JsonSerializer.Serialize(new { peer_id = peer })); await RefreshAccount(); MessageText.Text = "Device revoked."; }
        catch (Exception error) { MessageText.Text = error.Message; }
    }
    private async void ClearHistoryClicked(object sender, RoutedEventArgs e)
    {
        var synchronize = SyncHistoryDeletion.IsChecked == true;
        if (MessageBox.Show(synchronize ? "Delete connection history and create a signed deletion for other devices to apply on their next sync?" : "Delete history on only this device? Other devices retain their records.", "Delete history?", MessageBoxButton.OKCancel, MessageBoxImage.Warning) != MessageBoxResult.OK) return;
        try { await Request("v1/account/history", JsonSerializer.Serialize(new { synchronize }), HttpMethod.Delete); await RefreshAccount(); MessageText.Text = "History deleted."; }
        catch (Exception error) { MessageText.Text = error.Message; }
    }
    private async void ImportRecoveryClicked(object sender, RoutedEventArgs e)
    {
        var dialog = new OpenFileDialog { Title = "Join your account on this device", Filter = "Recovery file|*.json|All files|*.*", CheckFileExists = true };
        if (dialog.ShowDialog() != true) return;
        var replace = File.Exists(Path.Combine(config.String("state_dir"), "account.seed"));
        if (replace && MessageBox.Show("Importing replaces the current account and clears local device/history records. Your peer identity stays the same. Save the existing recovery key first if you need it.", "Replace local account?", MessageBoxButton.OKCancel, MessageBoxImage.Warning) != MessageBoxResult.OK) return;
        try { await RunAccountFileCommand("import", dialog.FileName, replace); MessageText.Text = "Account imported. Start Meshway to enroll this device."; }
        catch (Exception error) { MessageText.Text = error.Message; }
    }
    private void PersonalServerChanged(object sender, RoutedEventArgs e) { if (ExitRole != null && PersonalServer.IsChecked == true) ExitRole.IsChecked = true; }
    private void ChooseClicked(object sender, RoutedEventArgs e) { var dialog = new OpenFileDialog { Filter = "Executable files|*.exe", CheckFileExists = true }; if (dialog.ShowDialog() == true) ExecutablePath.Text = dialog.FileName; }
    private void ExitChanged(object sender, RoutedEventArgs e) { if (PrivateTargets == null) return; PrivateTargets.IsEnabled = ExitRole.IsChecked == true; if (!PrivateTargets.IsEnabled) PrivateTargets.IsChecked = false; }
    private void OpenFolderClicked(object sender, RoutedEventArgs e) => Process.Start(new ProcessStartInfo("explorer.exe", Path.GetDirectoryName(configPath)!) { UseShellExecute = true });
    private void OnClosing(object? sender, CancelEventArgs e)
    {
        timer.Stop();
        deviceVPN.SignalStop();
        if (ownedProcess is { HasExited: false }) { try { ownedProcess.Kill(true); } catch (InvalidOperationException) { } }
        client.Dispose();
    }
}
