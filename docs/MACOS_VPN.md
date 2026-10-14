# macOS device VPN helper

Meshway's administrator helper bridges a macOS `utun` interface to the existing
user-owned Meshway daemon. It uses the same authenticated personal-server exit
as SOCKS mode. This separate helper does not require an Apple Developer account;
Apple Network Extension distribution remains a separately signed option.

The helper is undergoing integration and runtime qualification. Fake-backend
and socket tests establish specific cleanup and packet-I/O behavior. They do
not establish actual macOS routing, resolver selection, sleep/wake behavior or
recovery on a physical machine. Do not interpret a successful build as a passed
device VPN test.

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
routing; resolver and route recovery require separate crash qualification.
The app must not promise leak protection across helper crashes, restart,
sleep/wake or unrelated networking changes until those cases are tested.

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
