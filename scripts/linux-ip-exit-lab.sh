#!/bin/sh
set -eu
project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$project_dir"
if command -v go >/dev/null 2>&1; then go_command=go; else go_command=$project_dir/.tools/go/bin/go; fi
if [ -z "${MESHWAY_LAB_IMAGE:-}" ]; then
 printf '%s\n' 'Set MESHWAY_LAB_IMAGE to a trusted LOCAL Linux image containing sh/ip/mknod.' >&2
 exit 1
fi
docker image inspect "$MESHWAY_LAB_IMAGE" >/dev/null
docker_arch=$(docker info --format '{{.Architecture}}')
case "$docker_arch" in aarch64|arm64) target_arch=arm64;; x86_64|amd64) target_arch=amd64;; *) exit 1;; esac
mkdir -p .tools
CGO_ENABLED=0 GOOS=linux GOARCH=$target_arch "$go_command" test -c -o .tools/meshway-ip-exit-linux-test ./internal/vpn
lab_dir=$(mktemp -d "$project_dir/.tools/ip-exit-lab.XXXXXX")
lab_network="meshway-ip-exit-$$"
lab_gateway="meshway-ip-gateway-$$"
cleanup() {
 docker rm -f "$lab_gateway" >/dev/null 2>&1 || true
 docker network rm "$lab_network" >/dev/null 2>&1 || true
 rm -rf "$lab_dir"
}
trap cleanup EXIT INT TERM
docker network create --subnet 10.88.77.0/24 "$lab_network" >/dev/null
docker run -d --name "$lab_gateway" --network "name=$lab_network,ip=10.88.77.2,driver-opt=com.docker.network.endpoint.sysctls=net.ipv6.conf.IFNAME.disable_ipv6=0" --cap-add NET_ADMIN \
 -e MESHWAY_LINUX_EXIT_GATEWAY=1 -e MESHWAY_LAB_DIR=/lab -e MESHWAY_LAB_INTERNET="${MESHWAY_LAB_INTERNET:-0}" \
 -v "$lab_dir:/lab" -v "$project_dir/.tools/meshway-ip-exit-linux-test:/meshway-vpn-test:ro" \
 --sysctl net.ipv6.conf.all.disable_ipv6=0 --sysctl net.ipv6.conf.default.disable_ipv6=0 --entrypoint /bin/sh "$MESHWAY_LAB_IMAGE" \
 -c 'ip addr add 10.99.0.2/32 dev eth0; ip -6 addr add fd99::2/128 dev eth0 nodad; /meshway-vpn-test -test.run TestLinuxInternetVPNGateway -test.v -test.timeout 110s' >/dev/null
ready=0
while [ "$ready" -lt 100 ]; do
 if [ -f "$lab_dir/gateway.json" ]; then break; fi
 ready=$((ready+1))
 sleep 0.1
done
if [ ! -f "$lab_dir/gateway.json" ]; then docker logs "$lab_gateway"; exit 1; fi
docker run --rm --network "name=$lab_network,ip=10.88.77.3,driver-opt=com.docker.network.endpoint.sysctls=net.ipv6.conf.IFNAME.disable_ipv6=0" --cap-add NET_ADMIN \
 -e MESHWAY_LINUX_IP_EXIT_LAB=1 -e MESHWAY_LAB_DIR=/lab -e MESHWAY_LAB_INTERNET="${MESHWAY_LAB_INTERNET:-0}" \
 -v "$lab_dir:/lab" -v "$project_dir/.tools/meshway-ip-exit-linux-test:/meshway-vpn-test:ro" \
 --sysctl net.ipv6.conf.all.disable_ipv6=0 --sysctl net.ipv6.conf.default.disable_ipv6=0 --entrypoint /bin/sh "$MESHWAY_LAB_IMAGE" \
 -c 'mkdir -p /dev/net; mknod /dev/net/tun c 10 200; printf "nameserver 10.77.0.1\noptions timeout:1 attempts:1\n" > /etc/resolv.conf; /meshway-vpn-test -test.run TestLinuxKernelInternetVPN -test.v -test.timeout 70s'
