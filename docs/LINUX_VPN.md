# Linux device VPN

Select your server in the local Meshway dashboard, keep the daemon running, and start device VPN with one administrator command:

```sh
sudo meshway vpn start --config /absolute/path/to/config.json
```

Meshway creates a dedicated persistent TUN for the configuration owner's UID, connects it through the authenticated local daemon API, adds IPv4/IPv6 capture routes, preserves exact peer/relay transport routes, configures tunneled DNS, and installs an nftables firewall. The existing daemon can remain an ordinary user; only setup and teardown need administrator privileges. A configuration serving exit or relay traffic is refused for this client-only setup.

Stop restores the original networking:

```sh
sudo meshway vpn stop --config /absolute/path/to/config.json
sudo meshway vpn status --config /absolute/path/to/config.json
```

The browser dashboard cannot grant administrator privileges. Its server selection works without privileges for SOCKS/tunnels; the command above enables device traffic afterward. Keep the configuration path absolute when using sudo so it refers to the correct user's account. Do not enable this on a remote SSH-only machine without console access: its device-wide firewall also blocks direct traffic outside the VPN.

## Requirements and DNS

Linux needs `iproute2`, `nftables`, `/dev/net/tun`, IPv6 support, and administrator access. Default DNS mode requires working `systemd-resolved`/`resolvectl` with `/etc/resolv.conf` pointing into `/run/systemd/resolve/`. Setup adds per-link IPv4/IPv6 DNS, the `~.` routing domain and flushes resolver caches; stop reverts that owned link without editing other interfaces' DNS.

For distributions without systemd-resolved, explicitly choose file mode:

```sh
sudo meshway vpn start --config /absolute/path/config.json --dns-mode file
```

File mode supports a regular `/etc/resolv.conf`, snapshots its exact bytes and permissions, and writes tunneled resolvers while active. It refuses symlinks or managed resolver paths; stop restores the snapshot. NetworkManager or DHCP rewriting that file while the VPN is active is outside this simple file-mode contract. Use resolved mode on managed desktop systems. Only file mode has been exercised in the disposable Alpine integration lab; systemd-resolved is implemented but needs its distribution runtime validation.

## Traffic protection and recovery

The dedicated interface uses MTU 1280, client addresses `10.77.0.2/32` and `fd77::2/128`, and virtual DNS `10.77.0.1`/`fd77::1`. More-specific `/1` capture routes send ordinary IPv4/IPv6 destinations through the encrypted exit without deleting existing default routes. Existing connected LAN or other more-specific routes can win routing selection; the firewall blocks that direct traffic unless it is an explicitly preserved peer transport. Private LAN forwarding therefore may need an explicit route through the VPN and exit permission. Setup refuses an existing interface, an existing Meshway firewall table, or existing `/1` VPN capture routes.

The firewall permits loopback, the VPN interface, and exact configured/selected peer IP plus TCP/UDP transport ports. The physical interfaces used by those peers also permit IPv6 neighbor/router discovery types 133–136 with hop limit 255, DHCPv4 client traffic from UDP port 68 to port 67, and DHCPv6 from port 546 to port 547 within link-local/multicast scope. These limited control packets preserve neighbor discovery and lease renewal; other direct traffic is rejected. Applications can also reach the exact exempt peer IP/ports directly, so this is not process-isolated underlay enforcement. Invitations must contain literal IP addresses. Hostname invitations are refused to avoid resolving transport names through a captured resolver or hidden LAN destination. New relay/IP addresses require stop, update the explicit invitation, and start again.

A peer outage triggers core reconnect while captured traffic stays blocked. If the daemon exits, the persistent TUN, routes and nftables protection remain until an explicit stop. Setup records a root-owned private journal under `/run/meshway-vpn` before mutations. Its parent directories must also be root-owned and protected from unprivileged writes. Routes carry protocol 186 and a random per-journal metric, so recovery preserves foreign routes at the same destination. Failures attempt rollback. An interrupted setup or incomplete cleanup is recovered with `meshway vpn stop`; missing client configuration still permits route/DNS/firewall cleanup. Stop verifies TUN removal even if an unavailable daemon retains an open descriptor. Failed route, DNS or device restoration keeps the journal and firewall instead of claiming success. A failed inspection is treated as uncertainty, and protection remains until cleanup can be confirmed. The firewall is removed last. The journal is runtime state; reboot resets it together with the normal nonpersistent routes and nft rules. This helper does not install a persistent boot-time firewall or guarantee every distribution's DHCP/resolver behavior.

## Verified lab

```sh
MESHWAY_LAB_IMAGE=YOUR_TRUSTED_LOCAL_ALPINE_IMAGE scripts/linux-vpn-setup-lab.sh
```

The script adds distro-signed nftables packages inside an ephemeral Docker image and creates separate gateway/client Linux namespaces. It never changes development-host routes, DNS, active VPN or proxy settings. The actual helper creates the persistent TUN, routes, DNS and firewall; real Linux kernel HTTP crosses an encrypted IPv6 underlay. Flushing the physical neighbor cache while connected proves neighbor rediscovery still works with protection active. Daemon termination leaves protection installed and prevents new direct HTTP. Explicit stop restores byte-identical resolver contents and the original main route table, then removes the owned device and firewall. Tests verify restoration with a removed configuration and a live daemon, refusal to overwrite an existing firewall, preservation of foreign routes, rejection of writable runtime ancestors, retention of protection/journal after failed cleanup inspections, and successful subsequent recovery. Both containers are named and removed by the script's exit and interrupt cleanup. The lab passed on 2026-10-14; all owned containers, network and ephemeral image were cleaned up.

The earlier packet lab separately verifies kernel IPv4/IPv6 traffic, real UDP DNS and certificate-verified public HTTPS. See [VPN.md](VPN.md). Physical Linux desktop distributions, systemd-resolved runtime, DHCP/network changes and sleep/wake require their own checks before broad release claims.
