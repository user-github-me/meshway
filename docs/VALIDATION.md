# Runtime and network validation

The development Mac's default routes, DNS, active VPN and system proxy remain unchanged in every lab. Exit configuration is explicit and temporary. Keys and bearer tokens stay in private temporary files; test output records public peer IDs and transport observations only. Test harnesses stop owned daemons and remove owned networks/resources on failure as well as success.

## Restrictive NAT, relay and loss

`scripts/nat-lab.py` creates five independent Docker namespaces: a public relay, two separate MASQUERADE routers, a private client and a private exit. Each router denies inbound initiation. A direct client-to-exit attempt must fail before a circuit relay connection succeeds. The test asserts the actual authenticated transport is marked circuit-limited, fetches public HTTPS, adds 80 ms delay with 20 ms jitter and 3% packet loss on the disposable relay, observes relay outage, and fetches again after restart.

```sh
docker build --pull=false --build-arg LAB_BASE_IMAGE=YOUR_TRUSTED_LOCAL_ALPINE_IMAGE -f deploy/Dockerfile.lab -t meshway:network-lab .
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o .tools/cloud/meshway-linux-arm64 ./cmd/meshway
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o .tools/cloud/meshway-net-lab-linux-arm64 ./cmd/meshway-net-lab
python3 scripts/nat-lab.py --image meshway:network-lab
```

Use matching Linux architecture for the Docker engine. The 2026-10-14 run passed direct rejection, HTTPS through a real circuit-limited connection, delay/loss, outage detection and restored HTTPS. Evidence is `artifacts/nat-validation.json`. The development network synthesized reserved `198.18.0.0/15` DNS answers; Meshway correctly refused them. The lab obtains public DNS over authenticated HTTPS and writes only the disposable exit's `/etc/hosts`, keeping host DNS unchanged and private-target blocking enabled. This is a lab accommodation, not product DNS configuration.

## Real external networks

`scripts/cloud-validation.py --provision` is explicitly opt-in and requires prior owner authorization to incur temporary Azure charges. It uses the logged-in Azure CLI session, two small B1s Ubuntu VMs in separate VNets, dedicated temporary resource group/tags, a fresh private SSH key, SSH restricted to the current client IP, and TCP/UDP peer port 4001. Control APIs stay loopback behind SSH forwarding. SSH on 443 accommodates networks filtering port 22. A `finally` block deletes the entire dedicated group and checks that it is absent; inspect the recorded cleanup result.

The successful external run on 2026-10-14 fetched example.com HTTPS through the owner's Mac → Azure Linux exit → public Internet, then blocked new inbound exit connections and fetched through the separate public Azure relay. Its actual transport was circuit-limited. The same run passed 80 ms delay/20 ms jitter/3% packet loss, relay-outage detection, and HTTPS after relay restart. Both VMs were deleted and the resource group was confirmed absent. The first port-22 deployment encountered network filtering, was deleted and retried on 443. A test firewall initially dropped established relay reservation traffic; restricting only new initiation corrected the lab. Exact results and SHA-256 of the tested binary are in `artifacts/cloud-validation.json`.

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o .tools/cloud/meshway ./cmd/meshway
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o .tools/cloud/meshway-net-lab ./cmd/meshway-net-lab
go build -o .tools/cloud/meshway-net-lab-local ./cmd/meshway-net-lab
python3 scripts/cloud-validation.py --provision --linux-binary .tools/cloud/meshway
```

Temporary tests are not a permanent public Meshway service. Carrier-grade NAT, independent residential ISPs, blocked UDP/TCP, changing Wi-Fi/cellular networks, real sleep/wake and prolonged use require additional field runs. Reliable relay fallback has a reachable-transport precondition; no app can promise every network permits any tunnel.

## Platform execution

Website: the production `/meshway/` build was loaded in an isolated real Chromium session, navigated to the dashboard, and checked for loaded logo images and failed resources; zero failed resources. GitHub Pages deployment succeeded in actual Action run `38492048133`; the public `https://user-github-me.github.io/meshway/` was then loaded in real Chromium, navigated to `#/dashboard`, and verified with both logo images loaded and zero failed resources. `.github/workflows/pages.yml` deploys that base path after a main-branch push. Assets use Vite's base URL instead of root-relative logo paths.

Windows: `scripts/validate-windows.ps1 -Daemon PATH -Desktop OPTIONAL_GUI` runs native initialization, loopback API readiness/auth rejection, graceful shutdown and optionally a real GUI window on Windows. Actual Windows CI additionally passed pinned vendor-signed Wintun kernel IPv4/IPv6 encrypted TCP packet round trips without changing default routes or DNS; evidence is `artifacts/windows-ci-final/windows-wintun-validation.json`. `scripts/windows-tun-lab.ps1` reproduces this on a disposable Windows administrator runner. Compilation on macOS does not replace this result. Full-device native helper routing/firewall/crash cleanup remains a separate runtime gate and must be exercised in a disposable Windows VM.

Android: `scripts/validate-android.sh SERIAL APK [ADB]` installs and launches on an explicitly selected emulator/device. The kernel lab `TestAndroidKernelTUNEncryptedPing` verifies actual Android VpnService TUN packets. The Android35 ARM64 emulator packet-exit run passed six real TUN checks including TCP, DNS, TLS1.3, blocked traffic during exit failure, restored traffic and lifecycle recovery; evidence is `artifacts/android-ip-exit/result.json`. User consent is needed; tests must not silently grant full-device VPN permission on a personal device. A physical-device release run must cover IPv4/IPv6 HTTPS, DNS capture, VPN disconnect, exit death, battery optimization, background operation, screen lock, reboot, Wi-Fi/mobile switching and another VPN's consent behavior. Record model, OS, build hash, carrier/network and packet evidence. An emulator cannot certify physical carrier behavior.

Apple: unsigned app/provider compilation is source validation. A signed Network Extension on an actual supported macOS/iOS installation must establish the tunnel and prove default routes, DNS, IPv6 containment, sleep/wake and fail-closed behavior, then restore prior state. Team/provisioning entitlements must match; an unsigned extension cannot be represented as a verified installed VPN.

Distribution: source-built GHCR Linux amd64/arm64 manifests passed anonymous digest verification, and the ARM64 image actually executed its declared version as an unprivileged user. Exported Docker archives loaded and retained bundled license notices. `scripts/verify-container.py` records current anonymous manifest evidence in `artifacts/container-publication.json`. `scripts/package-install-lab.py` actually installed and removed Debian and Fedora RPM packages inside owned containers, checking native version, private state permissions, VPN dependencies, no automatic service enablement and retained account state after removal. Exact package hashes are recorded in `artifacts/deb-install-validation.json` and `artifacts/rpm-install-validation.json`. These tests caught and fixed missing Debian archive parent directories and an undeclared RPM post-install runuser dependency. Every owned package-test container was removed.

The owner-authorized persistent Azure relay is separate from deleted temporary validation VMs. Its current binary hash matches the released Linux core, with real TCP/QUIC reservations, forced encrypted public-circuit payload, restart/reconnection and retained identity checks documented in `artifacts/public-relay.json` and `docs/PUBLIC_NETWORK.md`.

## Security assessment and independent review

Development security checks include Go race/vet/fuzz testing, signed account/certificate tampering, private target rejection, loopback origin/auth checks, stream bounds, encrypted sync replay checks, and Ed25519 release tamper/rollback/expiry tests. These are implementation checks, not an independent audit.

Review observations: the default daemon is neither an open proxy nor an internet exit; an exit requires explicit configuration and peer authorization. SOCKS DNS resolution at an exit validates resolved addresses and dials those exact IPs. Signed release metadata now prevents a compromised mirror substituting its own checksums. The local publisher key and account root remain sensitive trust anchors; their compromise requires explicit trust replacement/account recovery action. Peers and exits can observe timing/destinations, so this provides encrypted connectivity without an anonymity guarantee. Relays have bounded reservations/circuits; public operator abuse policy and budget remain operational obligations.

Before claiming independent review, commission a reviewer with no implementation role and publish their signed/date-stamped report identifying the exact commit and tested artifacts. Required review scope: account vault/key derivation and recovery export; root/device certificates and revoked-device propagation; sync replay/conflict/deletion; libp2p configuration/relay resource exhaustion; DNS rebinding/private destination policy; packet parser/netstack/UDP and DNS egress; native privileged helper and IPC authorization; IPv6/default-route/leak/kill-switch failure states; browser restoration; public advertisement privacy; dependency provenance; release/update trust and rollback; signing entitlements; container/systemd privilege boundaries; sustained hostile-peer load and exit abuse response. Track each finding and retest the fixed commit. No development agent can self-certify this external review as completed.
