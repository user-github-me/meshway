# Internet tunneling and connection modes

Meshway's main interface follows the familiar connection dashboard pattern: choose a server, Connect, see real traffic, and select how applications use the connection. The networking and private-key account system belong to Meshway. A paid provider subscription is unnecessary for your own server.

There are two separate choices:

| Choice | Options | Effect |
| --- | --- | --- |
| How traffic reaches the server | Direct encrypted TCP/QUIC, an encrypted peer relay, or a desktop SSH transport | Carries authenticated Meshway streams to the selected server |
| How applications enter the tunnel | SOCKS proxy, optional macOS System proxy, browser extension, or native VPN/TUN | Selects the applications or device traffic that uses Meshway |

The server opens internet connections from its own location. For example, a personal server in Singapore becomes the internet exit for the applications you connect through Meshway. Domain names in SOCKS requests resolve on the exit; native internet VPN also carries its configured virtual DNS through the exit.

## Your own private server

Run `meshway setup-server --personal --config server.json`, securely import the same account recovery backup on the stopped server, and start the daemon. Each client has a distinct device identity authorized by the recovery account. The server checks account membership through its authenticated peer connection, and its advertisement is private to authorized devices.

For a direct server, share its reachable TCP/QUIC invitation and select it in the client. Keep the loopback control API private. The server's internet exit remains an explicit role, and arbitrary private/LAN destinations remain disabled by default.

An SSH transport can keep the server's Meshway listener on loopback. Your desktop's existing SSH alias, agent/key authentication, and verified host key establish a private port forward. Meshway then authenticates the pinned peer and account inside that connection. No SSH password, account recovery key, or shell command belongs in the transport settings. Configuration details are in [PROTOCOL.md](PROTOCOL.md).

## Capture controls

- **SOCKS proxy:** use the displayed loopback address in an application, or enable the browser extension. Other applications retain their own connections.
- **System proxy on macOS:** explicitly opt in to configure the system's SOCKS proxy for supported applications. Meshway saves the original values and restores them when stopped. This setting does not capture applications that ignore the system proxy.
- **VPN/TUN:** the native platform interface captures traffic. Android uses VpnService, Linux uses TUN, Windows uses Wintun, and Apple platforms use Network Extension. iPhone and the Apple Network Extension path need legitimate Apple signing and provisioning. An optional macOS administrator utun helper is being qualified separately; see [MACOS_VPN.md](MACOS_VPN.md). The browser dashboard reports the packet session; it cannot create an OS VPN.
- **Personal tunnel:** selects your own account-authorized server. SOCKS capture is available for desktop SSH transport. Full-device capture through an SSH underlay requires separately validated endpoint routing; unsupported combinations are rejected before changing device networking. Android/iPhone use direct TCP/QUIC or peer relays and do not run a desktop SSH client.

Named service forwards are advanced tools for connecting a particular private service. Internet tunneling through a selected exit also supports ordinary browsing and other TCP/UDP applications.

## Reachability and speed

Direct QUIC avoids TCP transport nesting where UDP is available. TCP and SSH provide alternative paths where those protocols are reachable. Performance depends on the physical links, the server's load, configured limits, relay limits, and packet loss. SSH is a standard encrypted transport and can itself be identified or blocked. The current implementation has no independently validated TLS camouflage or censorship-resistant obfuscation transport; access on every restricted network cannot be promised. Real HTTPS through direct, relayed, and personal-server paths is tested, while tests from a specific restricted network need evidence from that network.

On a shared production VPS, start with bounded CPU, memory, sessions, and bandwidth. Change only the Meshway service and keep a service-specific rollback command. Measure production load before raising traffic limits.
