#!/bin/sh
set -eu
project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$project_dir"
if [ -n "${GO_BINARY:-}" ]; then go_command=$GO_BINARY
elif command -v go >/dev/null 2>&1; then go_command=go
elif [ -x .tools/go/bin/go ]; then go_command=$project_dir/.tools/go/bin/go
else printf '%s\n' 'Go 1.26+ is required. Install Go from https://go.dev/dl/.' >&2; exit 1
fi
mkdir -p dist/bin
if [ "${1:-}" = "--all" ]; then targets='darwin/arm64 darwin/amd64 linux/arm64 linux/amd64 windows/arm64 windows/amd64'
else targets=$("$go_command" env GOOS)/$("$go_command" env GOARCH)
fi
for target in $targets; do
  target_os=${target%/*}; target_arch=${target#*/}; suffix=
  if [ "$target_os" = windows ]; then suffix=.exe; fi
  output=dist/bin/meshway-$target_os-$target_arch$suffix
  printf 'Building %s\n' "$output"
  CGO_ENABLED=0 GOOS=$target_os GOARCH=$target_arch "$go_command" build -buildvcs=false -trimpath -ldflags='-s -w' -o "$output" ./cmd/meshway
done
if [ "${1:-}" != "--all" ]; then cp "$output" dist/meshway$suffix; fi
