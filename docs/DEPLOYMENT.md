# Server and distribution setup

## VPS / Linux

Build with `scripts/build.sh --all` or create local release archives with `scripts/release.sh`. Copy the correct Linux binary to `/usr/local/bin/meshway`. Create a dedicated `meshway` user, create `/var/lib/meshway` owned by it, and run:

On systemd distributions with `useradd`/`runuser`, `sudo ./deploy/install-systemd.sh /path/to/meshway-linux-ARCH` performs the local installation and generates private state without starting the service. Edit its roles and start it explicitly after setup. The manual steps follow.

```sh
sudo -u meshway /usr/local/bin/meshway init --config /var/lib/meshway/config.json
```

Edit roles and authorization while stopped. Copy `deploy/systemd/meshway.service` to `/etc/systemd/system/`, then enable/start the service. Open inbound TCP and UDP port 4001 for peer transport. Leave SOCKS/API on loopback. Public IPs/hostnames in peer invitation addresses must actually reach the VPS; container NAT addresses are not globally usable.

`relay: true` enables bounded circuit relay service. `exit: true` enables internet destinations only for IDs in `allowed_peers`. `advertise: true` explicitly offers the node's description to known peers, with optional `server_label` and `server_country`. Private services use named `services`, not blanket access to LAN targets.

The systemd example drops capabilities and cannot create TUN devices. If enabling experimental Linux IP VPN, a separate administrator-controlled TUN setup is needed; review `docs/VPN.md` rather than weakening service permissions indiscriminately.

## Containers

Docker Desktop/Engine must be running. From the project root:

```sh
docker compose -f deploy/compose.yaml build
docker compose -f deploy/compose.yaml run --rm meshway init --config /var/lib/meshway/config.json
docker compose -f deploy/compose.yaml run --rm --entrypoint sh meshway
```

Use the shell only to edit the persisted config with an available editor or copy it using Docker volume tooling, then start `docker compose -f deploy/compose.yaml up -d`. The image runs as a non-root user and persists identity/config in a named volume. The default container is not a TUN VPN and does not expose a browser-facing API/proxy. QUIC requires publishing UDP4001 as shown.

If a compiler image cannot be downloaded, `deploy/Dockerfile.prebuilt` packages `dist/bin/meshway-linux-ARCH` into a trusted Alpine runtime. Build with `docker build -f deploy/Dockerfile.prebuilt --build-arg TARGETARCH=arm64 -t meshway:local .` (use `amd64` as appropriate). The local validation used an already cached Alpine-based image due Docker Hub timeouts, and verified non-root initialization plus actual Mac→Linux-container→public-HTTPS forwarding. A subsequent actual source compiler-image build succeeded with the official SDK archive and checksum-verified dependencies; encrypted public HTTPS through the resulting image also passed.

## npm and releases

`packages/cli` launches a trusted Go binary and downloads releases only through an explicit `install-core` action. In the source checkout it detects `dist/bin` or accepts `MESHWAY_BINARY`. `scripts/release.sh` writes six raw platform binaries, archives, and SHA256SUMS locally. The owner-authorized GitHub source/release destination is user-github-me/meshway. The installer ships its independent publisher-key pin; see RELEASING.md for signed alpha downloads. No resources are published by these scripts.

Owner signing/notarization/store submissions require legitimate credentials. Their scripts, iOS entitlements, Debian/RPM packaging and signed update verification are implemented; actual store publication and registry ownership remain operational requirements. Do not label debug APKs or unsigned builds as production releases.
