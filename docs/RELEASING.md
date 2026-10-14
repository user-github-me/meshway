# Release process

The initial source destination authorized by the owner is `github.com/user-github-me/meshway`. This does not establish ownership of an npm scope, browser store listing, Apple team, Windows certificate, domain, or permanent hosted relay. Those destinations must be configured explicitly.

## Publisher signatures and updates

Meshway has an Ed25519 publisher public key in `config/release-public.pem`, also shipped in `packages/cli/release-public.pem`. The initial private key is in the ignored `.tools/release/publisher-private.pem` with mode 0600. Back it up securely outside this checkout before deleting development state. Never upload it, put it in an artifact directory, print it, or use an account recovery key as a release signing key. A public release signature establishes publisher identity; it does not replace OS code signing or an independent security review.

`scripts/release-tool.mjs` signs every file in an artifact directory in a detached `RELEASE.json.sig`. Signed metadata includes the version, a monotonically increasing sequence, creation/expiry timestamps, and SHA-256 of each file. Verification rejects tampering, extra unsigned files, malformed filenames, incorrect keys, expired new metadata, and unsafe key permissions.

```sh
./scripts/release.sh
python3 scripts/package-linux.py --binary dist/bin/meshway-linux-amd64 --version 0.1.0 --arch amd64
npm pack --workspace @meshway/cli --pack-destination release
# Add all final platform packages before signing. Sign again after any change.
node scripts/release-tool.mjs sign release .tools/release/publisher-private.pem 0.1.0-alpha.1 1 2027-01-12T00:00:00Z
node scripts/release-tool.mjs verify release config/release-public.pem
```

The npm installer ships the project key pin. A self-hosted fork can supply `--public-key /path/to/independently-verified.pem`. Reusing a cache rejects earlier sequences, conflicting metadata at one sequence, and changes to its publisher key. Cached execution checks the signature and binary on every launch and allows already installed metadata to expire for offline use. An update is an explicit install of a newer signed release; stop the owned daemon, install, and restart. No background updater changes a running VPN or routes.

```sh
meshway install-core --release-base https://github.com/user-github-me/meshway/releases/download/v0.1.0-alpha.1/ --version 0.1.0-alpha.1
```

The trusted npm package/source and locally protected public-key pin are part of the trust boundary. This does not defend against an attacker who can replace the entire local installation, pin, and rollback record. Publisher key rotation requires a separately verified new pin and fresh cache; no downloaded release can silently rotate trust.

## Platform artifacts

`scripts/package-linux.py` creates real Debian packages without requiring `dpkg` or changing the host. `scripts/package-rpm.sh BINARY VERSION x86_64|aarch64` uses an available Linux `rpmbuild`; actual aarch64 and x86_64 RPM creation/metadata checks passed in an isolated Alpine container with `rpm file binutils findutils grep coreutils` installed from signed repositories. Choose a matching image architecture (`docker run --platform linux/amd64` for x86_64); aarch64 rpmbuild cannot build x86_64 packages merely by changing its target. Both ship a hardened systemd unit, create a dedicated account/private state on installation, and leave startup explicit. The generic `deploy/install-systemd.sh` also requires explicit root invocation on Linux. No development-host service is installed by packaging commands.

Container source builds accept `GO_IMAGE`, `RUNTIME_IMAGE`, `GOPROXY`, and `GOSUMDB`. Select verified image digests or trusted mirrors when the default registry is unavailable. The build rejects `GOSUMDB=off`; do not disable Go checksum or package signature verification to work around networking. Actual full source compilation passed locally using a cached trusted Alpine image and the official Go 1.27.2 Linux ARM64 archive with its independently checked SHA-256, preserving Go module checksum verification. `deploy/Dockerfile.prebuilt` packages a locally verified static Linux core with a trusted runtime image.

Windows archives must include the GUI `Meshway.exe`, distinct `meshway-daemon.exe`, `meshway-vpn-helper.exe`, pinned upstream `wintun.dll`, and `WINTUN-LICENSE.txt`. `scripts/sign-windows.ps1` requires Windows, an explicit certificate thumbprint, and HTTPS timestamp URL. It verifies and preserves valid upstream signatures, signs remaining artifacts, and verifies each signature. Microsoft driver certification and a signing certificate are owner-controlled release requirements.

For the packet VPN Apple app, run `apps/apple/build.sh macos|ios` to create the native project/framework, then `scripts/archive-apple.sh macos|ios TEAM_ID SIGNING_IDENTITY` with an explicit developer team and installed profiles. Xcode expands the real account prefix in both Network Extension/keychain entitlements and bundle configuration. Do not distribute a manually signed unsigned packet extension containing unresolved account prefixes. Generic `scripts/sign-macos.sh` can sign the simple desktop bundle; Network Extension bundles require explicit app/provider entitlements. `scripts/notarize-macos.sh` takes explicit App Store Connect key-file, key ID, and issuer environment variables; it never enumerates Keychain identities or reads this machine's Azure keys.

Android Gradle reads `MESHWAY_ANDROID_KEYSTORE`, `MESHWAY_ANDROID_STORE_PASSWORD`, `MESHWAY_ANDROID_KEY_ALIAS`, and `MESHWAY_ANDROID_KEY_PASSWORD` only when a release store is supplied. After rebuilding the Go AAR, run `apps/android/gradlew assembleRelease` from its directory and verify with `apksigner verify --verbose`. An unsigned release build and debug-signed APK are development artifacts. No generated keystore is committed.

## GitHub and registries

`scripts/publish-release.sh OWNER/REPO TAG ARTIFACT_DIR PUBLIC_KEY` verifies every artifact before uploading a draft using an existing tag and `docs/RELEASE_NOTES.md`. `.github/workflows/release.yml` provides the same workflow with an explicit repository, tag, sequence and expiry. Configure the `release` environment with protected approvals, the private PEM secret, and independent public PEM variable before running. Its token can publish only to the workflow repository. A remote CI workflow is unverified until actually run there.

Review the draft and release notes, then publish to the owner's supplied destination. npm `pack` is local; npm `publish` and container push require the owner's actual permitted namespace/registry and credentials. Website deployment requires a real destination and DNS. Native app stores require the owner's signing teams and listings; generated test credentials cannot satisfy those requirements.

The owner-authorized GitHub Pages destination is `https://user-github-me.github.io/meshway/`; the repository has Pages configured for Actions. `.github/workflows/pages.yml` builds Vite with `/meshway/`, uploads the static artifact, and deploys on main push. Actual deployment run `38492048133` succeeded on 2026-10-14; a real-browser check of the public site passed with correct assets/dashboard navigation and zero failed resources.
