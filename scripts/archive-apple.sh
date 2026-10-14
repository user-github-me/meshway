#!/bin/sh
set -eu
if [ "$(uname -s)" != Darwin ] || [ "$#" != 3 ]; then
  printf '%s\n' 'Usage: scripts/archive-apple.sh macos|ios EXPLICIT_TEAM_ID EXPLICIT_SIGNING_IDENTITY' >&2; exit 1
fi
platform=$1
team=$2
identity=$3
case "$platform" in
  macos) scheme=macOSApp; destination='generic/platform=macOS';;
  ios) scheme=iOSApp; destination='generic/platform=iOS';;
  *) exit 1;;
esac
project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
mkdir -p "$project_dir/dist"
# Xcode expands AppIdentifierPrefix in app/provider keychain entitlements and
# Info.plist. A real explicit team and available provisioning profiles are required.
xcodebuild -project "$project_dir/apps/apple/Meshway.xcodeproj" -scheme "$scheme" \
  -configuration Release -destination "$destination" \
  -archivePath "$project_dir/dist/Meshway-$platform.xcarchive" \
  DEVELOPMENT_TEAM="$team" CODE_SIGN_IDENTITY="$identity" CODE_SIGN_STYLE=Automatic archive
printf '%s\n' 'Apple archive created using the explicit team and signing identity.'
