# Release process

The owner-authorized source/release destination is `github.com/user-github-me/meshway`, with GitHub Pages and `ghcr.io/user-github-me/meshway` distribution. The separately authorized persistent Azure encrypted relay is documented in `docs/PUBLIC_NETWORK.md`. npm namespace ownership, browser/app-store listings, Apple provisioning, Windows certificates and a custom domain require their respective owner credentials/configuration.

Use a separate artifact directory for a later release so the previous signed
release stays unchanged. For example, after freezing alpha2 source:

```sh
MESHWAY_DIST_DIR="$PWD/.tools/release/alpha2/dist" MESHWAY_RELEASE_DIR="$PWD/.tools/release/alpha2/artifacts" MESHWAY_RELEASE_VERSION=0.1.0-alpha.2 ./scripts/release.sh
```

`scripts/build.sh` accepts `MESHWAY_DIST_DIR`; `native-manifest.py --dist` reads
that same build tree. Android accepts `MESHWAY_ANDROID_OUTPUT_DIR` for its APK,
Windows `build.ps1` accepts `-Output` and `-AppVersion`, and Debian `--output`
and RPM `MESHWAY_RELEASE_DIR` keep packages in the selected staging directory.
Copy complete final native builds before generating metadata. Alpha2 uses
publisher sequence 2; it must never replace public sequence-1 metadata. After
publication, run `node scripts/validate-published-release.mjs 0.1.0-alpha.2 2`
to verify an actual public install with the pinned publisher key.

## Alpha.2

The alpha.2 macOS administrator helper passed actual public DNS/HTTPS/exit-IP and three cleanup-mode tests; its separate Apple packet extension still requires developer provisioning. The universal ad-hoc-signed Mac app enables that qualified helper. Freeze exact native bundles, installer hashes, tracked-only source archives and final-revision container exports before signing publisher sequence 2. Alpha.1 files and sequence 1 remain immutable. Expected alpha.2 inventory is 35 signed payloads plus the metadata/signature; iPhone binaries and the old ARM64-only Mac fallback are excluded. Repack both npm launchers after changing their default core pin to alpha.2. Compare all draft asset hashes and sizes before publishing. Validate anonymous GitHub/npm installations and cached-tamper rejection after publication. Published metadata supplies the authoritative inventory and expiry.

## Publisher signatures and updates

Meshway has an Ed25519 publisher public key in `config/release-public.pem`, also shipped in `packages/cli/release-public.pem`. The initial private key is in the ignored `.tools/release/publisher-private.pem` with mode 0600. Back it up securely outside this checkout before deleting development state. Never publish it, put it in an artifact directory, print it, or use an account recovery key as a release signing key. Authorized release automation stores it only as an encrypted GitHub environment secret, protected by owner review. A public release signature establishes publisher identity; it does not replace OS code signing or an independent security review.

`scripts/release-tool.mjs` signs every file in an artifact directory in a detached `RELEASE.json.sig`. Signed metadata includes the version, a monotonically increasing sequence, creation/expiry timestamps, and SHA-256 of each file. Verification rejects tampering, extra unsigned files, malformed filenames, incorrect keys, expired new metadata, and unsafe key permissions.

```sh
./scripts/release.sh
python3 scripts/package-linux.py --binary dist/bin/meshway-linux-amd64 --version 0.1.0-alpha.1 --arch amd64
python3 scripts/package-linux.py --binary dist/bin/meshway-linux-arm64 --version 0.1.0-alpha.1 --arch arm64
./scripts/package-linux-lab.sh TRUSTED_ALPINE_IMAGE dist/bin/meshway-linux-amd64 0.1.0 x86_64 0.alpha.1
./scripts/package-linux-lab.sh TRUSTED_ALPINE_IMAGE dist/bin/meshway-linux-arm64 0.1.0 aarch64 0.alpha.1
npm pack --workspace @meshway/cli --pack-destination release
# Add all final platform packages before signing. Sign again after any change.
python3 scripts/native-manifest.py --version 0.1.0-alpha.1
node scripts/release-tool.mjs sign release .tools/release/publisher-private.pem 0.1.0-alpha.1 1 2027-01-12T00:00:00Z
node scripts/release-tool.mjs verify release config/release-public.pem
```

The npm installer ships the project key pin. A self-hosted fork can supply `--public-key /path/to/independently-verified.pem`. Reusing a cache rejects earlier sequences, conflicting metadata at one sequence, and changes to its publisher key. Cached execution checks the signature and binary on every launch and allows already installed metadata to expire for offline use. An update is an explicit install of a newer signed release; stop the owned daemon, install, and restart. No background updater changes a running VPN or routes.

```sh
meshway install-core
```

The trusted npm package/source and locally protected public-key pin are part of the trust boundary. This does not defend against an attacker who can replace the entire local installation, pin, and rollback record. Publisher key rotation requires a separately verified new pin and fresh cache; no downloaded release can silently rotate trust.

## Platform artifacts

The initial release is `v0.1.0-alpha.1`, core version `0.1.0-alpha.1`, registry package version `0.1.0-alpha.1`, original GitHub CLI version `0.1.0`. It has 36 signed payload files plus `RELEASE.json` and `RELEASE.json.sig`: six raw cores, six core archives, two complete Windows portable bundles and two EXE installers, universal macOS ZIP/DMG and ARM64 fallback, Android development APK, two browser extension ZIPs, two npm CLI tarballs, two Debian packages, two RPMs, two portable container archives, native hash manifest, checksums, license and third-party notices. Two explicit `meshway-source-0.1.0-alpha.1` ZIP/tar.gz payloads include the final committed packaging/source snapshot; GitHub also supplies the immutable core tag archives. Sequence 1 expires for new installations on 2027-01-12. Changes after public publication require a new release sequence and version rather than replacing sequence-1 metadata.

`artifacts/release-artifact-validation.json` records archive-content checks against current raw cores/native binaries: all 16 included native executable hashes, complete Windows helper/driver bundles, universal macOS GUI/provider/daemon; deferred iOS developer artifacts remain buildable but are excluded from this release. Debian packages were checked to contain the exact current raw Linux cores. Packaging checks do not replace the device/runtime evidence in `docs/VALIDATION.md`.

`THIRD_PARTY_NOTICES.txt` preserves the pinned upstream Go, web, .NET desktop runtime and Wintun license texts. `scripts/third-party-notices.py` regenerates notices from the public dependency caches without reading credentials. Core/app/extension archives, Linux packages and container images include notices; the npm installer downloads signed license companions alongside its core. The main macOS app and embedded daemon are universal Intel/Apple Silicon binaries; the simple daemon-client fallback is explicitly named `Meshway-macos-arm64-daemon-client.zip`.

The owner deferred the iPhone/iPad binary release until an Apple Developer account is ready. iOS source, unsigned build checks and archive/export scripts remain public; no unsigned IPA is included in the initial downloads. Explicit local packaging with `MESHWAY_INCLUDE_IOS_ARTIFACTS=1 ./scripts/release.sh` can create `Meshway-ios-unsigned.ipa` with standard `Payload/Meshway.app` and embedded provider, but installation and VPN entitlements require the owner's Apple team/provisioning. Build/archive using `scripts/archive-apple.sh ios TEAM_ID SIGNING_IDENTITY`, then `scripts/export-ios.sh ARCHIVE OWNER_EXPORT_OPTIONS_PLIST`. A publisher signature cannot make an unsigned IPA installable.

`scripts/package-linux.py` creates real Debian packages without requiring `dpkg` or changing the host. `scripts/package-rpm.sh BINARY VERSION x86_64|aarch64` uses an available Linux `rpmbuild`; actual aarch64 and x86_64 RPM creation/metadata checks passed in an isolated Alpine container with `rpm file binutils findutils grep coreutils` installed from signed repositories. Choose a matching image architecture (`docker run --platform linux/amd64` for x86_64); aarch64 rpmbuild cannot build x86_64 packages merely by changing its target. Both ship a hardened systemd unit, create a dedicated account/private state on installation, and leave startup explicit. The generic `deploy/install-systemd.sh` also requires explicit root invocation on Linux. No development-host service is installed by packaging commands.

Container source builds accept `GO_IMAGE`, `RUNTIME_IMAGE`, `GOPROXY`, and `GOSUMDB`. Select verified image digests or trusted mirrors when the default registry is unavailable. The build rejects `GOSUMDB=off`; do not disable Go checksum or package signature verification to work around networking. Actual full source compilation passed locally using a cached trusted Alpine image and the official Go 1.27.2 Linux ARM64 archive with its independently checked SHA-256, preserving Go module checksum verification. `deploy/Dockerfile.prebuilt` packages a locally verified static Linux core with a trusted runtime image.

Windows archives must include the GUI `Meshway.exe`, distinct `meshway-daemon.exe`, `meshway-vpn-helper.exe`, pinned upstream `wintun.dll`, and `WINTUN-LICENSE.txt`. `scripts/sign-windows.ps1` requires Windows, an explicit certificate thumbprint, and HTTPS timestamp URL. It verifies and preserves valid upstream signatures, signs remaining artifacts, and verifies each signature. Microsoft driver certification and a signing certificate are owner-controlled release requirements.

For the packet VPN Apple app, run `apps/apple/build.sh macos|ios` to create the native project/framework, then `scripts/archive-apple.sh macos|ios TEAM_ID SIGNING_IDENTITY` with an explicit developer team and installed profiles. Xcode expands the real account prefix in both Network Extension/keychain entitlements and bundle configuration. Do not distribute a manually signed unsigned packet extension containing unresolved account prefixes. Generic `scripts/sign-macos.sh` can sign the simple desktop bundle; Network Extension bundles require explicit app/provider entitlements. `scripts/notarize-macos.sh` takes explicit App Store Connect key-file, key ID, and issuer environment variables; it never enumerates Keychain identities or reads this machine's Azure keys.

Android Gradle reads `MESHWAY_ANDROID_KEYSTORE`, `MESHWAY_ANDROID_STORE_PASSWORD`, `MESHWAY_ANDROID_KEY_ALIAS`, and `MESHWAY_ANDROID_KEY_PASSWORD` only when a release store is supplied. After rebuilding the Go AAR, run `apps/android/gradlew assembleRelease` from its directory and verify with `apksigner verify --verbose`. An unsigned release build and debug-signed APK are development artifacts. No generated keystore is committed.

## GitHub and registries

`scripts/publish-release.sh OWNER/REPO TAG ARTIFACT_DIR PUBLIC_KEY` verifies every artifact before uploading a draft using an existing tag and `docs/RELEASE_NOTES.md`. `.github/workflows/release.yml` provides the same workflow with an explicit repository, tag, sequence and expiry. The actual `release` environment requires owner review, permits only main/release-tag deployment refs, and contains the encrypted private PEM secret and independent public PEM variable. Public fork PRs never receive them. Its token can publish only to the workflow repository. A remote CI workflow is unverified until actually run there.

Review the draft and release notes, then publish to the owner's supplied destination. The owner authorized releases and container publication to this repository. `.github/workflows/container.yml` publishes source-built `ghcr.io/user-github-me/meshway:VERSION` images for Linux amd64 and arm64 using the workflow token, with source/revision/license labels, SBOM and provenance. Prereleases use their explicit version tag; they do not replace a stable latest tag. It also exports architecture-specific `docker save` archives for signed GitHub release downloads. `scripts/verify-container.py ghcr.io/user-github-me/meshway:VERSION` checks anonymous access and verifies the index and both platform manifest digests without reading registry credentials.

The initial actual publication run `38495059460` succeeded, and anonymous manifest verification passed for `0.1.0-alpha.1`; exact current digest is recorded in `artifacts/container-publication.json`. A later source revision must rerun publication and update that evidence. Container state belongs in a private named volume; initialize it once before running the daemon. Keep control APIs off published ports and leave exit enablement explicit.

The owner logged into npm as `nazifbd` and authorized registry publication. The public registry package uses the owned `@nazifbd/meshway` scope and prerelease `alpha` tag; `@meshway` is not an owned npm organization. `scripts/pack-npm-release.mjs` creates registry metadata around the same tested launcher code and publisher key. Publish its reviewed tarball using `npm publish TARBALL --access public --tag alpha`. The registry tarball and original GitHub launcher tarball are both downloadable release assets, followed by `meshway install-core` for signature-verified core installation. Native app stores require the owner's signing teams and listings; generated test credentials cannot satisfy those requirements.

The owner-authorized GitHub Pages destination is `https://user-github-me.github.io/meshway/`; the repository has Pages configured for Actions. `.github/workflows/pages.yml` builds Vite with `/meshway/`, uploads the static artifact, and deploys on main push. Actual deployment run `38492048133` succeeded on 2026-10-14; a real-browser check of the public site passed with correct assets/dashboard navigation and zero failed resources.
