using System.Net;
using System.Net.Sockets;
using System.Numerics;

namespace Meshway.VPN;

public sealed record VPNPlan(string PeerID, string Invitation, string[] Endpoints, string[] BlockedCIDRs)
{
    public const string InterfaceName = "Meshway";
    public const string IPv4Address = "10.77.0.2";
    public const string IPv6Address = "fd77::2";
    public const string IPv4DNS = "10.77.0.1";
    public const string IPv6DNS = "fd77::1";

    public static async Task<VPNPlan> Create(string peerID, string invitation, IEnumerable<string> bootstraps)
    {
        var selected = ParseInvitation(invitation, peerID);
        var endpoints = new HashSet<IPAddress>();
        foreach (var address in new[] { invitation }.Concat(bootstraps))
        {
            var parts = address.Split('/', StringSplitOptions.RemoveEmptyEntries);
            for (var i = 0; i + 1 < parts.Length; i++)
            {
                if (parts[i] is "ip4" or "ip6") endpoints.Add(ParseEndpoint(parts[i + 1]));
                else if (parts[i] is "dns" or "dns4" or "dns6")
                {
                    var host = parts[i + 1];
                    if (Uri.CheckHostName(host) != UriHostNameType.Dns) throw new InvalidDataException("Invalid server hostname.");
                    foreach (var ip in await Dns.GetHostAddressesAsync(host))
                        if (parts[i] != "dns4" || ip.AddressFamily == AddressFamily.InterNetwork)
                            if (parts[i] != "dns6" || ip.AddressFamily == AddressFamily.InterNetworkV6) endpoints.Add(ParseEndpoint(ip.ToString()));
                }
            }
        }
        if (endpoints.Count is 0 or > 128) throw new InvalidDataException("A device VPN needs 1 to 128 known transport endpoints. Add a numeric or DNS server invitation.");
        var ips = endpoints.OrderBy(x => x.AddressFamily).ThenBy(x => x.ToString()).ToArray();
        // Firewall block rules take precedence over allow rules. Exclude exact
        // transport endpoints from the blocked address space, rather than
        // attempting an ineffective broad-block / application-allow pairing.
        return new VPNPlan(peerID, invitation, ips.Select(x => x.ToString()).ToArray(), ComplementCIDRs(ips).ToArray());
    }

    public static string[] ParseInvitation(string address, string expectedPeer)
    {
        var parts = address.Split('/', StringSplitOptions.RemoveEmptyEntries);
        if (string.IsNullOrWhiteSpace(expectedPeer) || parts.Length < 4 || parts[^1] != expectedPeer || !parts.Contains("p2p"))
            throw new InvalidDataException("Server invitation does not identify the selected peer.");
        return parts;
    }

    public static IPAddress ParseEndpoint(string value)
    {
        if (!IPAddress.TryParse(value, out var address) || address.Equals(IPAddress.Any) || address.Equals(IPAddress.IPv6Any) || address.IsIPv6Multicast || address.ScopeIdOrZero() != 0)
            throw new InvalidDataException("Invalid transport endpoint IP address.");
        if (address.AddressFamily == AddressFamily.InterNetwork && address.GetAddressBytes()[0] >= 224)
            throw new InvalidDataException("Multicast transport endpoints cannot be used.");
        return address;
    }

    public static IEnumerable<string> ComplementCIDRs(IEnumerable<IPAddress> exceptions)
    {
        var values = exceptions.Distinct().ToArray();
        foreach (var family in new[] { AddressFamily.InterNetwork, AddressFamily.InterNetworkV6 })
        {
            var bits = family == AddressFamily.InterNetwork ? 32 : 128;
            var allowed = values.Where(x => x.AddressFamily == family).Select(Number).Order().ToArray();
            foreach (var cidr in Walk(BigInteger.Zero, 0, bits, allowed)) yield return cidr;
        }
    }

    private static IEnumerable<string> Walk(BigInteger start, int prefix, int bits, BigInteger[] allowed)
    {
        if (allowed.Length == 0)
        {
            var bytes = start.ToByteArray(isUnsigned: true, isBigEndian: true);
            var fixedBytes = new byte[bits / 8]; bytes.CopyTo(fixedBytes, fixedBytes.Length - bytes.Length);
            yield return new IPAddress(fixedBytes) + "/" + prefix;
            yield break;
        }
        if (prefix == bits) yield break;
        var middle = start + (BigInteger.One << (bits - prefix - 1));
        foreach (var cidr in Walk(start, prefix + 1, bits, allowed.Where(x => x < middle).ToArray())) yield return cidr;
        foreach (var cidr in Walk(middle, prefix + 1, bits, allowed.Where(x => x >= middle).ToArray())) yield return cidr;
    }

    private static BigInteger Number(IPAddress address) => new(address.GetAddressBytes(), isUnsigned: true, isBigEndian: true);
    public static bool Contains(string cidr, IPAddress address)
    {
        var fields = cidr.Split('/'); var network = IPAddress.Parse(fields[0]);
        if (network.AddressFamily != address.AddressFamily) return false;
        var bits = address.AddressFamily == AddressFamily.InterNetwork ? 32 : 128;
        var prefix = int.Parse(fields[1]);
        return Number(network) >> (bits - prefix) == Number(address) >> (bits - prefix);
    }
}

internal static class IPAddressExtensions
{
    public static long ScopeIdOrZero(this IPAddress ip) => ip.AddressFamily == AddressFamily.InterNetworkV6 ? ip.ScopeId : 0;
}
