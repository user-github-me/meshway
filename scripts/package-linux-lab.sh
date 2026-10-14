#!/bin/sh
set -eu
if [ "$#" -lt 4 ] || [ "$#" -gt 5 ]; then
  printf '%s\n' 'Usage: scripts/package-linux-lab.sh TRUSTED_ALPINE_IMAGE LINUX_BINARY VERSION x86_64|aarch64 [RPM_RELEASE]' >&2; exit 1
fi
project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT HUP INT TERM
mkdir -p "$work/scripts" "$work/deploy/systemd" "$work/release" "$project_dir/release"
cp "$project_dir/scripts/package-rpm.sh" "$work/scripts/"
cp "$project_dir/deploy/meshway.spec" "$work/deploy/"
cp "$project_dir/deploy/systemd/meshway.service" "$work/deploy/systemd/"
cp "$project_dir/LICENSE" "$work/"
cp "$2" "$work/meshway"
case "$4" in x86_64) platform=linux/amd64;; aarch64) platform=linux/arm64;; *) printf '%s\n' 'Unsupported RPM architecture.' >&2; exit 1;; esac
# Signed Alpine packages supply the GNU tools rpmbuild needs; no host install.
docker run --rm --platform "$platform" --user 0:0 -v "$work:/source" --entrypoint /bin/sh "$1" \
  -c 'set -eu; apk add --no-cache rpm file binutils findutils grep coreutils >/dev/null; sh /source/scripts/package-rpm.sh /source/meshway "$1" "$2" "$3"; rpm -qpi /source/release/*.rpm' -- "$3" "$4" "${5:-1}"
find "$work/release" -name '*.rpm' -exec cp {} "$project_dir/release/" \;
