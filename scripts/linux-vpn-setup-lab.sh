#!/bin/sh
set -eu
project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$project_dir"
: "${MESHWAY_LAB_IMAGE:?Set a trusted LOCAL Alpine image with ip/apk}"
if command -v go >/dev/null 2>&1; then go_command=go; else go_command=$project_dir/.tools/go/bin/go; fi
arch=$(docker info --format '{{.Architecture}}'); case "$arch" in aarch64|arm64) arch=arm64;; x86_64|amd64) arch=amd64;; *) exit 1;; esac
CGO_ENABLED=0 GOOS=linux GOARCH=$arch "$go_command" test -c -o .tools/linux-setup.test ./internal/linuxvpn
lab_dir=$(mktemp -d "$project_dir/.tools/linux-setup.XXXXXX")
network="meshway-setup-$$";gateway="meshway-setup-gateway-$$";image="meshway-setup-lab:$$"
cleanup(){ docker rm -f "$gateway" >/dev/null 2>&1 || true; docker network rm "$network" >/dev/null 2>&1 || true; docker image rm "$image" >/dev/null 2>&1 || true; rm -rf "$lab_dir"; }
trap cleanup EXIT INT TERM
# Fetch distro-signed nftables inside the disposable image; never install host tools.
printf 'FROM %s\nUSER root\nRUN apk add --no-cache nftables iproute2\n' "$MESHWAY_LAB_IMAGE" | docker build -t "$image" - >/dev/null
docker network create --subnet 10.89.77.0/24 "$network" >/dev/null
docker run -d --name "$gateway" --network "name=$network,ip=10.89.77.2,driver-opt=com.docker.network.endpoint.sysctls=net.ipv6.conf.IFNAME.disable_ipv6=0" --cap-add NET_ADMIN \
 -e MESHWAY_LINUX_HELPER_GATEWAY=1 -e MESHWAY_LAB_DIR=/lab -v "$lab_dir:/lab" -v "$project_dir/.tools/linux-setup.test:/meshway-test:ro" \
 --sysctl net.ipv6.conf.all.disable_ipv6=0 --sysctl net.ipv6.conf.default.disable_ipv6=0 --entrypoint /bin/sh "$image" \
 -c 'ip addr add 10.98.0.2/32 dev eth0; /meshway-test -test.run TestLinuxSetupGatewayFixture -test.v -test.timeout 100s' >/dev/null
ready=0;while [ "$ready" -lt 100 ];do [ ! -f "$lab_dir/gateway.json" ] || break;ready=$((ready+1));sleep 0.1;done
if [ ! -f "$lab_dir/gateway.json" ];then docker logs "$gateway";exit 1;fi
docker run --rm --network "name=$network,ip=10.89.77.3,driver-opt=com.docker.network.endpoint.sysctls=net.ipv6.conf.IFNAME.disable_ipv6=0" --cap-add NET_ADMIN \
 -e MESHWAY_LINUX_HELPER_LAB=1 -e MESHWAY_LAB_DIR=/lab -v "$lab_dir:/lab" -v "$project_dir/.tools/linux-setup.test:/meshway-test:ro" \
 --sysctl net.ipv6.conf.all.disable_ipv6=0 --sysctl net.ipv6.conf.default.disable_ipv6=0 --entrypoint /bin/sh "$image" \
 -c 'mkdir -p /dev/net; mknod /dev/net/tun c 10 200; /meshway-test -test.run TestLinuxPrivilegedSetupRollbackAndFailClosed -test.v -test.timeout 70s'
