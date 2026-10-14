# Meshway npm launcher

This package launches the shared Go networking core. The public npm distribution is `@nazifbd/meshway`; the original `@meshway/cli` launcher tarball is available in the GitHub release.

From this checkout:

```sh
./scripts/build.sh
npm install
node packages/cli/bin/meshway.js version
node packages/cli/bin/meshway.js init --config .meshway/config.json
node packages/cli/bin/meshway.js daemon --config .meshway/config.json
```

Install the public npm package and explicitly install the signed native core:

```sh
npm install -g @nazifbd/meshway@alpha
meshway install-core
meshway version
```

Or install the original GitHub tarball:

```sh
npm install -g https://github.com/user-github-me/meshway/releases/download/v0.1.0-alpha.1/meshway-cli-0.1.0.tgz
meshway install-core
meshway version
```

`install-core` uses the signed project's `0.1.0-alpha.1` release and its public-key pin by default. For a fork, pass `--release-base HTTPS_URL --public-key /path/to/independently-verified.pem --version VERSION`. Downloads happen only when you explicitly run the install command. Running a missing core gives a clear installation instruction and does not start a network download.

The package ships the project publisher pin in `release-public.pem`. Acquire and confirm a custom fork's public key independently of its release download before using `--public-key`. The installer requires its pinned key, HTTPS, a valid Ed25519 signature, unexpired metadata, and a matching SHA-256 before installing. Reusing the cache rejects lower release sequences, conflicting metadata at one sequence, and publisher key changes. Run the same command for an update; it never replaces a running daemon. Stop the app, install, and restart. Cached launches reverify the signature and bytes while allowing installed metadata to expire for offline use.

The pin and rollback record protect against a compromised download server, not an attacker who can rewrite your local installation and trust files. Explicit publisher-key changes need a separate cache and independent verification. Set `MESHWAY_BINARY` to a trusted local executable, or `MESHWAY_CACHE_DIR` for another installation location. Installing the npm package does not automatically download or execute native code.
