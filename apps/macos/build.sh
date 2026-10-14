#!/usr/bin/env bash
set -euo pipefail
project_root=$(cd "$(dirname "$0")/../.." && pwd)
daemon=${1:-"$project_root/dist/bin/meshway-darwin-$(uname -m | sed 's/arm64/arm64/;s/x86_64/amd64/')"}
cd "$project_root/apps/macos"
MESHWAY_DAEMON="$daemon" swift test
hit_test_dir=$(mktemp -d "${TMPDIR:-/tmp}/meshway-button-test.XXXXXX")
trap 'rm -rf "$hit_test_dir"' EXIT
swiftc Sources/MeshwayClientCore/FullCardButtonStyle.swift "$project_root/scripts/macos-button-hit-test.swift" -o "$hit_test_dir/button-hit-test"
"$hit_test_dir/button-hit-test"
swift "$project_root/scripts/macos-proxy-framework-lab.swift"
swift build -c release
app="$project_root/dist/Meshway.app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
cp .build/release/Meshway "$app/Contents/MacOS/Meshway"
if [ ! -x "$daemon" ]; then
  echo "Missing executable daemon: $daemon. Build the Go daemon first, or pass its path." >&2
  exit 1
fi
cp "$daemon" "$app/Contents/Resources/meshway"
cp "$project_root/apps/apple/Config/Meshway.icns" "$app/Contents/Resources/Meshway.icns"
cat > "$app/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleName</key><string>Meshway</string>
<key>CFBundleDisplayName</key><string>Meshway</string>
<key>CFBundleIdentifier</key><string>network.meshway.desktop</string>
<key>CFBundleExecutable</key><string>Meshway</string>
<key>CFBundleIconFile</key><string>Meshway</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleShortVersionString</key><string>0.1.0</string>
<key>CFBundleVersion</key><string>1</string>
<key>LSMinimumSystemVersion</key><string>13.0</string>
<key>NSHighResolutionCapable</key><true/>
</dict></plist>
PLIST
echo "Built $app (unsigned; signing/notarization required for public distribution)."
