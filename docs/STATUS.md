# Verified status

Meshway 0.1.0-alpha.1 is an experimental open-source implementation. Source published: https://github.com/user-github-me/meshway. Website live and actual browser-verified: https://user-github-me.github.io/meshway/. Verification performed on 2026-10-14. All eight actual GitHub CI jobs passed: core, web, browser, macOS, Apple macOS/iOS packet-provider builds, Android, Windows signed Wintun IPv4/IPv6 kernel TCP, and isolated Linux privileged VPN setup. The earlier temporary validation servers were deleted; one explicitly authorized persistent public relay is now running, as described in PUBLIC_NETWORK.md.

## Implemented and exercised

| Area | Evidence |
| --- | --- |
| Encrypted peer core | Real authenticated libp2p TCP/QUIC, circuit-v2 relays, persistent per-device identities, bounded bootstrap reconnect |
| SOCKS and service tunnels | Actual HTTP through peers and named private services; SOCKS5 UDP IPv4/IPv6 echo, exit-side domain resolution, source/fragment rejection and control-EOF cleanup |
| Personal servers | Distinct devices share recovery account, signed membership, private advertisements, wrong-account denial; revoked active UDP/packet sessions closed |
| Accounts | Signed devices/history; authenticated encrypted optional sync; Keychain/Keystore/DPAPI storage integration; AES-GCM state migration/tampering tests; recovery import/rotation with restart journal; independent signed revocation propagation and signed history deletion |
| Server catalog | Live list/map, owner-supplied country, authorized click/select; persistent invitations and selection; optional bounded bootstrap directory discovery with authenticated owner requery and private-server exclusion |
| Linux internet VPN | Real kernel TUN → encrypted peer → gVisor userspace internet exit, IPv4/IPv6 TCP/UDP, kernel DNS, public TLS HTTPS; retained TUN blocks new TCP/DNS after stream loss; root CLI sets routes/DNS/nft protection and restores exact original settings, including missing-config and failed-cleanup journal cases |
| Android | Kotlin VpnService full IPv4/IPv6 capture and tunneled DNS; debug APK builds; real Android35 emulator private TUN/ICMP and account/file-picker tests; actual separate-UID internet VPN TCP, DNS, TLS1.3, lost-peer blocking, restart recovery and explicit-disconnect restoration passed ([evidence](../artifacts/android-ip-exit/result.json)) |
| Apple | Swift macOS and iOS apps plus real Network Extension provider; unsigned debug/release Xcode builds; real Swift↔Go packet bridge UDP DNS; 15 Swift tests including real daemon contract, rejected destructive account actions and state migration |
| Windows | WPF app, UAC helper, Wintun IPv4/IPv6 routes, NRPT DNS, owned firewall/journal rollback, DPAPI; x64/arm64 builds; 5,000 randomized helper CIDR test cases; actual Windows runner daemon authentication/shutdown and signed Wintun kernel IPv4/IPv6 TCP passed ([evidence](../artifacts/windows-ci-final/windows-wintun-validation.json)) |
| Browser/web | Responsive real website/dashboard, live account/server list/map/personal selection; 5 actual Chromium integration tests; browser proxy enable/restoration in isolated profile |
| External internet | Owner Mac → Azure exit → example.com TLS; forced circuit through separate Azure relay → same HTTPS; actual circuit-limited transport observed |
| Restrictive NAT/recovery | Real separate Linux NAT namespaces reject direct inbound, relayed HTTPS, 80ms±20ms delay and 3% loss, outage detection and restart HTTPS; corresponding public Azure relay/loss/restart checks also pass |
| Distribution | Six platform binaries; Debian/RPM packages with actual disposable installation/removal checks; public anonymous multiarch GHCR container and actual ARM64 execution; full native bundle scripts; Ed25519 signed release metadata with independently bundled pin, rollback/equivocation/expiry/cache validation |

Go vet and race tests pass across all packages. JavaScript has 21 passing unit tests and five browser integration tests. Swift has 15 passing tests. The native Windows helper has portable routing tests. Earlier packet parser fuzzing exercised 43,535 inputs. Security checks are development validation, not an independent audit.

External test details: [VALIDATION.md](VALIDATION.md), [cloud evidence](../artifacts/cloud-validation.json), [NAT evidence](../artifacts/nat-validation.json). Azure evidence includes binary SHA-256 and confirmed deletion of every temporary resource group. Host default routes, DNS, active VPN and system proxy were preserved.

## Artifacts and reproduction

Builds are ignored by Git. Source, build automation, public verification key and sanitized evidence are tracked. Private state/SSH/publisher credentials are excluded.

- `dist/bin/`: six Linux/macOS/Windows core binaries.
- `dist/Meshway.app`: simple unsigned macOS app; `dist/apple-macos/.../Release/Meshway.app` and `dist/apple-ios/.../Release-iphoneos/Meshway.app`: unsigned native packet VPN app/provider bundles.
- `dist/meshway-android-debug.apk`: debug-signed Android app.
- `dist/Meshway-win-x64/`, `dist/Meshway-win-arm64/`: GUI, distinct daemon/helper, Wintun and upstream license.
- `apps/web/dist/`, `apps/extension/dist/`: website/dashboard and browser extensions.
- `release/`: packaged artifacts, signed RELEASE.json and detached signature.

```sh
./scripts/check.sh
# Set CHROMIUM_EXECUTABLE if using an existing compatible Chrome for Testing.
npm run test:browser --workspace @meshway/web
MESHWAY_LAB_IMAGE=meshway:network-lab MESHWAY_LAB_INTERNET=1 scripts/linux-ip-exit-lab.sh
python3 scripts/nat-lab.py --image meshway:network-lab
# Azure provisioning requires prior owner authorization for temporary charges.
python3 scripts/cloud-validation.py --provision
```

## External release requirements

Actual signed Apple Network Extension operation requires the owner's Apple team/profiles. Authenticode/notarization/store signing requires legitimate owner credentials; automation is implemented but generated development credentials cannot replace them. Physical phones, cellular handover, prolonged sleep/wake/battery behavior, and Windows full-device helper crash/firewall behavior need their actual platform runtime results. Windows kernel packet I/O is verified separately. Browser Firefox runtime needs a field run. These cannot be certified by compilation on this Mac.

Packet-loss containment while the core remains running is tested. Process-crash containment depends on platform lockdown/firewall configuration; platform bounds are documented in [VPN.md](VPN.md) and [DESKTOP_VPN.md](DESKTOP_VPN.md). IP egress supports TCP/UDP, not ICMP forwarding or fragmented/extended IP packets. Windows peer-IP exceptions permit other applications to those exact endpoint IPs. Universal NAT reachability, anonymity, and independently audited security are not claimed.

One owner-managed Azure relay is running and incurs ongoing charges; TCP/QUIC reservations, identity-preserving updates/restarts and a real 32-KiB encrypted circuit payload passed. [Public invitations and operating limits](PUBLIC_NETWORK.md) distinguish encrypted relay service from internet exit access. The GHCR container is public and anonymously pulled; the npm CLI is an installable GitHub release package, which the owner selected instead of npm registry publication. Browser/app-store listings, sustained volunteer abuse response and an independent security review remain operational release requirements. An independent reviewer must assess an exact release commit before a production-security claim.
