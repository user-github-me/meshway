#!/usr/bin/env bash
set -euo pipefail
project_root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$project_root"
export PATH="$project_root/.tools/go/bin:$project_root/.tools/native/bin:$PATH"
if [ -z "${ANDROID_HOME:-}" ]; then
  echo "Set ANDROID_HOME to an Android SDK with platform 35, build tools 35.0.0 and NDK 27.2.12479018." >&2
  exit 1
fi
export ANDROID_NDK_HOME=${ANDROID_NDK_HOME:-"$ANDROID_HOME/ndk/27.2.12479018"}
if ! command -v gomobile >/dev/null; then
  GOBIN="$project_root/.tools/native/bin" go install golang.org/x/mobile/cmd/gomobile@v0.0.0-20261011034528-197770444867
fi
if ! command -v gobind >/dev/null; then
  GOBIN="$project_root/.tools/native/bin" go install golang.org/x/mobile/cmd/gobind@v0.0.0-20261011034528-197770444867
fi
# bind uses the pinned gobind already installed above. gomobile init would
# silently fetch gobind@latest and make an otherwise cached build depend on
# an unrelated future module version.
mkdir -p apps/android/app/libs
# libp2p's Android interface dependency anet requires this on Go 1.23+.
# See github.com/wlynxg/anet README; this flag is scoped to the mobile bind.
gomobile bind -ldflags='-checklinkname=0 -s -w' -target=android/arm64,android/amd64 -androidapi 26 -o apps/android/app/libs/meshway.aar ./pkg/meshway
cd apps/android
./gradlew --no-daemon assembleDebug
output_dir=${MESHWAY_ANDROID_OUTPUT_DIR:-"$project_root/dist"}
mkdir -p "$output_dir"
cp app/build/outputs/apk/debug/app-debug.apk "$output_dir/meshway-android-debug.apk"
echo "Built $output_dir/meshway-android-debug.apk. This debug key is for testing; release signing and device validation remain required."
