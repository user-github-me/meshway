# Start using Meshway

## On your device

Open the native Meshway app. Start the node, save your account recovery key using the native file dialog, and keep the backup somewhere private outside the device. Add a server invitation from someone you trust; the app shows it in Available connections. Choose Connect on an available server. Disconnect stops forwarding through the selected exit. Advanced settings expose peer addresses, permissions, roles, and local ports.

A running node alone does not provide internet access: a server must offer and authorize it. The native desktop apps expose a local SOCKS proxy; configure an application to use it or enable the Meshway browser extension. Full system VPN on macOS/Windows remains a separate implementation gate. Android IP mode currently routes an explicitly chosen private subnet.

## Your own private VPS

On the VPS, build/install the daemon and run:

```sh
meshway setup-server --personal --config /private/path/server.json
meshway account import --config /private/path/server.json --input /private/path/recovery.txt --force
meshway daemon --config /private/path/server.json
```

Import the same recovery key you saved from the client, using a private transfer method. This grants the VPS account authority; secure the VPS accordingly. The server accepts only valid device certificates from that account. It does not require a manual peer allowlist and does not enable history sync. Other accounts cannot fetch its private advertisement or use its exit.

Open TCP/UDP4001 on the VPS firewall. Copy a reachable invitation from its startup addresses, replacing a private/container address with the VPS public address if necessary while preserving the `/p2p/DEVICE_ID` suffix. Add the invitation on your client. Your private server appears with a Private badge and Connect button. Save the invitation as a bootstrap peer for automatic dialing on the next start; runtime selection is not currently persisted automatically.

On a desktop browser, enable the extension after selecting the server. Its default endpoint is `127.0.0.1:1080`. Turn off the extension to restore browser proxy settings when the node stops.

## Community server

Initialize a separate gateway config, enable `exit:true` and `advertise:true`, give it a `server_label`, and explicitly put permitted client peer IDs in `allowed_peers`. Keep `allow_private_targets:false` for public internet forwarding. This mode can serve users with other account keys. A relay is separate: `relay:true` forwards encrypted peer streams without offering internet exit by itself.

## Private service tunnel

On a gateway publish one named service:

```json
"services": [{"name":"website","target":"127.0.0.1:8080"}]
```

On the authorized client configure:

```json
"forwards": [{"name":"website","listen":"127.0.0.1:8081","service":"website","peer":"GATEWAY_PEER_ID"}]
```

Connect to the gateway and open `http://127.0.0.1:8081`. Only that named service is published; a general exit is not needed. Restart after config changes.

## Source and troubleshooting

`scripts/build.sh` builds the native core. `npm ci && npm run dev` starts the website/dashboard. The dashboard needs the loopback API token from the config; tokens remain in page memory. Use real init-generated configurations; example configs contain deliberate nonfunctional placeholder tokens.

If there are no servers, add a reachable invitation or configure bootstrap peers. “Approval required” means the community operator must add your device peer ID. “Unavailable” means the last known server did not respond; refresh or verify its listener/firewall. For personal servers, verify both sides imported the same recovery backup. Do not copy the networking identity file between devices.

See [real-world tests](REAL_WORLD_TESTING.md), [account design](ACCOUNTS.md), and [platform limitations](PLATFORMS.md).
