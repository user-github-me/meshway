Meshway 0.1.0-alpha.2 repairs personal-server browsing under concurrent requests and adds a simpler connection dashboard. Meshway is an experimental Apache-2.0 peer VPN, SOCKS and tunneling implementation. [Source](https://github.com/user-github-me/meshway) and the [public website](https://user-github-me.github.io/meshway/) remain open to contributors. Published alpha.1 artifacts remain immutable.

The main controls now show **VPN**, **SOCKS**, and **Personal tunnel**, with service-port tools and technical settings underneath. Personal transport uses Meshway's encrypted connection through an existing, pinned OpenSSH alias where supported. Android uses direct Meshway TCP/QUIC personal-server invitations and clearly discloses that an SSH client is not included.

This release fixes an authentication concurrency limit that rejected simultaneous same-account browser streams, keeps authentication proofs tied to the exact live encrypted connection, and gives connection stages a bounded handshake deadline. The Mac app verifies application internet access separately from peer readiness, reports the measured exit IP, and detects changes to its effective proxy settings. Its optional System proxy uses a reversible authorization transaction and preserves subsequent changes made by another application. Recovery keys and account history remain local, with protected storage and optional authenticated encrypted sync.

Choose the artifact for your platform:

| Platform | Download | Included and qualification |
| --- | --- | --- |
| Android | `meshway-android-debug.apk` | Kotlin app and Go VpnService bridge; development-signature APK. Full packet VPN passed emulator checks; physical phone/carrier behavior remains separate. |
| macOS Intel or Apple Silicon | `Meshway-macos.dmg` / `Meshway-macos.zip` | Universal Swift dashboard, universal daemon and Apple packet-provider source build. SOCKS/personal SSH and optional System proxy are available. Administrator utun device VPN passed hosted kernel and public internet tests and requests macOS authorization. Installed Apple Network Extension remains a separately signed option. |
| Windows x64 or ARM64 | `Meshway-setup-x64.exe` / `Meshway-setup-arm64.exe`, or `Meshway-win-ARCH.zip` | Per-user installer/portable GUI, daemon, privileged helper and vendor-signed Wintun. Actual x64 installer tests pass; ARM64 installation remains unexecuted. |
| Linux amd64 or arm64 | `.deb`, `.rpm`, or `meshway-linux-ARCH.tar.gz` | Core and explicit Linux VPN controls; systemd packages leave startup to the owner. Both architectures passed actual container install/execute/remove checks. |
| Chrome or Firefox | `Meshway-extension-chrome.zip` / `Meshway-extension-firefox.zip` | Browser proxy extensions for local development installation. Firefox needs permission to run in private windows before proxy control. |
| npm CLI | `nazifbd-meshway-0.1.0-alpha.2.tgz` / `meshway-cli-0.1.0.tgz` | Owner-scoped registry package and GitHub launcher with independent publisher pin and explicit verified core installation. |
| Containers | `meshway-container-linux-ARCH.tar.gz` | Portable Docker image archives and `ghcr.io/user-github-me/meshway:0.1.0-alpha.2` for both Linux architectures. |
| iPhone/iPad | Public source and build scripts | Binary publication is deferred until the owner's Apple Developer account and provisioning are ready. |

Six raw cores and archives cover macOS/Linux/Windows amd64 and arm64. Exact bundled native hashes are recorded in `native-builds.json`. Explicit source ZIP/tar.gz payloads include the final committed build/packaging snapshot, alongside GitHub's immutable tag source archives. `LICENSE`, third-party notices and checksums accompany the builds. Final asset inventory, source commit and signature expiry are recorded in signed release metadata after all payloads freeze.

Install the owner-scoped package, then explicitly install the verified core:

```sh
npm install -g @nazifbd/meshway@0.1.0-alpha.2
meshway install-core
meshway version
```

The GitHub-distributed launcher is also available:

```sh
npm install -g https://github.com/user-github-me/meshway/releases/download/v0.1.0-alpha.2/meshway-cli-0.1.0.tgz
meshway install-core
meshway version
```

The registry package reports version `0.1.0-alpha.2`; the original GitHub CLI package keeps version `0.1.0`. Their installed core reports `Meshway 0.1.0-alpha.2`. Core installation verifies Ed25519 metadata, file hashes and signed license companions. Altered, expired, downgraded and conflicting releases are rejected; cached launches verify their installed binary. This release uses publisher sequence 2. The publisher signature authenticates content and does not replace Apple provisioning, notarization or Windows Authenticode certificates.

Actual Mac personal-server validation used fresh default-proxy Brave and Chrome profiles through the same-account encrypted SSH connection. Repeated concurrent browsers, disconnect/reselect cycles and owned SSH-child recovery passed expected exit-IP and certificate-verified HTTPS checks. Production website processes, routes and resolvers retained their baseline. Separate native Linux package jobs executed both architectures' Debian/RPM installation, private state permissions, required VPN tools, explicit service startup and account-preserving removal. Android's visible modes and SOCKS lifecycle passed on the final staged emulator APK; earlier real emulator TUN tests cover TCP, DNS, TLS and peer-loss behavior. Windows x64 installer tests verify exact installed payloads, shortcuts, no automatic networking, account-preserving removal and unchanged routes/DNS. See the [capability matrix](https://github.com/user-github-me/meshway/blob/main/docs/STATUS.md) and [runtime evidence](https://github.com/user-github-me/meshway/blob/main/docs/VALIDATION.md) for exact executable hashes and test scope.

The administrator Mac utun helper operates without an Apple Developer account. Actual disposable hosted Mac tests passed native DNS, certificate-verified public HTTPS and the expected exit IP in three cycles: normal disconnect, parent-exit cleanup, and forced helper termination followed by explicit recovery. Each cycle restored the prior routes, DNS and proxy. Final conservative route-absence checks also passed all three cleanup modes with simultaneous IPv4/IPv6 DNS. Temporary Azure servers and credentials were removed. The repair preserves and verifies a persistent physical route to the encrypted carrier before capturing default traffic. The Mac controls distinguish active device capture, reconnecting packet transport and recovery requiring attention; switching from System proxy to VPN starts the intended mode. There is no persistent Mac crash firewall kill switch. Physical-device cellular switching, sleep/wake, prolonged background operation, full Windows GUI route/DNS/crash behavior and independent security review remain separate checks.

Internet exits require explicit consent and peer authorization. The public owner-operated Azure relay carries encrypted peer traffic and provides no open internet exit access; [public invitations](https://github.com/user-github-me/meshway/blob/main/config/public-bootstrap.json) explain its role. This alpha provides encrypted connectivity without an anonymity or universal-network-reachability guarantee. Platform certificates and store listings remain owner-managed distribution steps.
