# Verified status

Meshway is an experimental open-source implementation. Alpha.1 is public; alpha.2 is being packaged with the qualified macOS administrator VPN helper. Source published: https://github.com/user-github-me/meshway. Website live and actual browser-verified: https://user-github-me.github.io/meshway/. Verification performed on 2026-10-14. All eight jobs passed in [CI run 38505614494](https://github.com/user-github-me/meshway/actions/runs/38505614494) at production source `f651571`: core, web, browser, macOS, Apple macOS/iOS packet-provider builds, Android, Windows signed Wintun IPv4/IPv6 kernel TCP and production firewall on an owned adapter, and isolated Linux privileged VPN setup. The earlier temporary validation servers were deleted; one explicitly authorized persistent public relay is now running, as described in PUBLIC_NETWORK.md.

## Alpha.2 connection repair

The personal macOS connection completed storage and network-preferences authorization. Its authenticated daemon and effective System proxy passed real-browser checks. A newer universal app is staged; an older duplicate app bundle still owns the live proxy and must restore its settings before replacement. Three repeated pairs of fresh Brave and Chrome profiles used macOS's existing System proxy and passed the expected Singapore server IP, dual-stack IP endpoint and certificate-verified HTTPS checks. Six additional disconnect/reselect cycles and loss/recovery of the fixture's owned SSH child passed with the final core. [Sanitized evidence](../artifacts/personal-browser-validation.json) records the exact executable hashes; private addresses, credentials and diagnostics remain ignored.

Connect separates authenticated server readiness from supported-application internet access. The app verifies HTTPS through a fresh default-proxy URLSession, observes forwarded payload and displays the measured exit IP before reporting internet ready. It detects externally changed effective proxy settings and clears that success state. All enabled network services share one authorization and preferences commit, competing HTTP/HTTPS/PAC enable flags are disabled reversibly, and restoration preserves subsequent external changes. Native authorization remains a macOS requirement. The preferences transaction now runs in one owned user subprocess with a 120-second deadline, private framed pipes and retained authorization. Five actual child-process tests cover repeated transactions, an unanswered prompt, malformed replies and bounded settings reads; timeout retains the restoration journal and working listener. The worker and administrator VPN controller have 49 passing Swift tests, including owned-process timeout and stale-readiness cancellation checks. The final universal app is built separately; live GUI replacement is not inferred from a bundle copied onto disk.

The fixes include managed foreground SSH with pinned same-account authentication, personal-authentication single-flight under concurrent browser requests, and separate bounded connection stages within a 30-second peer handshake. Fifty concurrent authenticated requests and encrypted personal SOCKS streams pass race tests; a deliberately delayed acknowledgement reproduces the former deadline and passes the repair. The original eight-session private server limit is now 64, with its 256-MiB memory, 10% CPU, 64-task and 1-MiB/s per-peer bandwidth limits preserved. The production websites' process IDs/start times, routes and resolver hash still match their baseline. Tests never changed this development Mac's routes, DNS or proxy; the user explicitly authorized normal app proxy actions.

The shared website logo is packaged in the native dashboard and Dock icon. Twenty-eight Swift tests, native button-edge dispatch and an isolated SystemConfiguration dictionary check pass. Own-Keychain queries run away from the main thread and expose an explicit permission action while preserving the existing account.

The administrator macOS utun bridge passed real kernel and public internet tests on disposable hosted Macs. Native DNS, the expected public exit IP and certificate-verified HTTPS passed normal Stop, parent-exit cleanup, and helper SIGKILL followed by explicit recovery. A persistent physical server bypass fixes the route-clone error that previously captured the encrypted carrier itself. Final conservative stale-route checks passed the same three cleanup modes with simultaneous IPv4/IPv6 DNS. Original routes, DNS and proxy were restored; every temporary Azure server was deleted. [Public internet evidence](../artifacts/macos-utun-public-validation.json) and [final kernel evidence](../artifacts/macos-utun-full-loopback-validation.json) identify the tested sources and binary hashes. This helper is enabled in the staged alpha.2 Mac app and does not require an Apple Developer account. Apple Network Extension signing remains separate. Physical sleep/wake and continuous crash leak protection are not established. See [MACOS_VPN.md](MACOS_VPN.md).

## Implemented and exercised

The initial GitHub release is public with 38 assets and 36 signed payloads. Both the GitHub CLI tarball and public npm package passed anonymous download, temporary installation, signed core installation, and rejection of a modified cached executable ([GitHub evidence](../artifacts/published-install-validation.json), [npm evidence](../artifacts/npm-core-install-validation.json)). Published alpha.1 files remain immutable. Subsequent connection-dashboard and unsigned-macOS fixes are being validated for alpha.2; rebuilding source does not update an already published installer.

| Area | Evidence |
| --- | --- |
| Encrypted peer core | Real authenticated libp2p TCP/QUIC, circuit-v2 relays, persistent per-device identities, bounded bootstrap reconnect |
| SOCKS and service tunnels | Actual HTTP through peers and named private services; SOCKS5 UDP IPv4/IPv6 echo, exit-side domain resolution, source/fragment rejection and control-EOF cleanup |
| Personal servers | Distinct devices share recovery account, signed membership, private advertisements, wrong-account denial; revoked active UDP/packet sessions closed |
| Accounts | Signed devices/history; authenticated encrypted optional sync; Keychain/Keystore/DPAPI storage integration; AES-GCM state migration/tampering tests; recovery import/rotation with restart journal; independent signed revocation propagation and signed history deletion |
| Server catalog | Live list/map, owner-supplied country, authorized click/select; persistent invitations and selection; optional bounded bootstrap directory discovery with authenticated owner requery and private-server exclusion |
| Linux internet VPN | Real kernel TUN → encrypted peer → gVisor userspace internet exit, IPv4/IPv6 TCP/UDP, kernel DNS, public TLS HTTPS; retained TUN blocks new TCP/DNS after stream loss; root CLI sets routes/DNS/nft protection and restores exact original settings, including missing-config and failed-cleanup journal cases |
| Android | Kotlin VpnService full IPv4/IPv6 capture and tunneled DNS; debug APK builds; real Android35 emulator private TUN/ICMP and account/file-picker tests; actual separate-UID internet VPN TCP, DNS, TLS1.3, lost-peer blocking, restart recovery and explicit-disconnect restoration passed ([evidence](../artifacts/android-ip-exit/result.json)) |
| Apple | Swift macOS and iPhone/iPad apps plus real Network Extension provider; unsigned debug/release Xcode builds and developer IPA; real Swift↔Go packet bridge UDP DNS; 15 Swift tests including real daemon contract, rejected destructive account actions and state migration |
| Windows | WPF app, UAC helper, Wintun IPv4/IPv6 routes, NRPT DNS, owned firewall/journal rollback, DPAPI; x64/arm64 builds; 5,000 randomized helper CIDR test cases; actual Windows daemon authentication/shutdown, signed Wintun IPv4/IPv6 kernel TCP and production firewall blocking/peer bypass/cleanup on one owned adapter passed ([evidence](../artifacts/windows-runtime-final/windows-firewall-validation.json)) |
| Browser/web | Responsive real website/dashboard, live account/server list/map/personal selection; 5 actual Chromium integration tests; actual Firefox 155 extension consent, domain proxying, private-window permission rejection and exact prior proxy restoration |
| External internet | Owner Mac → Azure exit → example.com TLS; forced circuit through separate Azure relay → same HTTPS; actual circuit-limited transport observed |
| Restrictive NAT/recovery | Real separate Linux NAT namespaces reject direct inbound, relayed HTTPS, 80ms±20ms delay and 3% loss, outage detection and restart HTTPS; corresponding public Azure relay/loss/restart checks also pass |
| Distribution | Six platform binaries; Debian/RPM packages with actual disposable installation/removal checks; public anonymous multiarch GHCR container and actual ARM64 execution; full native bundle scripts; Ed25519 signed release metadata with independently bundled pin, rollback/equivocation/expiry/cache validation |

Go vet and race tests pass across all packages. JavaScript has 21 passing unit tests and five browser integration tests. The macOS core has 49 passing Swift tests. The native Windows helper has portable routing tests. Earlier packet parser fuzzing exercised 43,535 inputs. Security checks are development validation, not an independent audit.

Windows runtime evidence above comes from actual CI run `38505614494`, job `115662528703`, at commit `f651571c17672e27582672bff0ef3f24463bc9d3`. Production firewall creation took 3.386 seconds. Eight real kernel UDP observations verified IPv4/IPv6 traffic before rules, blocking while active, exact-peer bypass, and restoration after cleanup; partial setup failure cleanup also passed. Only the disposable Wintun adapter was modified, and original default routes/DNS remained unchanged. [Provenance](../artifacts/windows-runtime-final/provenance.json) identifies the exact tested sources. NDP/DHCP filters were inspected; physical lease renewal and the complete GUI route/DNS/crash lifecycle remain separate platform checks.

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

Actual signed Apple Network Extension operation requires the owner's Apple team/profiles. Authenticode/notarization/store signing requires legitimate owner credentials; automation is implemented but generated development credentials cannot replace them. Physical phones, cellular handover, prolonged sleep/wake/battery behavior, and Windows full-device helper crash behavior need their actual platform runtime results. Windows kernel packet I/O is verified separately. Firefox runtime passed in an isolated profile; browser store distribution is separate. These cannot be certified by compilation on this Mac.

Packet-loss containment while the core remains running is tested. Process-crash containment depends on platform lockdown/firewall configuration; platform bounds are documented in [VPN.md](VPN.md) and [DESKTOP_VPN.md](DESKTOP_VPN.md). IP egress supports TCP/UDP, not ICMP forwarding or fragmented/extended IP packets. Windows peer-IP exceptions permit other applications to those exact endpoint IPs. Universal NAT reachability, anonymity, and independently audited security are not claimed.

One owner-managed Azure relay is running and incurs ongoing charges; TCP/QUIC reservations, identity-preserving updates/restarts and a real 32-KiB encrypted circuit payload passed. [Public invitations and operating limits](PUBLIC_NETWORK.md) distinguish encrypted relay service from internet exit access. The GHCR container is public and anonymously pulled; the CLI is distributed through npm as `@nazifbd/meshway` and GitHub release tarballs. The owner deferred the iPhone/iPad binary release until an Apple Developer account is ready; its source and build scripts remain public. Browser/app-store listings, sustained volunteer abuse response and an independent security review remain operational release requirements. An independent reviewer must assess an exact release commit before a production-security claim.
