# Native applications

Go powers Meshway's shared networking core. Kotlin/Android VpnService, Swift/Apple Network Extension, C#/WPF and Wintun on Windows, and Go/TUN on Linux use native operating-system APIs. TypeScript powers the website, dashboard and browser extension. Each account device has a distinct networking identity; all platforms share the versioned encrypted peer protocols.

## Apple

The simple SwiftUI daemon client builds with `scripts/build.sh` followed by `apps/macos/build.sh`. The native full-device VPN products build with `apps/apple/build.sh macos`, `apps/apple/build.sh ios`, or `apps/ios/build.sh`. Xcode and pinned gomobile produce a shared XCFramework and actual app/provider bundles. See [DESKTOP_VPN.md](DESKTOP_VPN.md) for entitlements, Keychain storage, state migration and provider lifecycle.

The app starts its own daemon, shows actual server/account state and file-picker recovery actions, and switches to the provider for full-device traffic. Apple requires valid owner team profiles to install/run the packet extension. Unsigned Release macOS/iOS builds and a real Swift↔Go packet bridge test passed. Fourteen Swift tests include actual daemon configuration acceptance and protected-state migration. The development Mac's active VPN, routes, DNS and proxy were never changed by these checks.

## Windows

Requires .NET8 and Go. Build with `apps/windows/build.ps1 -Runtime win-x64` or `win-arm64`; supply the matching pinned vendor Wintun library using `-Wintun`. `apps/windows/prepare-wintun.py` verifies the official archive hash. Packages include distinct `Meshway.exe`, `meshway-daemon.exe`, `meshway-vpn-helper.exe`, driver DLL and its upstream license.

WPF manages private account/configuration data and defaults to device VPN on server Connect. The elevated helper owns routes, tunneled DNS and scoped firewall rules with rollback and a watchdog. DPAPI protects the random state-encryption key. Actual Windows GitHub runner initialization, loopback authentication and graceful daemon shutdown passed. Portable helper tests cover 5,000 random IPv4/IPv6 CIDR cases. The opt-in signed Wintun kernel lab separately verifies real packet I/O without changing default routes/DNS; full-device firewall and crash recovery require actual native evidence.

## Android

The Kotlin foreground VpnService embeds the shared core through gomobile. Its simple server Connect flow asks Android for VPN consent, installs both-family default capture and virtual DNS, and reports connected only after the encrypted packet exit handshake. Lost peer sessions retain capture and reconnect; explicit Disconnect releases it. A separate private-subnet mode remains under Advanced settings. Android Keystore protects the random key used to encrypt local account/identity/preferences. Recovery backups use the system document picker; app backup is disabled.

```sh
export JAVA_HOME=/path/to/jdk17
export ANDROID_HOME=/path/to/android-sdk
export ANDROID_NDK_HOME="$ANDROID_HOME/ndk/27.2.12479018"
./apps/android/build.sh
adb install -r dist/meshway-android-debug.apk
```

Uses Gradle8.9, SDK/buildtools35, NDK27.2, arm64/x86_64 libraries and minSDK26. Go mobile linker compatibility uses the scoped upstream-required `-checklinkname=0`; future Go upgrades need validation. Owner signing credentials produce release APKs; the distributed development APK uses a debug key.

Actual Android35 arm64 emulator checks passed: install/start/background lifecycle, distinct-device account recovery picker flows, private kernel ICMP, and full internet VPN from a separate-UID probe: TCP, system DNS, TLS1.3 public HTTPS, lost-peer traffic blocking with TUN retained, restart recovery and explicit-disconnect normal traffic. [Evidence](../artifacts/android-ip-exit/result.json). Physical phones, battery policy, cellular handover, always-on/reboot behavior and another VPN's consent interactions need field runs. Phone inbound reachability is not assumed; peers may need relay/bootstrap transport.

## Linux, server and browser

The Go daemon provides SOCKS5 TCP/UDP, named TCP service tunnels, relays, approved public exits and same-account personal servers. Linux TUN supports IPv4/IPv6 internet packets without NAT/route privileges on the exit: gVisor reoriginates authorized TCP/UDP sockets in userspace. The local client needs explicit TUN/routes/DNS setup or the Linux privileged helper. Server installation supports containers, systemd, Debian/RPM packages and npm launcher downloads. Containers cannot silently configure the development host VPN.

The TypeScript Chrome/Firefox extension configures only a local browser SOCKS proxy. It requires consent and restores owned prior proxy settings. Real isolated Chromium tests cover activation and restoration; Firefox builds but native runtime needs field validation. A public HTTPS website cannot assume browser access to a loopback HTTP control API; the dashboard can be served locally.

## Verification

See [STATUS.md](STATUS.md), [VPN.md](VPN.md), and [VALIDATION.md](VALIDATION.md) for exact evidence and limitations. Real public Azure direct/forced-relay HTTPS, packet loss, outage/restart and confirmed cloud cleanup passed. Signing and independently audited security are separate release requirements, with automation in [RELEASING.md](RELEASING.md). Never infer universal NAT reachability or actual OS provider operation from compilation.
