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

namespace Meshway;

public partial class MainWindow : Window
{
    private readonly string configPath = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "Meshway", "config.json");
    private readonly HttpClient client = new() { Timeout = TimeSpan.FromSeconds(5) };
    private NativeConfiguration config = null!;
    private Process? ownedProcess;
    private bool busy;
    private bool refreshing;
    private bool live;
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

    private async Task<JsonNode?> Request(string route, string? json = null)
    {
        using var request = new HttpRequestMessage(json == null ? HttpMethod.Get : HttpMethod.Post, new Uri(config.ApiUri(), route));
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
            if (status?["upstream_peer"]?.GetValue<string>() is { Length: > 0 }) StateText.Text = status?["upstream_connected"]?.GetValue<bool>() == true ? "Selected peer connection is live" : "Selected peer offline · reconnects on traffic";
            StatusText.Text = $"Peer: {status?["peer_id"]}\nSOCKS5: {status?["socks_listen"]}\nPeers: {status?["peers"]?.AsArray().Count ?? 0}\nSent: {status?["bytes_sent"]} bytes · Received: {status?["bytes_received"]} bytes\n" + string.Join("\n", status?["addresses"]?.AsArray().Select(x => x!.GetValue<string>()) ?? []);
            try { await RefreshServers(status?["upstream_peer"]?.GetValue<string>() ?? ""); }
            catch (Exception error) { CatalogMessage.Text = "Server list unavailable: " + error.Message; ServerList.Children.Clear(); }
        }
        catch (Exception error) { live = false; StateText.Text = "Daemon offline"; StatusText.Text = ""; if (reportError) MessageText.Text = error.Message; }
        finally { refreshing = false; UpdateButtons(); }
    }

    private void UpdateButtons()
    {
        StartButton.IsEnabled = !busy && !live && ownedProcess == null && config != null;
        StopButton.IsEnabled = !busy && ownedProcess != null;
        SettingsGroup.IsEnabled = !busy && !live && ownedProcess == null && config != null;
    }

    private async Task RefreshServers(string selectedPeer)
    {
        var catalog = await Request("v1/servers");
        var servers = catalog?["servers"]?.AsArray();
        ServerList.Children.Clear();
        CatalogMessage.Text = servers is { Count: > 0 } ? "Choose a server to connect." : "No servers yet. Add an invitation address from a friend or your server.";
        if (servers == null) return;
        foreach (var server in servers)
        {
            var id = server?["peer_id"]?.GetValue<string>() ?? "";
            var label = server?["label"]?.GetValue<string>() ?? "Peer server";
            var country = server?["country"]?.GetValue<string>() ?? "";
            var address = server?["address"]?.GetValue<string>() ?? "";
            var row = new DockPanel { Margin = new Thickness(0, 0, 0, 12) };
            var connect = new Button { Content = id == selectedPeer ? "Selected" : "Connect", Padding = new Thickness(14, 7, 14, 7), IsEnabled = !busy && id != selectedPeer && server?["available"]?.GetValue<bool>() == true && server?["can_connect"]?.GetValue<bool>() == true };
            DockPanel.SetDock(connect, Dock.Right);
            connect.Click += async (_, _) =>
            {
                try { await Request("v1/select", JsonSerializer.Serialize(new { peer_id = id, address })); MessageText.Text = "Selected " + label + ". Applications can use the Meshway SOCKS connection."; await Refresh(true); }
                catch (Exception error) { MessageText.Text = error.Message; }
            };
            row.Children.Add(connect);
            row.Children.Add(new TextBlock { Text = label + (country.Length == 0 ? "" : " · " + country), FontSize = 16, VerticalAlignment = VerticalAlignment.Center });
            ServerList.Children.Add(row);
        }
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
            info.ArgumentList.Add("daemon"); info.ArgumentList.Add("--config"); info.ArgumentList.Add(configPath);
            var process = new Process { StartInfo = info, EnableRaisingEvents = true };
            process.OutputDataReceived += (_, args) => Log(args.Data);
            process.ErrorDataReceived += (_, args) => Log(args.Data);
            process.Exited += (_, _) => Dispatcher.InvokeAsync(() => { if (ownedProcess != process) return; ownedProcess = null; live = false; StateText.Text = "Daemon offline"; MessageText.Text = $"Daemon stopped (exit {process.ExitCode}). Inspect daemon.log if unexpected."; process.Dispose(); UpdateButtons(); });
            if (!process.Start()) throw new IOException("Could not start daemon.");
            ownedProcess = process;
            process.BeginOutputReadLine(); process.BeginErrorReadLine();
            for (var retry = 0; retry < 20; retry++) { await Refresh(false); if (live || ownedProcess == null) break; await Task.Delay(250); }
            MessageText.Text = live ? "Daemon running. Configure applications to use its SOCKS address." : "Daemon did not become ready within 5 seconds. Inspect daemon.log, then stop and retry.";
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
    private async void DisconnectClicked(object sender, RoutedEventArgs e)
    {
        try { await Request("v1/disconnect", "{}"); MessageText.Text = "Disconnected from the selected exit."; await Refresh(true); }
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
        if (live || ownedProcess != null || busy) throw new InvalidDataException("Stop Meshway before exporting or importing your recovery key.");
        Save();
        var executable = ExecutablePath.Text.Trim();
        if (!File.Exists(executable)) throw new FileNotFoundException("Choose the daemon executable first.");
        busy = true; UpdateButtons();
        try
        {
            async Task Run(string action, bool withFile)
            {
                var info = new ProcessStartInfo(executable) { UseShellExecute = false, CreateNoWindow = true, RedirectStandardError = true, RedirectStandardOutput = true };
                info.ArgumentList.Add("account"); info.ArgumentList.Add(action); info.ArgumentList.Add("--config"); info.ArgumentList.Add(configPath);
                if (withFile) { info.ArgumentList.Add(action == "export" ? "--output" : "--input"); info.ArgumentList.Add(file); }
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
        if (ownedProcess is { HasExited: false }) { try { ownedProcess.Kill(true); } catch (InvalidOperationException) { } }
        client.Dispose();
    }
}
