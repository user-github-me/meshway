# Meshway development

Read `docs/PLAN.md`, `docs/PROTOCOL.md`, and `docs/STATUS.md` before changes. Use Go for the shared networking core, Kotlin for Android, Swift for macOS, C# for Windows, and TypeScript for web/browser work.

Never read, print, or export Azure API keys or search for them. They are managed by this machine's macOS Keychain. Do not print Meshway recovery keys, device keys, or API tokens. Keep generated state and signing credentials out of Git.

Never alter the development host's default routes, DNS, active VPN, or system proxy while testing. Use temporary loopback peers, isolated browser profiles, disposable Docker network namespaces, or Android emulators. Preserve previous browser proxy settings and stop owned test daemons on cleanup. External HTTP tests are opt-in via `scripts/internet-smoke.py`.

Internet exits require explicit consent and peer authorization. Personal servers authenticate same-account root-signed device certificates. No fake server catalog, geolocation, connection activity, or claims of universal NAT reliability. Advanced technical controls belong behind simple default user flows.

Run relevant checks. `scripts/check.sh` aggregates Go vet/race, JS tests/typecheck/build, cross compilation, and macOS build/tests. Browser integration uses `npm run test:browser --workspace @meshway/web`. Android/Windows have their own build scripts; opt-in TUN tests require isolated environments. Mark roadmap items complete only with evidence; distinguish compilation from device testing and signing.

On case-insensitive filesystems, the Windows GUI `Meshway.exe` and core `meshway.exe` collide. Packaged Windows cores must be named `meshway-daemon.exe`.

No publishing or deployment is implied by local build commands. Repository and package namespace are provisional until the owner supplies a real release destination.
