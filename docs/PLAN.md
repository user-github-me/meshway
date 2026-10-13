# Meshway implementation plan

Meshway is an Apache-2.0 open source peer network with a shared Go networking core, desktop and Android clients, a browser proxy extension, a local dashboard, a public website, and installable server packages. The name and GitHub organization are provisional; no repository or public service has been published.

## Product decisions

- A user can run a client, a private service gateway, a relay, an internet exit, or several of these roles on the same machine.
- Peers use libp2p authenticated encrypted streams. Peer IDs identify public keys; direct connections are preferred. Circuit relay and hole punching use the established libp2p implementations.
- Joining is by explicit peer multiaddress or configured bootstrap peers. No centrally hosted discovery service is assumed. Running an initial relay/bootstrap on a VPS remains necessary for networks behind restrictive NAT.
- Relaying encrypted traffic and forwarding internet traffic are separate capabilities. Internet exits and private/LAN targets need explicit opt-in. Default operation never publishes an open internet proxy.
- SOCKS5 and TCP tunnels operate without system VPN privileges. IP VPN mode needs an OS TUN interface or a mobile VPN API, routes, and explicit setup. Browser extensions configure a browser proxy; they cannot supply a device-wide VPN.
- Native SwiftUI on macOS, C#/WPF on Windows, and Kotlin on Android use the shared Go daemon or gomobile binding. Linux uses the CLI and local web dashboard. Linux provides the initial TUN implementation. macOS and Windows IP VPN drivers and iOS entitlements are separate release gates.
- No custom encryption or anonymity claims. Relay/exit metadata, exit visibility, network abuse, traffic limits, and platform privileges are documented. Public internet exit operation is experimental pending security review.
- Accounts are generated Ed25519 recovery keys without email or a hosted account database. A separate device peer key is authorized by the account root, avoiding conflicting peer IDs when one account is used on multiple devices. History is local; encrypted, authenticated sync is optional. Device visibility means paired/synced devices with last-seen metadata, not an omniscient live global directory. Revoking a device cannot neutralize a stolen account recovery key; account key rotation is a later gate.

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
- [x] VPN route/setup documentation and explicit no-DNS/no-kill-switch limitations.
- [x] Small mobile binding API and lifecycle support.
- [x] Native Android client source and reproducible APK build scripts.
- [ ] Native macOS/Windows packet VPN integration (later release gate).
- [ ] iOS app and Network Extension entitlement (later release gate).

### 3 — User-facing apps
- [x] Real opt-in server catalog, list/map, one-click select/disconnect, and advanced settings.
- [x] Personal VPS mode uses same-account certificates without public allowlists.
- [x] Public responsive website with truthful capability matrix and install guide.
- [x] Local dashboard using live authenticated API.
- [x] Native desktop lifecycle implementations and packaging; macOS runtime exercised, Windows runtime pending.
- [x] Browser extension local proxy pairing, enable/disable, and cleanup.
- [x] Website/dashboard production build and extension package verification.

### 4 — Distribution
- [x] npm launcher/installer with checksum verification and supported-platform mapping.
- [x] Multi-stage container source and validated Compose; prebuilt non-root runtime tested (standard registry download unavailable).
- [x] Linux systemd example and setup script (syntax checked; deployment untested).
- [x] Cross-platform binary build and release archive script.
- [x] CI authored; local tests/race/JS/cross compilation/audits pass; remote CI awaits publication.
- [ ] Signing and notarization instructions; GitHub release publication (requires actual owner/repository).

### 5 — Harden and verify
- [x] Test auth, origin restrictions, private target policy, stream limits, malformed SOCKS traffic, and shutdown.
- [x] Run test → fix → retest loops until local checks pass.
- [x] Record exact tested platforms and features in `docs/STATUS.md`.
- [x] Android emulator real TUN and Linux privileged namespace TUN tests.
- [ ] Different-network/machine NAT/relay testing and physical Android VPN verification.
- [ ] Independent security review, bandwidth accounting/limits, abuse handling, exit trust/reputation, automatic discovery, updates, DNS leak protection, kill switch, IPv6, UDP proxy, and performance tests before public beta.

### 6 — Private-key accounts and local-first data
- [x] Generate account recovery key and signed per-device membership certificates.
- [x] Recovery backup/import using private files; never log recovery secrets.
- [x] Local bounded connection/event history and device registry.
- [x] Optional encrypted peer-to-peer history/device sync authenticated by root signatures and transport identity.
- [x] CLI and authenticated local API for account status, devices, history, and explicit sync.
- [x] Test wrong-account rejection, tampered device/event rejection, replay deduplication, recovery import, and offline storage.
- [ ] Key rotation, keychain-backed master key protection, synchronization conflict UI, automatic discovery, full app onboarding, and cross-device revocation propagation before a mature account release.

## Completion criteria

The first deliverable must be a runnable, tested open source foundation with a real encrypted client/server path and buildable web/desktop/extension packages. Platform source code and release automation must clearly state whether their platform tools, privileges, devices, signing credentials, and external infrastructure were available. A checklist item is complete only after its code or document exists and its relevant available checks pass. Unavailable hardware, public deployment, security audits, and signing cannot be represented as completed.
