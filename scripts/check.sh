#!/bin/sh
set -eu
project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$project_dir"
if command -v go >/dev/null 2>&1; then go_command=go; else go_command=$project_dir/.tools/go/bin/go; fi
"$go_command" vet ./...
"$go_command" test -race ./... -count=1 -timeout=180s
npm test
npm run typecheck
npm run build
./scripts/build.sh --all
if [ "$(uname -s)" = Darwin ]; then
  ./apps/macos/build.sh
fi
