# Meshway npm launcher

This package launches the shared Go networking core. It is not an independent JavaScript VPN. No package or hosted release has been published yet.

From this checkout:

```sh
./scripts/build.sh
npm install
node packages/cli/bin/meshway.js version
node packages/cli/bin/meshway.js init --config .meshway/config.json
node packages/cli/bin/meshway.js daemon --config .meshway/config.json
```

After publishing a trusted release containing raw platform binaries and `SHA256SUMS`:

```sh
meshway install-core --release-base https://github.com/YOUR-ORG/YOUR-REPO/releases/download/v0.1.0/
```

The installer requires HTTPS, bounds download sizes, verifies SHA-256 before installing, and verifies the cached binary again on launch. A checksum from the same release proves integrity, not publisher identity. Release signatures are a future gate. Set `MESHWAY_BINARY` to an explicitly trusted local executable, or `MESHWAY_CACHE_DIR` for another installation location. Installing the npm package does not automatically download or execute native code.
