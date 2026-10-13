# Native applications

Meshway uses the same Go networking core on every platform. Interfaces use the platform's native language and APIs: SwiftUI with Swift on macOS, WPF with C# on Windows, Kotlin and Android `VpnService` on Android, and Go CLI/server tools on Linux. Website and browser extension interfaces use TypeScript. These choices keep packet handling consistent while using the operating system's own application lifecycle.

## macOS

Requires macOS 13+, Swift 6, and the Go daemon. Build the core, then the app:

```sh
./scripts/build.sh
./apps/macos/build.sh
open dist/Meshway.app
```

The build script runs native client tests, builds the release Swift executable, and bundles the daemon for the current Mac architecture. Pass a daemon path as the first argument to override it. Swift source builds use `swift run --package-path apps/macos`; choose a daemon executable in settings when running outside the packaged app.

The app edits bootstrap peers, upstream peer, loopback API/SOCKS settings, approved peers, and optional relay/exit roles. Its main screen lists advertised servers and uses approved single-click exit selection. Advanced network settings are collapsed initially. Account, device records, and local history can be refreshed, with optional explicit account peer synchronization. Native file dialogs save a private recovery key or import an existing one while the daemon is stopped. Importing over an existing local account requires an explicit replacement confirmation, which explains that local history and device records are cleared. JSON fields it does not edit, including named services and TCP forwards, are preserved. Config and identity state stay under `~/Library/Application Support/Meshway`; config permissions are owner-only. Settings apply on the next daemon start. Start controls an owned subprocess, live status uses bearer-authenticated loopback requests, and Stop shuts down only the process launched by this app. An already running daemon can be viewed without taking ownership. Quitting terminates the owned subprocess.

The release is unsigned. Signing, hardened runtime configuration, notarization, updates, and a native Network Extension for device-wide IP VPN remain release gates. SOCKS5 and encrypted TCP tunnels work through the bundled core without a packet driver; the app does not modify system proxy settings or routes.

## Windows

Requires the .NET 8 SDK to build. The WPF app targets Windows and can be cross-compiled from another OS:

```sh
dotnet build apps/windows/Meshway/Meshway.csproj -c Release
```

Build a self-contained package from PowerShell after building the Go binaries:

```powershell
./apps/windows/build.ps1 -Runtime win-x64
# or: ./apps/windows/build.ps1 -Runtime win-arm64
```

The output directory contains `Meshway.exe` and `meshway-daemon.exe`. App preferences and daemon configuration use `%LOCALAPPDATA%\Meshway`, which inherits the user's directory permissions. The native app offers the same bootstrap/role/allowlist controls as macOS and preserves unedited JSON fields. It also includes server selection, account/device/history viewing, optional sync, private-server settings, and native recovery file dialogs while stopped. It calls the authenticated loopback API and uses `ProcessStartInfo.ArgumentList` to launch the daemon without a shell. Stop requests daemon shutdown; closing the window terminates its owned process tree. SOCKS and TCP tunneling are available through the daemon. A Windows packet driver, system routes/DNS integration, signing, an installer, and Windows runtime validation remain release gates.

## Android

The Kotlin app embeds the Go core using `gomobile`, rather than trying to run an executable from app storage. Its foreground service continues peer traffic after leaving the UI, exposes a Stop notification, and stores config/identity only in private app storage with backups disabled. Default relay and internet exit roles are off. It lists advertised servers with approved selection, displays local account/devices/history, and offers explicit account peer synchronization. Android's document picker saves/imports private recovery files while stopped; private transfer files are deleted afterward. Full JSON config can be edited under advanced settings before starting; the core validates it. The private server toggle enables an exit for enrolled devices sharing the same recovery key. Importing the full recovery key onto a server grants that server account enrollment authority; only use servers you control. Device revocation cannot invalidate a stolen master recovery key; see [ACCOUNTS.md](ACCOUNTS.md).

Requirements: JDK 17, the included Gradle 8.9 wrapper, Go 1.26+, Android SDK platform 35 and build tools 35.0.0, Android NDK 27.2.12479018, and pinned `gomobile`/`gobind` tools installed automatically by the build script. SDK licenses must already be accepted.

```sh
export JAVA_HOME=/path/to/jdk17
export ANDROID_HOME=/path/to/android-sdk
export ANDROID_NDK_HOME="$ANDROID_HOME/ndk/27.2.12479018"
./apps/android/build.sh
adb install -r dist/meshway-android-debug.apk
```

The build includes arm64 and x86_64 native libraries for phones and emulators. The Android binding scopes `-checklinkname=0` to the mobile build because libp2p's `github.com/wlynxg/anet` dependency accesses Go's private IPv6 zone cache; upstream currently requires this workaround on Go 1.23+. Compatibility with future Go internals needs review. The debug APK is a development artifact. Release signing uses an owner's private signing key, which is deliberately excluded from source control. `gradle assembleRelease` produces an unsigned release APK until a signing configuration is supplied.

Optional Android VPN mode creates a real TUN with Android's user consent and passes its descriptor to the Go core. It supports an explicitly entered private IPv4 address and private subnet route, an approved remote peer, and a matching 1280-byte peer MTU. The app excludes its own peer transport sockets from the VPN to avoid a routing loop. The initial client routes only the chosen private subnet. A remote peer needs its own VPN device, matching routes and peer permission; automatic internet exit NAT is not configured. Other device traffic uses its normal connection. No DNS interception, DNS leak protection, kill switch, IPv6 routes, full internet VPN, automatic network recovery, or real-phone verification is claimed. The notification says the local private interface is active only after descriptor attachment succeeds; it does not assert remote internet reachability.

## Linux and server platforms

The Go CLI is the native Linux client and server. The daemon works on Linux, macOS, and Windows and can serve encrypted relays, explicitly enabled internet exits, and named private services. Linux packet VPN uses a privileged TUN; see [VPN.md](VPN.md) and the server installation documents. Containers cannot create device-wide host VPN routes by themselves. npm is an installation/launcher convenience around the native daemon.

## Validation and release gates

The development host is an arm64 Mac. Ten Swift tests, including actual Go daemon acceptance of the native private-server config, and the release `.app` build have passed; the app has been launched locally. WPF source compilation and self-contained win-x64 and win-arm64 publishes have passed on macOS; the Windows UI and process lifecycle require testing on Windows. Android arm64/x86_64 debug APK compilation has passed using a locally installed SDK. On an Android 35 arm64 emulator, installation, account creation, foreground operation while backgrounded, Stop, authenticated status, outbound encrypted peer dialing to a Mac gateway, approved server selection, and disconnect have passed. Android recovery export through the system document picker produced a valid-sized private recovery backup and removed its temporary app file. Explicitly confirmed SAF import of that backup preserved the account ID and device peer ID and successfully restarted the node. A real Android VpnService TUN was established with system user consent. The opt-in `TestAndroidKernelTUNEncryptedPing` passed: a Mac packet socket sent an ICMP request through an encrypted stream into the Android TUN; the Android kernel generated a valid echo reply over the return encrypted stream. Stop removed `tun0` and the foreground service. Revocation, different-account migration, real-phone behavior, and battery/background restrictions still require broader verification. Android interface enumeration on this emulator is restricted by SELinux, so its advertised addresses are loopback only; outbound peer dialing succeeds. Public inbound phone reachability is not assumed. Signing and public store/release publication have not been performed.

Repeat the Android TUN lab on a disposable emulator with the native app stopped, `MESHWAY_ANDROID_TUN_LAB=1 MESHWAY_ANDROID_PEER_ID=<device-peer-id> go test -v ./internal/vpn -run TestAndroidKernelTUNEncryptedPing -count=1`. The test prints a temporary host identity and bootstrap address. Configure that address and approved peer in the Android app, enable its private VPN for `10.77.0.2/30` and route `10.77.0.0/30`, then start and accept Android VPN consent within 90 seconds. The host uses a socket pair and leaves host networking unchanged.
