#!/usr/bin/env bash
set -euo pipefail
project_root=$(cd "$(dirname "$0")/../.." && pwd)
export PATH="$project_root/.tools/go/bin:$project_root/.tools/native/bin:$PATH"
cd "$project_root"
lock="$project_root/.tools/native/apple-native-build.lock"
mkdir -p "$project_root/.tools/native"
while ! mkdir "$lock" 2>/dev/null; do
  if [ -f "$lock/pid" ] && ! kill -0 "$(cat "$lock/pid")" 2>/dev/null; then rm -rf "$lock"; continue; fi
  sleep 1
done
echo "$$" > "$lock/pid"
trap 'rm -rf "$lock"' EXIT
platform=${1:-macos}
case "$platform" in
  macos) scheme=macOSApp; sdk=macosx ;;
  ios) scheme=iOSApp; sdk=iphoneos ;;
  iossimulator) scheme=iOSApp; sdk=iphonesimulator ;;
  *) echo 'Usage: apps/apple/build.sh [macos|ios|iossimulator]' >&2; exit 2 ;;
esac
if ! command -v gomobile >/dev/null; then
  GOBIN="$project_root/.tools/native/bin" go install golang.org/x/mobile/cmd/gomobile@v0.0.0-20261011034528-197770444867
fi
if ! command -v gobind >/dev/null; then
  GOBIN="$project_root/.tools/native/bin" go install golang.org/x/mobile/cmd/gobind@v0.0.0-20261011034528-197770444867
fi
gomobile init
if [ "${MESHWAY_REUSE_APPLE_CORE:-0}" != 1 ]; then
  gomobile bind -ldflags='-checklinkname=0 -s -w' -target=ios,iossimulator,macos -iosversion=16.0 -macosversion=13.0 -o dist/Meshway.xcframework ./pkg/meshway
fi
python3 apps/apple/generate-project.py
# No account certificates or Keychain signing identities are read by this build.
xcodebuild -project apps/apple/Meshway.xcodeproj -scheme "$scheme" -configuration Release -sdk "$sdk" -derivedDataPath "dist/apple-$platform" CODE_SIGNING_ALLOWED=NO build
printf 'Built unsigned native %s app and packet tunnel in dist/apple-%s/Build/Products.\n' "$platform" "$platform"
