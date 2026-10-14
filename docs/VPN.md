# Packet VPN and Internet exits

Meshway has two packet modes. Internet mode sends device IPv4/IPv6 TCP and UDP through an approved encrypted peer to a userspace Internet exit. The older point-to-point mode links two TUN devices for explicitly routed private networks. SOCKS5 CONNECT/UDP ASSOCIATE and named TCP services also work without device-wide routes.

## Internet mode

An exit operator enables `exit: true` and approves callers using `allowed_peers`, or enables `personal_server` to require same-account signed device certificates. The same consent and private-target policy apply to packet and SOCKS traffic. The exit uses a separate gVisor TCP/IP stack for each approved device and creates ordinary TCP/UDP sockets for permitted destinations. The server requires no TUN, root privileges, forwarding sysctls, NAT rules, or default-route changes.

Clients use MTU 1280, IPv4 address `10.77.0.2/32`, IPv6 `fd77::2/128`, IPv4 DNS `10.77.0.1`, and optional IPv6 DNS `fd77::1`. These addresses are scoped to each encrypted session; different users do not share packet stacks. Virtual DNS port 53 is forwarded to the exit's explicit literal `dns_resolver`, default `1.1.1.1:53`. Both TCP and UDP DNS are supported. A private resolver requires explicit `allow_private_targets: true`. Public destination policy rejects private, reserved, loopback, multicast, and mapped private IPv6 addresses unless the operator permits them. Fake-IP DNS from another active VPN can return reserved addresses that Meshway correctly refuses; choose a real resolver on the exit.

Example client configuration:

```json
"vpn": { "enabled": true, "internet": true, "mtu": 1280, "device": "meshway0", "peer": "REMOTE_PEER_ID" }
```

The exit's `vpn` entry is optional because `exit: true` registers the packet exit protocol. An explicit resolver can be configured in its VPN entry:

```json
"exit": true,
"vpn": { "enabled": true, "dns_resolver": "1.1.1.1:53", "mtu": 1280 }
```

`/meshway/ip-exit/1.0.0` uses a bounded version/MTU handshake and big-endian uint16 packet framing over libp2p's authenticated encrypted stream. It validates IP lengths, negotiated MTU, expected device source addresses, destination policy, and TCP/UDP protocol before passing packets to gVisor. IPv4 fragmentation, IPv6 extension headers, and Internet ICMP forwarding are currently rejected. This is a TCP/UDP Internet VPN; arbitrary IP protocols and public ICMP ping are not promised. Native clients must use the supplied MTU. Session and flow limits bound concurrent users and destination sockets; idle TCP and UDP sockets expire. Byte counters include packet traffic and SOCKS payload traffic, while transport/relay overhead is not included. Configured bandwidth limits apply to both directions.

The client retains its TUN or packet-flow adapter when an encrypted session fails and automatically retries the selected peer every two seconds. Outgoing packets are dropped until the session returns. Explicit Stop closes the adapter. A running daemon therefore cannot silently switch captured traffic to a direct exit during a peer outage. OS-level protection is still needed for process crashes, sleep/wake and applications exempted by the OS: use Android always-on/block-connections settings, Apple Network Extension policies, or an installed Windows/Linux firewall. A peer-loss test alone is not proof of all-platform leak protection.

## Native packet interfaces

The Go binding provides `Node.StartInternetVPN(fd, peerID)` for Android/TUN descriptors and `StartPacketVPN(peerID)`, `ReadPacket()`, `WritePacket(packet)`, `StopPacketVPN()` for Apple Network Extension packet flow. FD ownership is duplicated; the caller retains its original. Packet queues are bounded, dropped packets do not block the network stack, and stop unblocks pending reads. VPN status reports active session, selected peer, device, and last error.

Android's Kotlin VpnService assigns IPv4/IPv6 default routes and tunneled DNS after user consent. The app's peer transport must be excluded from VPN capture. Always-on/lockdown is a separate Android OS choice and physical device/vendor behavior still needs verification. Windows creates a bounded Wintun packet ring with the signed architecture-specific `wintun.dll` beside `meshway-daemon.exe`; administrator-native setup owns addresses, DNS, firewall, transport bypass and cleanup. Apple uses `NEPacketTunnelProvider`, requiring actual Network Extension signing and entitlements. Building a driver bridge does not establish that permissions, routes, DNS and crash cleanup work on a real Windows/Apple device.

Linux interface creation needs CAP_NET_ADMIN or an administrator-created TUN owned by the process. Meshway opens the named device; routes, DNS and firewall are explicit administrative setup. Keep routes to the underlying peer and relay addresses on the physical network before capturing default traffic. Never apply full routes to the development host during tests.

## Point-to-point compatibility

`/meshway/ip/1.0.0` still connects one local packet device to one allowed peer with matching MTU. Omit `internet` from configuration to use this protocol. An empty peer waits for an approved incoming packet stream. Both ends need their own TUN/VpnService descriptors, explicitly assigned subnet addresses and routes. This mode does not supply general Internet egress or DNS, and loss of its peer closes its device. It is useful for private subnet labs and exact host-to-host IP links.

## Verified network labs

The following tests were run on 2026-10-14:

- Go race tests exercise encrypted IPv4/IPv6 TCP, UDP and DNS forwarding; private DNS/destination rejection; consent/authorization; forged device sources; bounded sessions; and automatic stream reconnect while retaining the native packet device.
- `MESHWAY_LAB_IMAGE=meshway:network-lab MESHWAY_LAB_INTERNET=1 scripts/linux-ip-exit-lab.sh` creates disposable, separately routed Linux client and gateway Docker namespaces. Actual Linux kernel TCP crosses a real TUN for IPv4 and IPv6. Actual UDP DNS and certificate-verified public HTTPS pass through the encrypted exit. Forced exit loss retains the client interface and fresh TCP/DNS fail closed. All containers, routes and DNS are owned by the lab and removed afterward; the Mac's routes, DNS, active VPN and proxy are unchanged.
- `MESHWAY_INTERNET_VPN_LAB=1 MESHWAY_LAB_HTTPS_IP=PUBLIC_EXAMPLE_COM_IP go test ./internal/vpn -run TestInternetVPNPublicHTTPSAndDNS -v` tests encrypted synthetic IP packets plus real UDP DNS and certificate-verified HTTPS. The optional literal IP supports development hosts with an existing fake-IP VPN without changing its settings.
- `scripts/linux-tun-lab.sh` separately verifies the compatible point-to-point protocol using real Linux kernel ICMP. The existing opt-in Android kernel lab verifies point-to-point Android VpnService TUN ICMP; Android full Internet behavior is recorded separately when exercised.
- SOCKS UDP tests exercise IPv4/IPv6 and domain destinations, private target/consent/allowlist denial, rejection of a different local sender port, FRAG rejection, and closure when the TCP control connection ends.

These labs prove the tested packet paths and specific peer-outage behavior. Physical Android, Apple entitlements/runtime, Windows runtime and firewall cleanup, independently located restrictive NAT networks, roaming, sleep/wake, and daemon-crash leak behavior require their own evidence.
