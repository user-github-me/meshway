# Private-key accounts and devices

A Meshway account is a random Ed25519 recovery key, generated locally. It has no email, password, or central login server. The account ID is derived from its public key. A saved recovery key restores account ownership; losing every copy loses the account.

Each device generates an independent networking identity. The account recovery key signs a certificate binding that identity to the account. Importing the same recovery backup on another device gives the same account ID and a different device ID, so simultaneous devices do not fight over one peer identity.

## Create, back up, and import

Stop the daemon before account file operations. The same kernel-backed state lock protects running daemons and offline account operations.

Native macOS, Windows, and Android clients offer recovery export/import through system file pickers, with explicit confirmation when replacing an existing account. CLI operations remain available for server administration.

```sh
meshway account create --config /path/config.json
meshway account export --config /path/config.json --output /private/path/recovery.txt
meshway account import --config /path/config.json --input /private/path/recovery.txt --force
```

Export never prints the secret and never overwrites an existing backup. Import with `--force` replaces the old local account and clears its account history/device registry, preserving the device's network identity. The recovery format is versioned. Store the backup away from the device. Initial at-rest protection uses private files; native Keychain, Android Keystore, and Windows DPAPI protection are still needed.

## History and devices

```sh
meshway account status --config /path/config.json
meshway account devices --config /path/config.json
meshway account history --config /path/config.json
```

History records bounded local daemon/connection metadata (up to 2,000 events); destination URLs and payloads are not collected. Device records hold signed membership, labels, and locally observed last-seen times. An offline peer cannot provide an always-complete global list of devices; the list includes devices learned through sync. Native and web interfaces read the authenticated local API.

## Optional encrypted sync

Import the same recovery backup on two devices. Set `account_sync: true` in both configurations, restart, and connect them using a peer invitation or bootstrap address. Then:

```sh
meshway account sync --config /path/config.json --peer OTHER_DEVICE_PEER_ID
```

Sync authenticates the remote transport identity against the root-signed device certificate, verifies event signatures, and encrypts the bounded dataset using AES-256-GCM in addition to libp2p encryption. It merges device records and deduplicates events. It does not sync root secrets, API tokens, proxy permissions, or system routes. Sync is disabled by default and requires both sides to enable it.

Device removal:

```sh
meshway account revoke --config /path/config.json --peer OTHER_DEVICE_PEER_ID
```

Signed revocations never automatically revert during merge. An observed revoked device cannot sync, and revocation spreads only when other devices synchronize. This does not invalidate a recovery key someone copied: that person can enroll a new device. Account key rotation and delegated device enrollment are required for stronger compromise recovery. Network gateway allowlists remain separately controlled.
