Meshway 0.1.0-alpha.1 is an experimental Apache-2.0 peer VPN, SOCKS and tunneling implementation. The public website is https://user-github-me.github.io/meshway/ and source is https://github.com/user-github-me/meshway.

This alpha includes SOCKS5 TCP/UDP, IPv4/IPv6 packet VPN and tunneled DNS; recovery-key accounts with protected local state and optional encrypted peer sync; device revocation and signed history deletion; a server list/map; personal VPS access; and reversible Linux routing, DNS and firewall setup. Internet exits require explicit consent and peer authorization. One owner-operated Azure encrypted relay is live; [public invitations](https://github.com/user-github-me/meshway/blob/main/config/public-bootstrap.json) assist peer connections and provide no internet exit access.

Choose an artifact for your platform:

| Platform | Download | What is included |
| --- | --- | --- |
| Android | `meshway-android-debug.apk` | Kotlin app and Go VpnService bridge; development APK with a debug signature |
| macOS, Intel or Apple Silicon | `Meshway-macos.zip` | Universal Swift app, packet-tunnel extension and universal daemon; owner signing is required for installed packet VPN |
| macOS, Apple Silicon fallback | `Meshway-macos-arm64-daemon-client.zip` | Simple desktop client for the local SOCKS/tunnel daemon |
| Windows x64 or ARM64 | `Meshway-win-x64.zip` / `Meshway-win-arm64.zip` | GUI, daemon, privileged VPN helper and vendor-signed Wintun driver |
| iPhone/iPad | `Meshway-ios-unsigned.ipa` / `Meshway-ios-unsigned.zip` | Swift app and packet-tunnel provider; developer artifacts requiring an Apple team and provisioning before installation |
| Linux amd64 or arm64 | `.deb`, `.rpm`, or `meshway-linux-ARCH.tar.gz` | Core and Linux VPN controls; distro packages include systemd integration and leave service startup explicit |
| Chrome or Firefox | `Meshway-extension-chrome.zip` / `Meshway-extension-firefox.zip` | Browser proxy extension; load locally for development; Firefox needs “Run in Private Windows → Allow” before proxy control |
| npm-compatible CLI | `meshway-cli-0.1.0.tgz` | Launcher with the publisher key pin and explicit verified core installer |
| Containers | `meshway-container-linux-ARCH.tar.gz` | Portable `docker load` archives; also available as `ghcr.io/user-github-me/meshway:0.1.0-alpha.1` for both Linux architectures |

Six raw cores and their archives are also available for macOS/Linux/Windows amd64 and arm64. `native-builds.json` records the exact bundled native executables. The 32 payload files include `LICENSE`, `THIRD_PARTY_NOTICES.txt` and `SHA256SUMS`; `RELEASE.json` and its detached signature complete the 34 release assets.

Install the CLI from the GitHub package, then explicitly install the pinned project core:

```sh
npm install -g https://github.com/user-github-me/meshway/releases/download/v0.1.0-alpha.1/meshway-cli-0.1.0.tgz
meshway install-core
meshway version
```

The CLI package version is 0.1.0; the installed core reports `Meshway 0.1.0-alpha.1`. There is no npm registry publication for this alpha. The installer verifies Ed25519 metadata, artifact checksums and signed license companions, and rejects altered, expired, downgraded or conflicting releases. Cached launches check the signature and binary before execution. Release sequence is 1, with new-install metadata expiring on 2027-01-12; already verified cached cores can run offline after that date.

Validation evidence covers actual Azure direct/forced-relay HTTPS, dual restrictive NAT with packet loss and recovery, Android emulator VPN TCP/DNS/TLS and exit failure recovery, Windows vendor-signed Wintun kernel IPv4/IPv6 TCP, Linux kernel VPN crash/rollback, actual Debian/RPM installation/removal, Chromium and Firefox proxy restoration, and source-built multiarchitecture containers. See the [capability matrix](https://github.com/user-github-me/meshway/blob/main/docs/STATUS.md) and [runtime evidence](https://github.com/user-github-me/meshway/blob/main/docs/VALIDATION.md) for the tested scope.

Verify `RELEASE.json.sig` using the publisher Ed25519 public key acquired independently of the download, then verify every chosen artifact hash. The release signature authenticates publisher content; macOS/Windows owner code-signing certificates and Apple provisioning remain separate requirements. Android uses a development signing key, and Wintun retains its verified upstream vendor signature. Physical-device carrier/sleep-wake behavior, installed Apple packet VPN and independent security review remain qualification gates. This alpha provides encrypted connectivity without an anonymity or universal-network-reachability guarantee.
