# Threat model

## Assets and trust

The account recovery key grants account ownership; each device has a separate libp2p private key. The local API token grants control of the running daemon. Local history reveals limited peer connection metadata. Exit operators expose their public IP to destination services, and can observe traffic after it leaves the encrypted peer stream. Use destination TLS for application data.

libp2p establishes authenticated encrypted transport using peer keys and mature Noise/TLS transports. This protects stream contents from relays and ordinary network observers. It does not hide peer IPs from direct peers, traffic volume/timing, or destinations from an exit. Meshway is not an anonymity network.

## Enforced boundaries

- Explicit allowlists authorize inbound TCP gateways and IP VPN peers.
- Internet exits are disabled by default; named private services are separate from general internet exits.
- Private/reserved destination policy resolves DNS once and dials the validated address, preventing a second DNS resolution from rebinding the target.
- Loopback-only SOCKS/API listeners, bearer auth, allowed browser origins, and Host checks limit remote and cross-site control.
- Bounded headers, packets, stream slots, deadlines, and relay resource limits limit straightforward memory/socket exhaustion.
- OS state locks prevent concurrent daemons or recovery imports mutating one device identity.
- Account device certificates are signed by the root key and bound to the authenticated transport peer. Events are device-signed. Optional history sync uses AES-256-GCM in addition to encrypted libp2p transport; recovery secrets are never sent.
- Recovery export writes a new private file; APIs never return the recovery secret. Unix state files enforce private permissions. Windows file ACL protection still needs dedicated hardening before public release.

## Known limitations

An authorized exit user can still abuse their permitted access. Destination/port allowlists, accounting, reputation, and abuse responses need development. Relays and peer discovery can reveal metadata. There is no global public directory or independent live device presence oracle. Server labels and countries are operator claims, not validated geolocation.

Recovery keys are initially stored in local private files, not native Keychain/Keystore/DPAPI. Any device importing the root key can sign a new device certificate. A revoked device still holding that key can re-enroll; proper compromise recovery needs key rotation or hardware-backed/non-root delegated enrollment. Device revocation affects account sync and does not automatically edit network peer allowlists. Sync is manual, bounded, and has no guaranteed offline revocation delivery.

Connection history sync exposes data to other holders of the same recovery key. No central database contains that history. Local deletion propagation, retention choices, conflict handling, and protected-at-rest account storage need a mature design.

IP transport currently implements a point-to-point packet link, with one device and one stream per endpoint. Routes, NAT, and DNS remain explicit. There is no automatic whole-device internet VPN, kill switch, leak-proof reconnect, or native macOS/Windows VPN driver integration. Linux privileged TUN and Android emulator kernel round-trip tests have passed; full routing/reconnect/privacy tests and physical devices remain required.

Unsigned native builds, unpublished artifacts, a provisional repository namespace, and same-release checksums provide no authenticated update channel. Production distribution requires signatures, app signing/notarization, pinned release identities, reproducible builds, and update verification.
