Meshway 0.1.0-alpha.1 is an experimental Apache-2.0 peer VPN, SOCKS and tunneling implementation.

Use the capability and verification matrix in docs/STATUS.md before installing. Internet exits require explicit consent; personal servers use same-account device certificates. The public server directory starts empty until operators provide reachable peers.

Verify RELEASE.json.sig using a publisher Ed25519 public key acquired independently of this download, then check each artifact hash. OS code signatures are separate from the release manifest signature. This release must not be described as independently audited unless an external review report is attached.

The npm launcher carries the project public-key pin and rejects tampered, expired, downgraded or conflicting release metadata. Relays, personal servers and exits require actual reachable peer invitations; no fabricated public catalog is included.

Development macOS/Windows app artifacts are unsigned by an owner certificate. Android development APKs use a debug signing key. The upstream Wintun library retains its verified vendor signature. Packet VPN source, native helpers and actual lab results are included, but successful compilation does not certify physical-device VPN operation or app-store eligibility. Consult the current verification matrix before enabling a full-device VPN.
