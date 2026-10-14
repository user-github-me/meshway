#!/bin/sh
set -eu
if [ "$(uname -s)" != Darwin ] || [ "$#" != 2 ]; then
  printf '%s\n' 'Usage: scripts/sign-macos.sh APP_PATH EXPLICIT_CODESIGN_IDENTITY' >&2
  exit 1
fi
app=$1
identity=$2
test -d "$app/Contents"
# Sign embedded Mach-O executables before bundles; never enumerate signing identities.
find "$app/Contents" -type f -perm -111 | while IFS= read -r binary; do
  case "$(file -b "$binary")" in *Mach-O*)
    codesign --force --timestamp --options runtime --sign "$identity" "$binary"
    ;; esac
done
find "$app/Contents" -type d -name '*.framework' -depth | while IFS= read -r framework; do
  codesign --force --timestamp --options runtime --sign "$identity" "$framework"
done
find "$app/Contents" -type d -name '*.appex' -depth | while IFS= read -r extension; do
  : "${MESHWAY_MAC_EXTENSION_ENTITLEMENTS:?Network Extension entitlement file must be supplied explicitly}"
  codesign --force --timestamp --options runtime --entitlements "$MESHWAY_MAC_EXTENSION_ENTITLEMENTS" --sign "$identity" "$extension"
done
if [ -n "${MESHWAY_MAC_APP_ENTITLEMENTS:-}" ]; then
  codesign --force --timestamp --options runtime --entitlements "$MESHWAY_MAC_APP_ENTITLEMENTS" --sign "$identity" "$app"
else
  codesign --force --timestamp --options runtime --sign "$identity" "$app"
fi
codesign --verify --deep --strict "$app"
printf '%s\n' 'macOS signing verified. Notarization is a separate release step.'
