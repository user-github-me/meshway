using System.Diagnostics;
using System.Net.Http.Headers;
using System.Security.Principal;
using System.Security.Cryptography;
using System.Runtime.InteropServices;
using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;
using Meshway.VPN;
using Meshway.Security;

static async Task<JsonNode?> Request(HttpClient client, Uri baseUri, string token, string path, object? payload = null)
{
    using var message = new HttpRequestMessage(payload == null ? HttpMethod.Get : HttpMethod.Post, new Uri(baseUri, path));
    message.Headers.Authorization = new AuthenticationHeaderValue("Bearer", token);
    if (payload != null) message.Content = new StringContent(JsonSerializer.Serialize(payload), Encoding.UTF8, "application/json");
    using var response = await client.SendAsync(message);
    var text = await response.Content.ReadAsStringAsync();
    if (!response.IsSuccessStatusCode) throw new IOException("The VPN core rejected the operation (HTTP " + (int)response.StatusCode + ").");
    return JsonNode.Parse(text);
}

if (!OperatingSystem.IsWindows()) throw new PlatformNotSupportedException("Windows device VPN requires Wintun and Windows administrator consent.");
if (!new WindowsPrincipal(WindowsIdentity.GetCurrent()).IsInRole(WindowsBuiltInRole.Administrator)) throw new UnauthorizedAccessException("Administrator consent is required for device VPN routes and firewall protection.");
if (args.Length != 1) throw new ArgumentException("Expected one private VPN request path.");
var requestPath = Path.GetFullPath(args[0]);
var request = JsonNode.Parse(await File.ReadAllTextAsync(requestPath))!.AsObject();
var configPath = Path.GetFullPath(request["config"]!.GetValue<string>());
var peer = request["peer_id"]!.GetValue<string>();
var invitation = request["address"]!.GetValue<string>();
var parentID = request["parent_pid"]!.GetValue<int>();
var parentStart = request["parent_start"]!.GetValue<long>();
var statusPath = requestPath + ".status";
var stopPath = requestPath + ".stop";
var journal = Path.Combine(Path.GetDirectoryName(requestPath)!, "vpn-network-journal.json");
var config = JsonNode.Parse(await File.ReadAllTextAsync(configPath))!.AsObject();
var token = config["api_token"]!.GetValue<string>();
var uri = new Uri("http://" + config["api_listen"]!.GetValue<string>() + "/");
if (!IPAddressLoopback(uri) || token.Length < 32) throw new InvalidDataException("VPN helper accepts authenticated loopback configurations only.");
var executable = Path.Combine(AppContext.BaseDirectory, "meshway-daemon.exe");
var wintun = Path.Combine(AppContext.BaseDirectory, "wintun.dll");
var expectedWintun = RuntimeInformation.ProcessArchitecture == Architecture.Arm64
    ? "f7ba89005544be9d85231a9e0d5f23b2d15b3311667e2dad0debd344918a3f80"
    : "e5da8447dc2c320edc0fc52fa01885c103de8c118481f683643cacc3220dafce";
if (!File.Exists(wintun) || Convert.ToHexString(SHA256.HashData(await File.ReadAllBytesAsync(wintun))).ToLowerInvariant() != expectedWintun)
    throw new InvalidDataException("The Windows VPN driver library does not match the pinned signed upstream release.");
var bootstraps = config["bootstrap_peers"]?.AsArray().Select(x => x!.GetValue<string>()) ?? [];
var plan = await VPNPlan.Create(peer, invitation, bootstraps);
using var client = new HttpClient { Timeout = TimeSpan.FromSeconds(15) };
Process? daemon = null;
var transaction = new NetworkTransaction(journal);
try
{
    if (File.Exists(journal)) await transaction.Restore(journal);
    var start = new ProcessStartInfo(executable) { UseShellExecute = false, CreateNoWindow = true, WorkingDirectory = AppContext.BaseDirectory };
    start.Environment["MESHWAY_STATE_KEY"] = Convert.ToBase64String(StateKey.LoadOrCreate(configPath));
    start.ArgumentList.Add("daemon"); start.ArgumentList.Add("--config"); start.ArgumentList.Add(configPath);
    daemon = Process.Start(start) ?? throw new IOException("Could not start elevated VPN core.");
    JsonNode? live = null;
    for (var i = 0; i < 60; i++)
    {
        try { live = await Request(client, uri, token, "v1/status"); break; }
        catch (HttpRequestException) { }
        if (daemon.HasExited) throw new IOException("VPN core exited before becoming ready.");
        await Task.Delay(250);
    }
    if (live == null) throw new IOException("VPN core did not become ready.");
    await Request(client, uri, token, "v1/select", new { peer_id = peer, address = invitation });
    await Request(client, uri, token, "v1/vpn/start", new { peer_id = peer, device = VPNPlan.InterfaceName, mode = "exit" });
    await transaction.Apply(plan);
    await File.WriteAllTextAsync(statusPath, JsonSerializer.Serialize(new { active = true, message = "Device VPN connected" }));
    while (!File.Exists(stopPath))
    {
        if (daemon.HasExited) {
            await File.WriteAllTextAsync(statusPath, JsonSerializer.Serialize(new { active = false, message = "VPN core stopped. Internet remains blocked until you disconnect." }));
        }
        try { using var parent = Process.GetProcessById(parentID); if (parent.HasExited || parent.StartTime.ToUniversalTime().Ticks != parentStart) break; }
        catch (ArgumentException) { break; }
        await Task.Delay(500);
    }
}
catch (Exception error)
{
    await File.WriteAllTextAsync(statusPath, JsonSerializer.Serialize(new { active = false, message = error.Message }));
    Environment.ExitCode = 1;
}
finally
{
    // Routes/rules must be removed while Wintun still exists, then the core
    // closes its owned adapter. A failed cleanup leaves the ownership journal
    // and fail-closed rules for the next helper invocation to recover.
    try { await transaction.DisposeAsync(); }
    catch { await File.WriteAllTextAsync(statusPath, "{\"active\":false,\"message\":\"Network cleanup needs retry. Start and disconnect Meshway VPN again.\"}"); }
    if (daemon != null)
    {
        try { await Request(client, uri, token, "v1/vpn/stop", new { }); } catch { }
        try { await Request(client, uri, token, "v1/shutdown", new { }); } catch { }
        var exiting = daemon.WaitForExitAsync();
        if (await Task.WhenAny(exiting, Task.Delay(4000)) != exiting && !daemon.HasExited) daemon.Kill(true);
        daemon.Dispose();
    }
    File.Delete(stopPath);
}

static bool IPAddressLoopback(Uri uri) => System.Net.IPAddress.TryParse(uri.Host.Trim('[', ']'), out var ip) && System.Net.IPAddress.IsLoopback(ip) && uri.Port is > 0 and < 65536 && uri.UserInfo.Length == 0 && uri.AbsolutePath == "/";
