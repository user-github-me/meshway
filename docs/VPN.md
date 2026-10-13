# Experimental IP VPN link

Meshway's IP mode transports raw IPv4/IPv6 packets through an encrypted authenticated peer stream. It is a point-to-point link, not an automatic internet VPN service. Both endpoints need packet devices, matching MTU, permissions, and routes. Internet egress additionally needs administrator-configured forwarding/NAT and carefully chosen routes/DNS. SOCKS5 and TCP service tunnels are easier first-use modes.

## Linux TUN

Example enabled configuration:

```json
"vpn": { "enabled": true, "mtu": 1280, "device": "meshway0", "peer": "REMOTE_PEER_ID" }
```

On the receiving endpoint omit `peer` to wait for an authorized inbound stream. Put the connecting peer in `allowed_peers`. The process creating `/dev/net/tun` needs CAP_NET_ADMIN or an administrator-created interface owned by its user. Meshway creates/attaches the named interface but never changes routes itself.

For a lab using two Linux hosts, administrators can assign `10.73.0.1/30` and `10.73.0.2/30` to the two interfaces, set MTU1280, and bring them up:

```sh
sudo ip addr add 10.73.0.1/30 dev meshway0
sudo ip link set meshway0 mtu 1280 up
```

Use `.2` on the other endpoint. A ping to the opposite address tests the link once the peer stream is established. Select a subnet that does not conflict with either host. Do not replace a default route without preserving the route to the underlying peer transport. This build does not supply a kill switch or DNS leak protection.

## Android

The Kotlin app requests Android `VpnService` consent, establishes a scoped TUN interface, and supplies its file descriptor to the Go core. The core duplicates the descriptor and the Kotlin service owns the original. Peer sockets must be excluded from VPN capture and the VPN route must remain scoped to the explicit private subnet. Debug APK compilation is not evidence of working device routing; physical/emulator testing remains necessary.

## Protocol and tests

`/meshway/ip/1.0.0` authenticates peers through libp2p and an inbound allowlist. A fixed version/MTU handshake precedes big-endian uint16-sized IP packets. IPv4 total/header lengths, IPv6 payload lengths, and MTU bounds are validated before writing to the packet device. One packet session/device is supported at a time. Disconnect closes the device; reconnect requires reattaching it.

The automated integration test uses datagram socketpairs as packet devices to verify encrypted IPv4/IPv6 forward/return paths and bounded shutdown. A privileged Linux test also passed in a disposable Docker namespace: an encrypted peer packet entered an actual TUN interface and the kernel echo reply returned through the encrypted stream. `scripts/linux-tun-lab.sh` reproduces it. The opt-in `TestAndroidKernelTUNEncryptedPing` also passed with a live Android 35 emulator, actual VpnService user consent, the private TUN, and a kernel echo reply across the encrypted return path. Multi-machine routing, physical phones, reconnect/leak protection, and general internet VPN remain separate gates. macOS Network Extension, Windows Wintun integration, and iOS Network Extension are not implemented.
