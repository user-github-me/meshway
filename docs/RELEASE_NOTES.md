Meshway 0.1.0-alpha.1 is an experimental Apache-2.0 peer VPN, SOCKS and tunneling implementation.

Download native apps, six platform cores, Debian/RPM installers, browser extensions, the npm-compatible CLI tarball and portable container images below. The public multiarch image is `ghcr.io/user-github-me/meshway:0.1.0-alpha.1`; the website is https://user-github-me.github.io/meshway/.

Use the capability and verification matrix in docs/STATUS.md before installing. Internet exits require explicit consent; personal servers use same-account device certificates. One public Azure encrypted relay is live with invitations in config/public-bootstrap.json. It assists peer connections and does not offer internet exit access.

This alpha includes SOCKS5 TCP/UDP, IPv4/IPv6 VPN and tunneled DNS, local-first recovery-key accounts with encrypted optional sync, device revocation/history deletion, clickable server list/map, personal VPS access, protected native state and reversible Linux routing/DNS/firewall setup. Real Azure direct/relayed HTTPS, dual-NAT loss/recovery, Android emulator VPN, Windows signed Wintun kernel TCP, Linux crash/rollback, Debian/RPM install/remove and all eight platform CI jobs passed. Third-party license notices are included.

Verify RELEASE.json.sig using a publisher Ed25519 public key acquired independently of this download, then check each artifact hash. OS code signatures are separate from the release manifest signature. This release must not be described as independently audited unless an external review report is attached.

The npm launcher carries the project public-key pin and rejects tampered, expired, downgraded or conflicting release metadata. Install it with `npm install -g https://github.com/user-github-me/meshway/releases/download/v0.1.0-alpha.1/meshway-cli-0.1.0.tgz`, then run `meshway install-core`. Relays, personal servers and exits require actual reachable peer invitations; no fabricated public catalog is included.

Development macOS/Windows app artifacts are unsigned by an owner certificate. Android development APKs use a debug signing key. The upstream Wintun library retains its verified vendor signature. Packet VPN source, native helpers and actual lab results are included, but successful compilation does not certify physical-device VPN operation or app-store eligibility. Consult the current verification matrix before enabling a full-device VPN.
