# Meshway implementation plan

Meshway is an Apache-2.0 open source peer network with a shared Go networking core, desktop, Android and iPhone/iPad clients, a browser proxy extension, a local dashboard, a public website, and installable server packages. The owner selected Meshway and authorized github.com/user-github-me/meshway releases/packages and Azure validation/deployment. The website runs on GitHub Pages, and one bounded encrypted public relay runs in Azure; personal servers and consenting internet exits remain separately operated.

## Product decisions

- A user can run a client, a private service gateway, a relay, an internet exit, or several of these roles on the same machine.
- Peers use libp2p authenticated encrypted streams. Peer IDs identify public keys; direct connections are preferred. Circuit relay and hole punching use the established libp2p implementations.
- Joining is by explicit peer multiaddress or configured bootstrap peers. No centrally hosted discovery service is assumed. Running an initial relay/bootstrap on a VPS remains necessary for networks behind restrictive NAT.
- Relaying encrypted traffic and forwarding internet traffic are separate capabilities. Internet exits and private/LAN targets need explicit opt-in. Default operation never publishes an open internet proxy.
- SOCKS5 and TCP tunnels operate without system VPN privileges. IP VPN mode needs an OS TUN interface or a mobile VPN API, routes, and explicit setup. Browser extensions configure a browser proxy; they cannot supply a device-wide VPN.
- Native SwiftUI on macOS, C#/WPF on Windows, and Kotlin on Android use the shared Go daemon or gomobile binding. Linux uses the CLI and local web dashboard. Linux uses TUN; Apple platforms use Network Extension and Windows uses Wintun. An optional administrator utun bridge for unsigned macOS builds is being qualified separately. Native runtime evidence and legitimate signing remain separate requirements.
- No custom encryption or anonymity claims. Relay/exit metadata, exit visibility, network abuse, traffic limits, and platform privileges are documented. Public internet exit operation is experimental pending security review.
- Accounts are generated Ed25519 recovery keys without email or a hosted account database. A separate device peer key is authorized by the account root, avoiding conflicting peer IDs when one account is used on multiple devices. History is local; encrypted, authenticated sync is optional. Device visibility means paired/synced devices with last-seen metadata, not an omniscient live global directory. Revoking a device cannot neutralize a stolen account recovery key; offline root rotation produces a new account and requires every private server to import the new backup.

## Architecture and contract

```
Website / Swift / C# / Kotlin / Browser extension
                    |
            loopback authenticated API
                    |
       Go daemon + encrypted libp2p host
       |             |               |
     SOCKS5       TCP tunnel        IP tunnel
       |             |               |
       +------ encrypted streams ----+
                     |
       direct peer / circuit relay / opt-in gateway
```

The Go CLI entrypoint is `cmd/meshway`. Packages under `internal/` own the daemon, local API, SOCKS5 listener, tunnels, and Linux TUN support. Public Go `pkg/meshway` supplies a small core API suitable for embedding and gomobile. Build scripts produce binaries for Linux, macOS, and Windows. Node workspaces own `apps/web`, `apps/extension`, and `packages/cli`.

Configuration is JSON: identity/state directory; libp2p listen addresses; bootstrap peer multiaddresses; optional upstream peer ID; loopback SOCKS/API addresses; API token; relay role; explicit allowed inbound peer IDs; exit role; explicit private-destination permission; named TCP forwards; optional VPN addresses. Default ports: peer TCP/QUIC 4001, SOCKS 1080, API 7777. API stays loopback only and requires a bearer token. Browser UI origins are explicit. The core agent will commit exact schema and API details in `docs/PROTOCOL.md`.

## Roadmap and acceptance checklist

### 0 — Design and project foundation
- [x] Select architecture, roles, consent boundaries, and shared-core strategy.
- [x] Write roadmap before implementation.
- [x] Add license, security policy, contribution guide, example configuration, and threat model.

### 1 — Working encrypted peer network
- [x] Persistent identity and libp2p encrypted node.
- [x] Direct peer dialing; bootstrap addresses; circuit relay hosting/client configuration.
- [x] Peer permissions; opt-in exit; blocked private targets by default.
- [x] SOCKS5 CONNECT with bounded framing and timeouts.
- [x] TCP forwards over peer streams.
- [x] Authenticated loopback control API with status and shutdown.
- [x] Integration test: local client → encrypted peer → HTTP origin.
- [x] Integration test: named TCP tunnel and denied requests.

### 2 — IP VPN and embedding
- [x] Linux TUN packet framing, validation, and bounded reads.
- [x] Android external VPN file descriptor support.
- [x] IPv4/IPv6 internet egress, virtual tunneled DNS, native capture/failure controls, and documented platform bounds.
- [x] Small mobile binding API and lifecycle support.
- [x] Native Android client source and reproducible APK build scripts.
- [x] Native Apple Network Extension and Windows Wintun/helper source, builds, DNS and failure controls; signed runtime validation remains platform evidence.
- [x] Swift iPhone/iPad app, Network Extension provider, unsigned developer IPA and entitlement/archive/export automation; unsigned Xcode builds pass.

### 3 — User-facing apps
- [x] Real opt-in server catalog, list/map, one-click select/disconnect, and advanced settings.
- [x] Personal VPS mode uses same-account certificates without public allowlists.
- [x] Public responsive website with truthful capability matrix and install guide.
- [x] Local dashboard using live authenticated API.
- [x] Native desktop lifecycle implementations and packaging; macOS runtime, Windows daemon and signed Wintun IPv4/IPv6 kernel runtime exercised.
- [x] Browser extension local proxy pairing, enable/disable, and cleanup.
- [x] Website/dashboard production build and extension package verification.

### 4 — Distribution
- [x] npm launcher/installer with checksum verification and supported-platform mapping.
- [x] Multi-stage container source and validated Compose; public multiarch GHCR image anonymously pulled and ARM64 executed.
- [x] Linux systemd example and setup script; Debian/RPM installation/removal checks and a live hardened Azure systemd relay deployment.
- [x] Cross-platform binary build and release archive script.
- [x] CI published; actual core/web/browser/macOS/Android/Windows checks and isolated Linux privileged helper checks pass.
- [x] Signed publisher metadata, native signing/notarization scripts, verified artifact packaging and release/Pages automation for the authorized repository.
- [x] Record actual GitHub source/website/container publication and native/kernel CI evidence.
- [x] Direct Windows x64/ARM64 EXE installers and universal macOS DMG; actual x64 install/uninstall and mounted DMG content checks pass.
- [x] Public npm `@nazifbd/meshway` alpha package; anonymous download and temporary installation verified.
- [x] Publish the initial signed GitHub release and verify installation from both public GitHub and npm downloads, including rejection of a modified cached core.
- [ ] Legitimate owner-signed native/store releases and physical-device field gates.

### 5 — Harden and verify
- [x] Test auth, origin restrictions, private target policy, stream limits, malformed SOCKS traffic, and shutdown.
- [x] Run test → fix → retest loops until local checks pass.
- [x] Record exact tested platforms and features in `docs/STATUS.md`.
- [x] Android emulator real TUN and Linux privileged namespace TUN tests.
- [x] Real Azure different-machine direct/forced-relay HTTPS, loss/outage/restart and confirmed cleanup; isolated restrictive dual-NAT real HTTPS.
- [ ] Physical phone/cellular and signed Apple/Windows native route tests with actual hardware/credentials.
- [x] Bounded sessions/flows, per-peer bandwidth limits/accounting, optional trusted directory discovery, signed update verification, IPv4/IPv6 UDP/TCP, tunneled DNS and kernel failure-containment tests.
- [ ] Independent audit, sustained hostile-load/performance field assessment, public operator abuse policy/reputation and production signed-platform failure tests.

### 6 — Private-key accounts and local-first data
- [x] Generate account recovery key and signed per-device membership certificates.
- [x] Recovery backup/import using private files; never log recovery secrets.
- [x] Local bounded connection/event history and device registry.
- [x] Optional encrypted peer-to-peer history/device sync authenticated by root signatures and transport identity.
- [x] CLI and authenticated local API for account status, devices, history, and explicit sync.
- [x] Test wrong-account rejection, tampered device/event rejection, replay deduplication, recovery import, and offline storage.
- [x] Offline recovery-root rotation, Keychain/Keystore/DPAPI-protected state key, atomic encrypted migration, persisted invitations, signed history deletion and independent revocation propagation.
- [x] Native account/file-picker flows and simple server selection with advanced controls; sync conflicts reject explicitly without silent data overwrite.
- [ ] Mature field-tested onboarding, delegated enrollment without sharing the root, offline-wide compromise recovery and independently reviewed account lifecycle.

## Completion criteria

The first deliverable must be a runnable, tested open source foundation with a real encrypted client/server path and buildable web/desktop/extension packages. Platform source code and release automation must clearly state whether their platform tools, privileges, devices, signing credentials, and external infrastructure were available. A checklist item is complete only after its code or document exists and its relevant available checks pass. Unavailable hardware, public deployment, security audits, and signing cannot be represented as completed.

### Alpha.2 repair acceptance
- [x] Managed foreground SSH, pinned same-account authentication, reconnect backoff clearing and owned child cleanup.
- [x] Actual external browser exit IP and HTTPS over the personal server; six disconnect/reselect cycles and owned SSH recovery.
- [x] Exact website logo in native dashboard and packaged Dock icon; shared button hit geometry verified.
- [x] Nonblocking storage authorization with explicit own-Keychain permission action, preserving existing account.
- [x] Batched reversible macOS proxy source, competing-proxy priority handling, isolated preferences framework checks.
- [x] Current native app startup and actual macOS System proxy browser IP after user authorization; three concurrent browser pairs, six reconnects and owned SSH loss/recovery pass.
- [ ] Administrator utun GUI integration, isolated native route/resolver and failure-recovery qualification.
- [ ] Build and publish distinct alpha.2 installers/packages from the final verified source.
