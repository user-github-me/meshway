# Real-world network acceptance tests

Local integration tests prove framing, encryption, consent, authentication, relayed bytes, packet forwarding, and app API behaviour. They do not establish reliability across every ISP, carrier NAT, firewall, VPS provider, or operating-system VPN API.

## Internet egress without disrupting the host

Build the core and run:

```sh
python3 scripts/internet-smoke.py --binary dist/meshway --url https://example.com/
```

This creates isolated temporary client/exit nodes, explicitly authorizes the client, and fetches a real public HTTPS page through SOCKS5 and encrypted peer transport. It never changes system routes, DNS, active VPN, or system/browser proxy settings. It prints byte counts, not private keys or config tokens. Temporary nodes are stopped and their private state is deleted.

Use `--container-image meshway:local` to run the exit as a trusted local Docker image in a separate Linux network namespace. The test binds its published peer port to host loopback, uses the generated device key, and verifies real HTTPS through Mac→Linux→internet. This has passed locally; it still does not prove remote carrier-NAT or different-country latency.

## Two-machine and different-network test

Use a VPS and a client on another network. Build/copy the correct binaries. Configure a personal server, import the same account recovery backup privately, and start it. Open TCP/UDP4001 at the VPS. Share a reachable invitation multiaddress containing the VPS public IP/hostname and its device peer ID. Add it to the client, refresh the real server list, and select the private server.

Use `curl --socks5-hostname 127.0.0.1:1080 --noproxy '' https://example.com/` to verify traffic. Check the exit public IP using a trusted endpoint of your choosing. Verify a different account is rejected; verify a revoked device is rejected after that server observes the revocation. Disconnect, stop the server, and confirm the proxy fails clearly. Restart and reconnect, confirming no misleading connected state.

For actual browser behavior, run:

```sh
node scripts/browser-exit-check.mjs --expected-exit-ip YOUR_SERVER_IP --proxy socks5://127.0.0.1:1080
```

It opens a
temporary browser profile, requires matching IPv4 and dual-stack public exit checks plus successful
certificate-verified HTTPS, then closes that profile. Omit `--proxy` to test the
browser using current macOS system proxy preferences. The script does not
activate a proxy, start a daemon or modify routes/DNS. On macOS and Linux it
records hashes of the current network baseline before and after. The output
contains public fixture hostnames and traffic results; credentials are never
accepted. Install the repository's npm dependencies first; macOS uses installed
Google Chrome and other platforms use Playwright Chromium unless `--browser`
specifies an executable.

Repeat with a public community gateway using explicit allowed peer IDs. Test Wi-Fi, cellular, restrictive firewall, IPv4 and IPv6 if available. Record date, platform/build, transport, topology, and exact outcomes. Do not claim these tests passed without actual machines and results.

## Relay fallback

Run a third publicly reachable node with `relay:true`. Set its real multiaddress in both endpoints' bootstrap peers. Verify relay reservations and a `/p2p-circuit/` address are available. Force the dial to that circuit address and transfer an actual HTTPS payload. Verify direct upgrade/hole punching only when observed, not inferred from libp2p being configured. The automated suite also checks actual circuit-v2 data with direct addresses removed.

## Packet VPN

Use disposable Linux VMs or containers with their own network namespace for privileged TUN route tests, and an Android emulator/device for VpnService. First test the scoped private subnet. Whole-device internet routing, DNS/IPv6 leak checks, kill switch, failover, captive portals, sleep/wake, and underlay route preservation are still public-beta gates. Never alter the developer's active host default route during these tests.

`MESHWAY_LAB_IMAGE=YOUR_TRUSTED_LOCAL_ALPINE_IMAGE ./scripts/linux-tun-lab.sh` runs a real kernel TUN ICMP round-trip test in a disposable Docker namespace with networking disabled and CAP_NET_ADMIN limited to that container. The peer packet crosses encrypted transport, enters actual `/dev/net/tun`, receives a kernel echo reply, and crosses the encrypted return path. This test has passed on Linux arm64 under Docker; it changes no host routes.

## Release gates

External NAT/relay reliability, Android/Linux live packet routing, signing/notarization, independent security review, abuse controls, automatic discovery, and safe reconnect/leak protection require verified evidence before broad public use. The source and build artifacts remain useful even while these gates are incomplete.
