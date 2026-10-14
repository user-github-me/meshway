#!/bin/sh
set -eu
if [ "$(uname -s)" != Darwin ] || [ "$#" != 2 ]; then
  printf '%s\n' 'Usage: scripts/notarize-macos.sh SIGNED_APP OUTPUT_ZIP' >&2; exit 1
fi
: "${MESHWAY_NOTARY_KEY_FILE:?Explicit App Store Connect API private-key file required}"
: "${MESHWAY_NOTARY_KEY_ID:?API key identifier required}"
: "${MESHWAY_NOTARY_ISSUER:?API issuer identifier required}"
codesign --verify --deep --strict "$1"
ditto -c -k --keepParent "$1" "$2"
# Credentials are passed explicitly; no Keychain profiles, enumeration, or credential output.
xcrun notarytool submit "$2" --key "$MESHWAY_NOTARY_KEY_FILE" --key-id "$MESHWAY_NOTARY_KEY_ID" --issuer "$MESHWAY_NOTARY_ISSUER" --wait
xcrun stapler staple "$1"
xcrun stapler validate "$1"
spctl --assess --type execute "$1"
ditto -c -k --keepParent "$1" "$2"
