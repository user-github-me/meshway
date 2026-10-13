# Meshway

An open source peer network for encrypted SOCKS5 connections, private TCP service tunnels, volunteer relays, and experimental point-to-point IP VPN links. Go powers the shared core; Kotlin, Swift, C#, and TypeScript power the platform interfaces.

Meshway is an early development release. No public server network, npm package, signed app store release, or hosted website has been published. See [the implementation roadmap](docs/PLAN.md) and [verified status](docs/STATUS.md) for what is implemented and what needs more work.

## Run from source

Requirements: Go 1.26+, Node 22+ for the web app. The build script also detects a project-local Go installation under `.tools/go`.

```sh
./scripts/build.sh
./dist/meshway init --config .meshway/config.json
./dist/meshway daemon --config .meshway/config.json
```

In another terminal:

```sh
npm ci
npm run dev
```

Open `http://localhost:5173/#/dashboard`. Pair with the API token from your local config file; the dashboard keeps the token only in memory. The default proxy listens at `127.0.0.1:1080`. A new network has no servers until you add a peer invitation or bootstrap server. Meshway does not invent public endpoints or claim that a disconnected proxy is an active VPN.

## Connect two machines

Read [the setup guide](docs/QUICKSTART.md). Each machine has its own identity and configuration. A gateway operator chooses whether to offer internet access and which peer IDs may use it. A private service gateway publishes only explicitly named services. Relays forward encrypted peer traffic and do not automatically act as internet exits.

## Private-key accounts

No email or hosted login database. A random account recovery key authorizes distinct device identities. History remains local unless both devices enable authenticated, encrypted sync. A recovery backup is the account: protect it and store a copy outside the device.

```sh
./dist/meshway account create --config .meshway/config.json
./dist/meshway account export --config .meshway/config.json --output /private/path/recovery.txt
./dist/meshway account devices --config .meshway/config.json
```

Create/export/import use private files and must run while the daemon is stopped. The native apps offer file-picker backup/import without terminal commands. [Account design](docs/ACCOUNTS.md) explains device membership, sync, and revocation limits. Root keys are never exposed by the dashboard API.

## Platforms and installation

- macOS: native SwiftUI client, bundled Go daemon; `./apps/macos/build.sh` after building the core.
- Windows: C#/WPF client; source/build scripts under `apps/windows`.
- Linux: native Go daemon, authenticated web dashboard, optional Linux TUN interface.
- Android: Kotlin client with a Go mobile binding and `VpnService`; `apps/android/build.sh`.
- Browser: TypeScript proxy extension for Chromium and Firefox; requires a running local SOCKS listener.
- Server: [container and systemd instructions](docs/DEPLOYMENT.md).
- npm: local launcher under `packages/cli`; supports verified release downloads after release publication.

[Platform details](docs/PLATFORMS.md) distinguish compilation, signing, and actual device testing. Native macOS/Windows full-device packet VPN, iOS, kill switch, DNS leak protection, and automatic public discovery remain release gates.

## Development

```sh
./scripts/check.sh
./scripts/release.sh
```

Checks cover encrypted connections, relay transport, permission denials, recovery accounts, encrypted sync, packet framing, app interfaces, and builds. Release scripts only create local artifacts. [Contributing](CONTRIBUTING.md), [security policy](SECURITY.md), and [threat model](docs/THREAT_MODEL.md) describe project expectations.

Licensed under [Apache-2.0](LICENSE).
