# Meshway for iPhone and iPad

The native SwiftUI application targets iOS 16+ on iPhone and iPad. Its real
Network Extension packet provider embeds the Go peer core and handles IPv4/IPv6
VPN traffic and tunneled DNS. The app includes server selection, personal-server
invitations, advanced connection modes, device membership, optional encrypted
history sync, backup/import/replacement and device revocation.

Build both shared daemon architectures first, then the iOS app:

```sh
./scripts/build.sh --all
./apps/ios/build.sh
```

Xcode and the pinned gomobile tools are required. Build scripts produce
`dist/apple-ios/Build/Products/Release-iphoneos/Meshway.app` and release packaging
creates an unsigned developer IPA containing `Payload/Meshway.app`. An unsigned
IPA cannot run a VPN extension on a physical iPhone. Use your Apple Developer
Team and provisioning profiles with Network Extension, app-group and Keychain
capabilities. `scripts/archive-apple.sh ios TEAM_ID SIGNING_IDENTITY` and
`scripts/export-ios.sh` handle the owner-signed archive/export. See
[Apple release instructions](../../docs/RELEASING.md).

Unsigned device and simulator source builds pass; the actual Swift/Go encrypted
packet bridge and account behavior are tested. No physical iPhone or signed
Network Extension runtime has been certified. Validate the installed signed app
on Wi-Fi and cellular, including DNS/IPv6, peer loss, reconnect, sleep/wake and
explicit-disconnect restoration before relying on it for sensitive traffic.
