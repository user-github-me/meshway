# Private-key accounts and devices

A Meshway account is a random Ed25519 recovery key generated locally. It needs no email, password, or central login server. Each device has its own networking identity and a root-signed membership certificate. Importing the same recovery backup enrolls another distinct device without conflicting peer identities. Anyone holding the recovery key can enroll devices; keep an offline copy private.

## Storage and recovery

Native clients encrypt identity, recovery seed, account history and preferences with AES-256-GCM. A separate random device storage key is protected by Android Keystore, Apple Keychain, or Windows DPAPI. The encrypted files authenticate their filename and contents. Existing private files migrate atomically when first opened with the native storage key. The key is excluded from JSON configuration, logs and API responses. CLI/server deployments may supply `MESHWAY_STATE_KEY` from their own secret manager; without it private 0600 files remain the explicit server storage mode. A native launch passes this key in process environment or the in-memory `StartSecure` embedding API. Processes with access to that user's memory/environment can still compromise it.

Stop the daemon before recovery file operations; a kernel state lock protects both running and offline operations. Native file pickers support backup/import. CLI equivalents:

```sh
meshway account create --config meshway.json
meshway account export --config meshway.json --output /private/path/recovery.txt
meshway account import --config meshway.json --input /private/path/recovery.txt --force
meshway account rotate --config meshway.json --output /private/path/new-recovery.txt
```

Export creates a private new file and never prints or overwrites the recovery secret. Importing the identical recovery key preserves signed certificates and history. Replacing a different account requires explicit force and resets its local registry/history while retaining the device network identity. An encrypted transition journal completes interrupted replacement on the next start.

Rotation saves a fresh recovery backup before changing the local account. Import that backup on every trusted device and private server while stopped. The new account ID makes old-root membership invalid on updated servers. Old servers remain accessible to the old root until updated; rotation cannot remotely change an offline machine. Rotation deliberately resets the local registry and history so records signed under the old account cannot be silently trusted in the new one.

## Devices and revocation

The local registry holds signed memberships and locally observed last-seen times. It learns devices through same-account authentication and optional sync. It cannot enumerate unknown offline devices globally.

```sh
meshway account devices --config meshway.json
meshway account revoke --config meshway.json --peer OTHER_DEVICE_PEER_ID
```

Revocations are signed and monotonic. The `/meshway/account-revoke/1.0.0` exchange propagates them independently of history-sync consent to connected known account devices and again after account authentication. Observed revocation closes that peer's transport; a running locally revoked device stops. Offline servers learn a revocation only after a successful trusted exchange. Older clients without the protocol need explicit account sync or an update. A copied recovery key can enroll a fresh device, so use rotation for root compromise.

## Local history and optional sync

History stores up to 2,000 signed connection/lifecycle events, without destination URLs or payloads. It stays local unless `account_sync:true` is enabled on both peers and a sync is requested:

```sh
meshway account history --config meshway.json
meshway account sync --config meshway.json --peer OTHER_DEVICE_PEER_ID
```

Sync verifies transport-bound certificates and event signatures, adds account-level AES-256-GCM encryption to libp2p transport, deduplicates event IDs, and merges monotonic revocations. Conflicting certificates/events reject the entire incoming dataset. It does not transfer recovery secrets, API tokens, route settings, or proxy permissions.

The dashboard offers device revocation and history deletion. `DELETE /v1/account/history` with `{"synchronize":true}` creates a signed cutoff that prevents older sync snapshots restoring deleted events. Send optional sync to apply the deletion to other devices. `synchronize:false` clears only this device's current history; subsequent sync can restore another device's copies. There is no central account reset or global data-retention server.
