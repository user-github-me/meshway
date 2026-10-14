# Security policy

Meshway is experimental and has not undergone independent security review. It currently supplies encrypted peer transport, not a guarantee of anonymity, comprehensive DNS leak protection, or a production kill switch.

Do not post recovery keys, device keys, API tokens, or live sensitive traffic in issues. Use [GitHub private vulnerability reporting](https://github.com/user-github-me/meshway/security/advisories/new) on the actual repository. The maintainer is [@user-github-me](https://github.com/user-github-me). Never disclose a vulnerability in a public issue before a coordinated fix.

Report a vulnerability privately to the actual maintainer using an established private channel, with a minimal sanitized reproduction, affected version, and impact. Independent review of the peer protocol, exit policy, account model, sync cryptography, app lifecycle, and distribution chain is a public-beta release gate.

The local API requires bearer authentication and explicit allowed browser origins. SOCKS and API listeners bind loopback. Exits require opt-in and allowed peer IDs. Private/reserved targets are blocked unless explicitly enabled or published as named services. Dependencies must pass `govulncheck` and the relevant package audits before release.
