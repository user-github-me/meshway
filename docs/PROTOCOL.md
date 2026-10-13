# Meshway core and control protocol

The provisional Go module is `github.com/meshway/meshway`. Nothing is published to that organization. Build with `go build -o bin/meshway ./cmd/meshway`. Use `meshway init --config meshway.json`, `meshway identity --config meshway.json`, `meshway daemon --config meshway.json`, `meshway status --config meshway.json`, and `meshway version`. Init creates a random API token and an Ed25519 identity with private filesystem permissions. Identity is stable until `state_dir/identity.key` is removed. Keep configuration and identity private; never commit real tokens or identities.

## Configuration

Configuration is strict JSON: unknown fields fail validation. Examples under `config/` contain placeholder tokens; run init to obtain your own token.

| Field | Meaning |
| --- | --- |
| `state_dir` | Identity directory, absolute path recommended |
| `listen_addresses` | libp2p TCP/QUIC multiaddresses, default port 4001 |
| `bootstrap_peers` | Explicit peer multiaddresses; also candidates for AutoRelay reservations |
| `upstream_peer` | Peer ID used by SOCKS and forwards without their own peer |
| `socks_listen` | Explicit loopback IP:port, default 127.0.0.1:1080; empty disables |
| `api_listen` | Explicit loopback IP:port, default 127.0.0.1:7777; empty disables |
| `api_token` | Bearer secret, at least 32 characters; init generates 256 random bits |
| `allowed_origins` | Exact browser origins allowed to call API |
| `relay` | Host libp2p circuit-v2 encrypted relays; default false |
| `exit` | Allow approved peers to request arbitrary public TCP destinations; default false |
| `allow_private_targets` | Also permit private/reserved addresses for exit requests; default false |
| `allowed_peers` | Peer IDs permitted to open inbound tunnels; empty denies every peer |
| `services` | Explicit gateway services `[{"name":"web","target":"127.0.0.1:8080"}]` |
| `forwards` | Local listeners `[{"name":"web","listen":"127.0.0.1:8081","service":"web","peer":"PEER_ID"}]` or a `target` instead of `service` |
| `vpn` | Optional `{ "enabled": true, "mtu": 1280, "device": "meshway0", "peer": "PEER_ID" }`; device is Linux only, empty device accepts external FD |
| `account_sync` | Allow same-account device history/registry sync; default false |
| `device_label` | Human-readable device name, maximum 80 characters |
| `advertise` | Publish server capabilities to directly queried known peers; default false |
| `server_label`, `server_country` | Optional name and two-letter operator-supplied country; no geolocation inference |
| `personal_server` | Require same-account signed device authentication for exit/services/VPN and private advertisements; default false |

An explicit service target may be private because the server operator selected that exact target. This does not enable arbitrary private target dialing. Every service request still requires the caller in `allowed_peers`. A generic target request requires `exit:true`. SOCKS is local and trusts processes on the same computer; its network listener cannot be exposed remotely.

## Encrypted peer TCP protocol

libp2p authenticates Peer IDs with Ed25519 keys and negotiates its standard encrypted transports. Custom cryptography is not implemented. `/meshway/tcp/1.0.0` carries a four-byte big-endian JSON length followed by JSON. Header size must be 1..4096 bytes. Client request is exactly one of `{"target":"example.org:443"}` or `{"service":"web"}`. Server responds `{"ok":true}` or `{"ok":false,"error":"reason"}`. Success switches to raw bidirectional TCP bytes, with half-close support. A 10-second handshake timeout and 2-minute rolling idle timeout bound stalled connections. At most 128 local sessions/inbound gateway streams run simultaneously.

Destinations are resolved at the exit, then checked and dialed using those exact resolved IP addresses to avoid a second DNS lookup. Any private/reserved result denies the request unless explicitly permitted. IPv4/IPv6 attempts are interleaved with a 250-ms stagger so an unreachable family does not exhaust the full deadline; successful dialing cancels and closes losing attempts. CONNECT supports IPv4, IPv6, and domain names. SOCKS BIND and UDP ASSOCIATE are unsupported. DNS therefore uses the exit resolver for SOCKS domain requests; this does not promise system-wide DNS leak protection.

Direct peers are preferred. AutoRelay and hole punching are enabled when explicit bootstrap candidates exist. Configured bootstrap peers receive a bounded connection attempt at startup and every 30 seconds when disconnected. Circuit-v2 relay hosting is bounded to 128 reservations and 16 simultaneous circuits with per-IP/peer limits; relayed circuits have a 2-minute/64-MiB limit. Relay operators forward encrypted traffic, while approved exit operators can observe unencrypted destination traffic. Neither is an anonymity guarantee. Production NAT traversal still needs a reachable bootstrap/relay and multi-machine testing.

## Loopback API

All substantive API calls require `Authorization: Bearer TOKEN`. Allowed Origin values are exact; an absent Origin supports native clients. Host must be a loopback IP or localhost to mitigate DNS rebinding. OPTIONS is unauthenticated for CORS preflight only. Responses have `Cache-Control: no-store`. Errors are JSON `{"error":"message"}`.

| Method/path | Input | Output |
| --- | --- | --- |
| `GET /v1/status` | none | Status below |
| `GET /v1/peers` | none | `{"peers":[{"id":"...","addresses":["..."]}]}` |
| `POST /v1/connect` | `{"address":"/ip4/1.2.3.4/tcp/4001/p2p/PEER_ID"}` | `{"id":"...","addresses":["..."]}` |
| `POST /v1/shutdown` | none | `{"ok":true}`; daemon closes shortly after response |
| `GET /v1/servers` | none | `{"servers":[{"peer_id":"...","label":"...","country":"DE","address":"...","addresses":[],"roles":{"relay":false,"exit":true},"available":true,"can_connect":true,"last_seen":"..."}]}` |
| `POST /v1/select` | `{"peer_id":"...","address":"..."}` | `{"ok":true,"upstream_peer":"..."}` |
| `POST /v1/disconnect` | none | `{"ok":true,"upstream_peer":""}`; closes outgoing TCP proxy streams |
| `GET /v1/account/status` | none | Account ID, device ID, creation time, sync enabled |
| `GET /v1/account/devices` | none | `{"devices":[...]}` signed account device certificates |
| `GET /v1/account/history` | none | `{"events":[...]}` bounded signed local history |
| `POST /v1/account/sync` | `{"peer_id":"..."}` | `{"ok":true}` after encrypted same-account sync |
| `POST /v1/account/revoke` | `{"peer_id":"..."}` | `{"ok":true}`; account sync revocation, explicit network allowlist is separate |

Status fields: `version`, `peer_id`, `addresses` (full dialable multiaddresses with Peer ID), `peers`, `roles:{relay,exit}`, `socks_listen`, `api_listen`, `upstream_peer`, `upstream_connected` (live direct/relay transport to selected peer), `started_at` (RFC3339 UTC), `bytes_sent`, `bytes_received`. Selection may remain when a server is offline; `upstream_connected` then becomes false. Subsequent proxy requests can redial the known address after the server restarts with the same identity. Counters measure live TCP stream payload, not all libp2p or relay overhead. Configuration edits require restarting the daemon; no API mutates credentials or permissions.

Server selection checks that the supplied address contains the selected Peer ID, dials that authenticated peer, and verifies its opt-in exit advertisement and caller approval. Selection closes existing outgoing TCP proxy streams; new SOCKS/unspecified forwards use the selected runtime peer. Disconnect clears that peer and closes outgoing TCP streams while independently enabled server/relay roles keep running. A pinned forward may reconnect to its explicit peer. Selection is not saved into configuration. The catalog contains queried configured/connected peers, so a new deployment has no public servers until operators provide reachable peers. Discovery does not provide anonymity, peer reputation, or geolocation verification.

`meshway setup-server --personal --config server.json` generates an explicit private same-account exit configuration and stable peer identity. Import the same recovery backup on the client and server while each daemon is stopped, then share the server's reachable multiaddress. A personal server verifies root-signed device certificates bound to the remote encrypted Peer ID using `/meshway/account-auth/1.0.0`; account history sync can remain disabled. Personal advertisements (`personal:true`) are returned only to authenticated devices in the same account. Personal mode ignores unrelated manual allowlist entries, so they cannot bypass account membership. Private/LAN exit targets remain blocked unless `allow_private_targets` is explicitly set, and root-key compromise compromises every personal server using that account. Known revoked devices are refused; revocation must reach each server to take effect.

Accounts create a local master recovery key separate from per-device libp2p identities. `meshway account create --config meshway.json` creates/enrolls the local device. `meshway account export --config meshway.json --output backup.txt` saves a new private backup file; `meshway account import --config meshway.json --input backup.txt [--force]` joins that account on another device while preserving its distinct peer key. Offline mutation and running nodes acquire a cross-platform kernel state lock. Recovery keys never appear in API responses or logs. `account status|devices|history` reads the running API; `account sync|revoke --peer PEER_ID` operates on the running account. See account documentation for sync protocol, recovery compromise and revocation limitations.

## Embedding

`pkg/meshway` exports gomobile-compatible `Start(configJSON string) (*Node,error)`, `DefaultConfig(stateDir string)(string,error)`, `LoadConfig(path string)(string,error)`, `Identity(stateDir string)(string,error)`, and `Version() string`. Node exposes `Status()(string,error)`, `Connect(address string) error`, and `Stop() error`. Stop is idempotent. Embedders must protect config tokens just like the CLI.

`Node.StartVPN(fd int, peerID string) error` attaches an external Android VPN/TUN descriptor when `vpn.enabled` is true. The core duplicates the descriptor and owns the duplicate; the caller retains the original. Empty peer ID waits for an inbound approved peer; nonempty peer ID dials that peer. Linux `vpn.device` opens a TUN directly. Exactly one device and one packet stream run at a time. See `docs/VPN.md` for setup, packet protocol, and current limitations. IP transport is point-to-point: this core does not set routes, Internet NAT, system DNS, or a kill switch automatically.

Binding account methods: `AccountStatus`, `Devices`, `History` return JSON; `SyncAccount(peerID)` and `RevokeDevice(peerID)` perform account actions. `ExportRecovery(stateDir,output)` and `ImportRecovery(stateDir,input,force)` acquire the offline state lock; stop the node first. `Servers()` returns a JSON server array; `SelectPeer(peerID,address)` selects an approved advertised exit.
