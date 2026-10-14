# Native desktop and iOS packet VPN

Meshway now contains actual Apple `NEPacketTunnelProvider` and Windows Wintun integrations. The packet core uses the authenticated `/meshway/ip-exit/1.0.0` protocol. Its userspace exit stack forwards TCP and UDP, IPv4 and IPv6, and virtual DNS through approved internet exits. Internet exits still need explicit operator consent and peer authorization.

## Apple

`apps/apple/Meshway.xcodeproj` has four targets: macOS app/packet extension and iOS app/packet extension. The app stores a private configuration in `group.network.meshway`, stops its foreground core before the provider opens the shared identity, saves the native system VPN preference, and calls `startVPNTunnel`. The extension embeds the actual Go core from a gomobile XCFramework, opens the encrypted peer stream before installing its network settings, and bridges `NEPacketTunnelFlow` packets with bounded Go queues. SwiftUI's status observes `NEVPNStatus`, rather than assuming that a selected server is connected.

The provider installs IPv4 and IPv6 default routes, virtual IPv4/IPv6 DNS, and 1280 MTU. Numeric peer/relay transport endpoints are excluded as exact host routes; provider-owned transport sockets use the OS underlay. DNS resolution through the OS targets virtual `10.77.0.1`/`fd77::1` and is forwarded by the selected exit. A peer loss leaves the interface and DNS settings installed, marks the provider reasserting, and reconnects. Explicit Disconnect releases the interface. `includeAllNetworks` is enabled; process crashes, reboot, sleep/wake, another active VPN, and OS exceptions require signed-device validation before stronger kill-switch guarantees can be made.

Private state uses a fresh random 32-byte key in a single named Meshway Keychain item, shared only between the app and its extension. The item is non-synchronizing and device-only, available after first unlock. The key is passed directly into `StartSecure` or the foreground daemon's private environment; it is never included in VPN preferences, JSON configuration, API responses, or logs. The Go store encrypts identity/account files. Existing plaintext state is migrated by keyed reads. Recovery exports remain deliberately unencrypted backups which users must keep private.

Build without accessing signing credentials or changing host network settings:

```sh
apps/apple/build.sh macos
apps/ios/build.sh
apps/apple/build.sh iossimulator
```

Xcode, Go, and gomobile are required. Builds are serialized around the shared XCFramework. `MESHWAY_REUSE_APPLE_CORE=1` reuses a previously built matching framework. macOS and iOS device Release builds passed on this Mac. The simulator target can be compiled without an installed simulator runtime. The unsigned products are at `dist/apple-macos/Build/Products/Release/Meshway.app` and `dist/apple-ios/Build/Products/Release-iphoneos/Meshway.app`. A properly provisioned Apple Developer Team, approved Network Extension capability, app-group/keychain entitlements, and OS VPN consent are required to run the native provider. Do not claim that compiling an unsigned extension proves actual OS tunnel operation.

Fifteen Swift tests passed, including actual daemon acceptance and packet plan tests. The standalone `apps/apple/Tests/bridge-smoke.sh` was compiled and run against the actual XCFramework: Swift started secure nodes, authorized an encrypted IPv4 UDP echo packet, and received the packet response through the Go callback bridge. This test uses loopback peers and ephemeral test keys, with no host routes, DNS, active VPN, or proxy changes and no Keychain reads.

## Windows

The WPF Connect action defaults to device VPN. It stops the owned foreground core, launches the bundled `meshway-vpn-helper.exe` with Windows UAC consent, and waits until encrypted transport, Wintun, route setup, and firewall setup have succeeded. The helper accepts a private request file containing no secrets and invokes only its adjacent `meshway-daemon.exe`. Both executables are packaged with distinct names to avoid case-insensitive collisions. Switching off device VPN uses SOCKS mode.

The helper resolves the chosen peer and bootstrap transports before installing routes, records the physical route to each exact transport IP, creates the Meshway Wintun adapter, installs both virtual addresses, and adds IPv4/IPv6 `/1` routes without replacing any previous default routes. Virtual DNS plus an owned Windows NRPT rule prevents normal system resolver traffic from racing over another interface. Existing system proxy settings and other adapters' DNS settings are not edited.

Firewall protection blocks remote address space on each active non-Meshway adapter except exact known peer transport IPs. Windows block rules override allow rules, so a tested prefix-complement algorithm computes disjoint CIDRs around the underlay exceptions. The exception is an IP address exception, not a WFP per-process rule: other applications could contact those exact transport IPs. New network adapters, DHCP/DNS on uncommon networks, local-link traffic, switching networks, another VPN, sleep/wake, helper termination, and reboot require real Windows validation. Strict universal leak-proof claims are inappropriate before that evidence exists.

A bounded private ownership journal records only the helper's routes, NRPT identifiers, and random firewall rule group. Failed initialization restores changes; daemon loss retains blocking until Disconnect or app close. Cleanup removes only owned changes before closing Wintun. An unfinished cleanup leaves its journal for the next elevated helper to recover. The app's unexpected exit is observed by the helper's parent-process watchdog.

Windows private state uses a per-user DPAPI-protected random 32-byte storage key. The foreground app and elevated helper pass it to the core only through its private environment. Windows administrators and processes running as that same account remain trusted.

Pure network-plan tests passed on macOS, including 5,000 randomized IPv4/IPv6 prefix coverage cases, exact endpoint exclusions, relay parsing, and invalid invitations. WPF and the elevated helper compile and self-contained win-x64/win-arm64 publishes pass. Neither Windows route/firewall operations nor DPAPI were executed on this Mac. Actual Wintun and Windows UI tests need a Windows machine.

Build:

```powershell
./apps/windows/build.ps1 -Runtime win-x64 -Wintun path/to/wintun.dll
```

`apps/windows/prepare-wintun.py --arch amd64 --output dist/Meshway-win-x64/wintun.dll` downloads the official pinned Wintun 0.14.1 archive without installing a driver. The archive SHA-256 is `07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51`; the helper also pins architecture-specific DLL hashes before launching the daemon. Verify Authenticode on Windows before release. Include `WINTUN-LICENSE.txt` alongside each redistribution. Wintun source is GPL-2.0 at <https://git.zx2c4.com/wintun/>; the official Microsoft-signed prebuilt driver DLL carries its separate redistribution license. Full source reproducibility requires building the upstream driver with WDK and obtaining the platform-required driver signature; a locally compiled unsigned kernel driver cannot be represented as equivalent to the official signed artifact.

## Reproducible isolated packet tests

`apps/android/probe-app` is a disposable separate-UID app whose sockets are captured by Meshway's real VpnService rather than excluded as Meshway transport sockets. Its HTTP, TLS hostname verification, and DNS checks make the Android kernel VPN path measurable. `scripts/android-ip-exit-lab.py --binary dist/meshway --internet` opts into public DNS/TLS requests, tests actual kernel TUN traffic, confirms routes remain during peer loss, checks packet reconnect, and confirms explicit Disconnect restores normal traffic. Six checks passed on the Android 35 arm64 emulator: kernel TCP, system DNS, certificate-verified TLS 1.3 to a real public IP, peer-loss blocking with routes retained, encrypted reconnect, and explicit disconnect restoring normal traffic. The evidence is `artifacts/android-ip-exit/result.json`. It accepts only emulator serials. This lab never changes the development host's routes, DNS, active VPN, or proxy.

## Native account maintenance

All four native apps offer recovery replacement, signed device revocation, and history deletion with an explicit option to propagate a signed deletion on the next account sync. Recovery replacement requires Meshway and its VPN stopped, warns that the account ID changes, saves a new private backup through the system picker, and directs users to import that backup on every trusted device and personal server. macOS/Windows use the encrypted-state CLI. Android/iOS stage a new root in private temporary storage and commit it to the real account only after the chosen backup file is written successfully; cancelling a picker or failing to save leaves the current account intact. Revocations cannot repair a stolen recovery key; replacement creates a separate account while the old key still controls the old account.
