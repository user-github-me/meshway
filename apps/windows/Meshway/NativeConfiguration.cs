using System.IO;
using System.Security.Cryptography;
using System.Text.Json;
using System.Text.Json.Nodes;

namespace Meshway;

public sealed class NativeConfiguration
{
    public JsonObject Data { get; }
    public NativeConfiguration(JsonObject data) { Data = data; }
    public string String(string name) => Data[name]?.GetValue<string>() ?? "";
    public bool Boolean(string name) => Data[name]?.GetValue<bool>() ?? false;
    public string Lines(string name) => string.Join(Environment.NewLine, Data[name]?.AsArray().Select(x => x!.GetValue<string>()) ?? []);
    public void SetLines(string name, string value) => Data[name] = new JsonArray(value.Split(['\r', '\n'], StringSplitOptions.RemoveEmptyEntries).Select(x => (JsonNode?)JsonValue.Create(x.Trim())).ToArray());

    public Uri ApiUri()
    {
        string listen = String("api_listen");
        if (!Uri.TryCreate("http://" + listen, UriKind.Absolute, out var uri) ||
            !new[] { "127.0.0.1", "localhost", "[::1]", "::1" }.Contains(uri.Host) ||
            uri.Port is < 1 or > 65535 || !listen.Contains(':') ||
            uri.UserInfo.Length != 0 || uri.AbsolutePath != "/" || uri.Query.Length != 0 || uri.Fragment.Length != 0)
            throw new InvalidDataException("API listener must use 127.0.0.1, localhost, or [::1] with an explicit valid port.");
        return uri;
    }

    public void Validate()
    {
        _ = ApiUri();
        if (String("api_token").Length < 32 || String("api_token").Any(char.IsWhiteSpace)) throw new InvalidDataException("API token must contain at least 32 characters and no whitespace.");
        if (String("state_dir").Length == 0) throw new InvalidDataException("State directory is required.");
        var socks = String("socks_listen");
        if (socks.Length != 0)
        {
            if (!Uri.TryCreate("http://" + socks, UriKind.Absolute, out var url) || !url.IsLoopback || url.Port is < 1 or > 65535 || !socks.Contains(':') || url.AbsolutePath != "/" || url.UserInfo.Length != 0 || url.Query.Length != 0 || url.Fragment.Length != 0)
                throw new InvalidDataException("SOCKS listener must use a loopback host with a valid port, or be empty.");
        }
        if (Boolean("allow_private_targets") && !Boolean("exit")) throw new InvalidDataException("Private targets require the exit role.");
    }

    public void Save(string path)
    {
        Validate();
        Directory.CreateDirectory(Path.GetDirectoryName(path)!);
        var temporary = path + ".tmp";
        File.WriteAllText(temporary, Data.ToJsonString(new JsonSerializerOptions { WriteIndented = true }));
        File.Move(temporary, path, true);
    }

    public static NativeConfiguration LoadOrCreate(string path)
    {
        if (File.Exists(path))
        {
            var loaded = new NativeConfiguration(JsonNode.Parse(File.ReadAllText(path))!.AsObject());
            loaded.Validate();
            return loaded;
        }
        var config = new NativeConfiguration(new JsonObject
        {
            ["state_dir"] = Path.Combine(Path.GetDirectoryName(path)!, "node"),
            ["listen_addresses"] = new JsonArray("/ip4/0.0.0.0/tcp/4001", "/ip4/0.0.0.0/udp/4001/quic-v1"),
            ["bootstrap_peers"] = new JsonArray(), ["allowed_peers"] = new JsonArray(), ["allowed_origins"] = new JsonArray(),
            ["upstream_peer"] = "", ["socks_listen"] = "127.0.0.1:1080", ["api_listen"] = "127.0.0.1:7777",
            ["api_token"] = Convert.ToHexString(RandomNumberGenerator.GetBytes(32)).ToLowerInvariant(),
            ["relay"] = false, ["exit"] = false, ["allow_private_targets"] = false,
            ["forwards"] = new JsonArray(), ["services"] = new JsonArray()
        });
        config.Save(path);
        return config;
    }
}
