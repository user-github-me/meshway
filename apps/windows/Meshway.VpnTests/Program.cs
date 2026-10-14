using System.Net;
using Meshway.VPN;

static void Check(bool assertion, string reason) { if (!assertion) throw new Exception(reason); }
var allowed = new[] { IPAddress.Parse("203.0.113.5"), IPAddress.Parse("2001:db8::5") };
var complement = VPNPlan.ComplementCIDRs(allowed).ToArray();
Check(complement.Length == 160, "Single host exclusions should partition 32+128 address bits.");
foreach (var address in allowed) Check(!complement.Any(cidr => VPNPlan.Contains(cidr, address)), "Transport endpoints must remain outside block rules.");
foreach (var address in new[] { "1.1.1.1", "203.0.113.4", "203.0.113.6", "255.255.255.255", "::", "2001:db8::4", "2001:db8::6", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff" })
    Check(complement.Count(cidr => VPNPlan.Contains(cidr, IPAddress.Parse(address))) == 1, "Every other address must be blocked exactly once: " + address);
var many = new[] { IPAddress.Parse("10.0.0.1"), IPAddress.Parse("10.0.0.2"), IPAddress.Parse("::1"), IPAddress.Parse("::2") };
var blocks = VPNPlan.ComplementCIDRs(many).ToArray();
var random = new Random(947);
for (var i = 0; i < 5000; i++)
{
    var bytes = new byte[i % 2 == 0 ? 4 : 16]; random.NextBytes(bytes); var address = new IPAddress(bytes);
    Check(blocks.Count(cidr => VPNPlan.Contains(cidr, address)) == (many.Contains(address) ? 0 : 1), "Randomized complement coverage failed.");
}
var plan = await VPNPlan.Create("exit", "/ip4/203.0.113.5/tcp/4001/p2p/relay/p2p-circuit/p2p/exit", new[] { "/ip6/2001:db8::5/tcp/4001/p2p/relay" });
Check(plan.Endpoints.Length == 2, "Relay and bootstrap transports must be known before route changes.");
foreach (var bad in new[] { "/ip4/203.0.113.5/tcp/4001/p2p/wrong", "bad; Start-Process", "/ip4/0.0.0.0/tcp/1/p2p/exit" })
{
    try { await VPNPlan.Create("exit", bad, []); throw new Exception("Invalid invitation accepted."); }
    catch (InvalidDataException) { }
}
Console.WriteLine("Windows VPN route/firewall plan: IPv4+IPv6 coverage, peer bypass, relay parsing, input validation passed (no network mutations).");

var controls = new[] { "203.0.113.5", "255.255.255.255/32", "fe80::/10", "ff02::/16" };
var controlRanges = FirewallAddressRanges.Complement(controls, false).Concat(FirewallAddressRanges.Complement(controls, true)).ToArray();
Check(controlRanges.All(value => value.Contains('/') && !value.Contains('-')), "Windows control blocks must use valid CIDR notation.");
var exactCIDRs = FirewallAddressRanges.Complement(allowed.Select(x => x.ToString()).ToArray(), false).Concat(FirewallAddressRanges.Complement(allowed.Select(x => x.ToString()).ToArray(), true)).ToArray();
Check(exactCIDRs.SequenceEqual(complement), "Control and original endpoint complements must produce identical canonical CIDRs.");
foreach (var address in new[] { "203.0.113.5", "255.255.255.255", "fe80::1", "febf:ffff::1", "ff02::1:2" })
    Check(!controlRanges.Any(range => VPNPlan.Contains(range, IPAddress.Parse(address))), "Required link control must remain outside its DHCP/NDP block ranges.");
foreach (var address in new[] { "1.1.1.1", "203.0.113.4", "203.0.113.6", "255.255.255.254", "fe7f:ffff::1", "fec0::1", "ff01::1", "ff03::1", "2001:db8::1" })
    Check(controlRanges.Count(range => VPNPlan.Contains(range, IPAddress.Parse(address))) == 1, "Control exceptions must not permit adjacent or ordinary remote destinations: " + address);
Console.WriteLine("Windows control-destination CIDRs: DHCP broadcast/exact server and IPv6 link-local/multicast scope boundaries passed.");
