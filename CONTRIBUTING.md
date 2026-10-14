# Contributing

Start with `docs/PLAN.md`, `docs/PROTOCOL.md`, and `docs/THREAT_MODEL.md`. Changes should have a clear user-visible purpose and preserve consent for internet exits, explicit peer authorization, and local-first account data.

Use Go for the shared core, Kotlin for Android, Swift for macOS, C# for Windows, and TypeScript for the web app/extension. Keep private keys, API tokens, state directories, and signing credentials out of Git, screenshots, fixtures, and logs.

Run the checks relevant to the change; `scripts/check.sh` aggregates Go vet/race tests, JS tests/builds, core cross compilation, and macOS tests/builds on macOS. Android and Windows have their own native build scripts. Add meaningful behavioral tests for network, parsing, permissions, lifecycle, or synchronization changes.

Prefer small focused changes and explain the trigger, changed behavior, checks, and limitations. No custom cryptography, inferred consent, fake connected statuses, or undocumented system route changes. App UX should make the common connection path simple and place peer IDs and routing details under Advanced.

The source/release repository is github.com/user-github-me/meshway. Package registries and native signing teams require independently configured owner access.
