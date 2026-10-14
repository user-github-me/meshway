# macOS device VPN helper

Meshway's administrator helper bridges a macOS `utun` interface to the existing
user-owned Meshway daemon. It uses the same authenticated personal-server exit
as SOCKS mode. This separate helper does not require an Apple Developer account;
Apple Network Extension distribution remains a separately signed option.

The administrator helper passed actual kernel tests on disposable GitHub-hosted
macOS runners. Public-exit [run38629280142](https://github.com/user-github-me/meshway/actions/runs/38629280142)
at `5b7d544` exercised native DNS, the expected exit IP and certificate-verified
public HTTPS through the owned interface in three cycles: explicit Stop,
automatic parent-exit cleanup, and SIGKILL followed by scoped recovery. Every
cycle restored the original routes, DNS and proxy and closed the daemon packet
session. The temporary Azure resource group was deleted and its test credentials
removed; [public evidence](../artifacts/macos-utun-public-validation.json) records
both confirmations and the exact binary hashes.

The final conservative route-absence checks then passed actual full-helper
[run38630817901](https://github.com/user-github-me/meshway/actions/runs/38630817901)
at `7bb531f` with all three cleanup modes and twelve simultaneous IPv4/IPv6 DNS
queries per cycle; [kernel evidence](../artifacts/macos-utun-full-loopback-validation.json)
records the result. Earlier failed DNS tests remain in
[partial evidence](../artifacts/macos-utun-runtime-partial.json). Their cause was
a temporary physical route clone being mistaken for a persistent server bypass.
The helper now preserves a durable bypass and checks the effective physical
route after installing capture routes.

These results qualify the tested administrator helper. Physical hardware,
sleep/wake, native authorization UI and signed Network Extension behavior remain
separate checks. The public exit fixture used IPv4; IPv6 kernel capture and DNS
were tested, and public IPv6 egress requires a dual-stack exit.

## Ownership and setup

The app starts its daemon as the logged-in user, keeping account state and the
SSH agent in that user's session. Administrator authorization applies only to
the packet interface and networking helper. It does not create another account
or daemon as root. API credentials are read from a private configuration file;
they are not passed in process arguments or printed.

The owning app resolves the actual peer, relay or SSH endpoint addresses before
authorization and supplies those literal IPs to the helper. SSH aliases are
resolved using the user's existing OpenSSH configuration. The helper must
preserve routes to all supplied transport endpoints before capturing default
traffic. An existing device VPN or an endpoint already routed through another
`utun` interface must cause setup to refuse rather than replace that VPN.

The CLI contract is:

```text
meshway vpn start --config /absolute/private/config.json --peer PEER_ID \
  --underlay-ips TRANSPORT_IP[,OTHER_TRANSPORT_IP] --parent-pid APP_PID
meshway vpn stop --config /absolute/private/config.json
meshway vpn status --config /absolute/private/config.json
```

Start runs in the foreground under administrator authorization. The app owns
its lifetime. Stop and privileged status operate on the protected runtime
journal under `/private/var/db/meshway-macvpn`. These are administrative commands;
the app should present their concise result without exposing credentials.

## Packet path and cleanup

The helper starts the daemon's authenticated in-memory packet session before
installing routes. It assigns the session's fixed IPv4/IPv6 addresses with MTU
1280, captures IPv4/IPv6 default traffic, and creates an owned dynamic resolver
entry for the exit's virtual DNS addresses. It does not overwrite a physical
network service's DNS configuration or alter the system proxy.

Darwin `utun` packets carry a four-byte network-order address-family header.
Only validated IPv4 and IPv6 packets within the negotiated MTU enter the
authenticated HTTP packet queue. All packet requests use the daemon's existing
loopback bearer authentication. The helper also acquires a random packet-session
ID and supplies `X-Meshway-VPN-Session` on its reads, writes, status and Stop
requests. A different native client cannot claim or stop that queue. No API is
exposed on the public network.

The daemon reports `capture_ready` only after the helper verifies actual route
and resolver ownership. Its `capture_instance` correlates the live helper with
the app's own launch; connection to an encrypted peer alone is not proof that
application traffic is captured. The private recovery journal retains the
packet-session owner and original configuration owner so stale Stop can release
that exact queue without stopping a newer native session.

Packet reads and writes use a nonblocking descriptor and bounded polling.
Stopping workers first leaves the descriptor open during owned-route cleanup;
closing it earlier would allow macOS to reuse the interface name. Stale
recovery skips interface changes after its owning process has exited. Cleanup must
verify both route protocol markers and the random journal metric, verify its
random DNS entry and virtual resolver values, attempt every owned inverse
operation, and retain uncertain failures in the private journal for an explicit retry. A failed route
addition or an interface name alone is never evidence of ownership.

While the helper runs, a peer outage retains packet capture and drops traffic
until the encrypted session reconnects. There is no persistent macOS firewall
kill switch in this helper. A crash can close the interface and restore direct
routing. SIGKILL followed by explicit scoped Stop restored resolver and route
state in the disposable tests; it does not establish uninterrupted crash
containment.
The app must not promise leak protection across helper crashes, restart,
sleep/wake or unrelated networking changes.

## Safe local checks

```sh
go test -race ./internal/macvpn
GOOS=darwin GOARCH=amd64 go build ./cmd/meshway
GOOS=darwin GOARCH=arm64 go build ./cmd/meshway
```

The packet-I/O tests use disposable Unix datagram socket pairs. Transaction
tests use fake route/DNS executors and cover foreign resources, ambiguous
failures and continued cleanup. They never change the development host's
routes, DNS, VPN or proxy. Actual administrator setup belongs in a disposable
macOS VM or a separately authorized physical-device test with recovery access.

The manual `macos-utun.yml` workflow runs `scripts/macos-utun-lab.py` only on an
explicit disposable GitHub macOS runner. It creates a fresh private account,
publishes only its public peer ID for a separately authorized temporary exit,
then checks kernel routes, native DNS, public TCP/TLS and explicit/parent-exit/
SIGKILL recovery. Its artifacts contain bounded results and public identity;
configuration, account files and private helper logs are excluded. A failed
native check stays a failed runtime gate even when cleanup succeeds.
