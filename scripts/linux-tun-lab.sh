#!/bin/sh
set -eu
project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$project_dir"
if command -v go >/dev/null 2>&1; then go_command=go; else go_command=$project_dir/.tools/go/bin/go; fi
if [ -z "${MESHWAY_LAB_IMAGE:-}" ]; then
  printf '%s\n' 'Set MESHWAY_LAB_IMAGE to a trusted local Linux image with sh/ip/mknod (for example alpine:3.23).' >&2
  exit 1
fi
docker_arch=$(docker info --format '{{.Architecture}}')
case "$docker_arch" in aarch64|arm64) target_arch=arm64;; x86_64|amd64) target_arch=amd64;; *) exit 1;; esac
mkdir -p .tools
CGO_ENABLED=0 GOOS=linux GOARCH=$target_arch "$go_command" test -c -o .tools/meshway-vpn-linux-test ./internal/vpn
docker run --rm --network none --cap-add NET_ADMIN -e MESHWAY_LINUX_TUN_LAB=1 \
  -v "$project_dir/.tools/meshway-vpn-linux-test:/meshway-vpn-test:ro" \
  --entrypoint /bin/sh "$MESHWAY_LAB_IMAGE" \
  -c 'mkdir -p /dev/net; mknod /dev/net/tun c 10 200; /meshway-vpn-test -test.run TestLinuxKernelTUNEncryptedPing -test.v -test.timeout 30s'
