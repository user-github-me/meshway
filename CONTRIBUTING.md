# Contributing

Anyone is welcome to contribute. Fork the repository, create a focused branch,
and open a pull request against `main`. Read [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
Contributions are licensed under Apache-2.0; submit only work you may legally
contribute. Discuss large protocol or architecture changes in a feature issue
before implementing them. No separate contributor license agreement is required.

Start with `docs/PLAN.md`, `docs/PROTOCOL.md`, and `docs/THREAT_MODEL.md`. Changes should have a clear user-visible purpose and preserve consent for internet exits, explicit peer authorization, and local-first account data.

Use Go for the shared core, Kotlin for Android, Swift for macOS/iOS, C# for Windows, and TypeScript for the web app/extension. Keep private keys, API tokens, state directories, and signing credentials out of Git, screenshots, fixtures, and logs.

Run the checks relevant to the change; `scripts/check.sh` aggregates Go vet/race tests, JS tests/builds, core cross compilation, and macOS tests/builds on macOS. Android and Windows have their own native build scripts. Add meaningful behavioral tests for network, parsing, permissions, lifecycle, or synchronization changes.

Prefer small focused changes and explain the trigger, changed behavior, checks, and limitations. No custom cryptography, inferred consent, fake connected statuses, or undocumented system route changes. App UX should make the common connection path simple and place peer IDs and routing details under Advanced.

Pull requests must pass Go formatting, vet/race tests, vulnerability scanning,
web/extension/package tests and builds, real browser integration, macOS checks,
Apple packet-provider builds, Android builds, Windows build/runtime checks and
isolated Linux privileged VPN checks. Dependency review rejects new
high-severity vulnerable dependencies. Reviewers may require isolated behavioral
tests for routing, DNS, revocation, tunnel loss, reconnection or rollback changes.
Do not change the development machine's default routes, DNS, active VPN or proxy.

`main` requires a maintainer/code-owner approval and passing checks. New commits
dismiss stale approvals; resolve review conversations before merging. Force
pushes and branch deletion are blocked. Maintainers control release tags,
publisher keys, deployments and registry credentials. Public fork pull requests
run without release credentials; CI does not use `pull_request_target` to run
contributor code. A passing build is not an independent security audit.

The sole repository administrator retains emergency bypass for maintenance and
release recovery. Ordinary contributions must use pull requests and the checks
above. The versioned policy is in `config/github-branch-protection.json`; the
owner can reapply it with `scripts/configure-github.sh`. Release tags are
protected against changes or deletion by non-administrators.

Use the pull-request template, include user-visible compatibility changes and
reproduction commands, and update relevant documentation. Maintainers prefer
squash merges and delete merged branches. Commits should use a concise title
that describes the resulting behavior. Generated binaries and real state stay
out of Git; release assets are published separately with signed metadata.

The source/release repository is github.com/user-github-me/meshway. Package registries and native signing teams require independently configured owner access.
