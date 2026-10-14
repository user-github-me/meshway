#!/usr/bin/env bash
set -euo pipefail
project_root=$(cd "$(dirname "$0")/../../.." && pwd)
cd "$project_root"
mkdir -p dist/apple-tests
xcrun swiftc apps/apple/Tests/BridgeSmoke.swift -F dist/Meshway.xcframework/macos-arm64_x86_64 -framework Meshway -lresolv -o dist/apple-tests/bridge-smoke
exec ./dist/apple-tests/bridge-smoke
