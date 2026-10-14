#!/bin/sh
set -eu
if [ "$#" -lt 4 ] || [ "$#" -gt 5 ]; then
  printf '%s\n' 'Usage: scripts/package-linux-lab.sh TRUSTED_ALPINE_IMAGE LINUX_BINARY VERSION x86_64|aarch64 [RPM_RELEASE]' >&2; exit 1
fi
project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
output_dir=${MESHWAY_RELEASE_DIR:-"$project_dir/release"}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT HUP INT TERM
mkdir -p "$work/scripts" "$work/deploy/systemd" "$work/release" "$output_dir"
cp "$project_dir/scripts/package-rpm.sh" "$work/scripts/"
cp "$project_dir/deploy/meshway.spec" "$work/deploy/"
cp "$project_dir/deploy/systemd/meshway.service" "$work/deploy/systemd/"
cp "$project_dir/LICENSE" "$work/"
cp "$project_dir/THIRD_PARTY_NOTICES.txt" "$work/"
cp "$2" "$work/meshway"
if [ -n "${MESHWAY_ALPINE_MIRROR:-}" ]; then
 python3 - "$MESHWAY_ALPINE_MIRROR" "$work/alpine-mirror" <<'PY'
import sys
from pathlib import Path
from urllib.parse import urlsplit
url=urlsplit(sys.argv[1])
if url.scheme!='https' or not url.hostname or url.username or url.password or url.query or url.fragment:
    raise SystemExit('Use an explicit HTTPS Alpine mirror without credentials/query/fragment.')
if any(ch not in 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789:/._-' for ch in sys.argv[1]):
    raise SystemExit('Use a plain HTTPS Alpine mirror URL without shell/sed metacharacters.')
Path(sys.argv[2]).write_text(sys.argv[1].rstrip('/')+'\n')
PY
fi
case "$4" in x86_64) platform=linux/amd64;; aarch64) platform=linux/arm64;; *) printf '%s\n' 'Unsupported RPM architecture.' >&2; exit 1;; esac
# Signed Alpine packages supply the GNU tools rpmbuild needs; no host install.
docker run --rm --platform "$platform" --user 0:0 -v "$work:/source" --entrypoint /bin/sh "$1" \
  -c 'set -eu; if [ -f /source/alpine-mirror ]; then mirror=$(cat /source/alpine-mirror); sed "s@https://dl-cdn.alpinelinux.org/alpine@$mirror@g" /etc/apk/repositories > /tmp/meshway-repositories; cp /tmp/meshway-repositories /etc/apk/repositories; fi; apk --timeout 20 add --no-cache rpm file binutils findutils grep coreutils >/dev/null; sh /source/scripts/package-rpm.sh /source/meshway "$1" "$2" "$3"; rpm -qpi /source/release/*.rpm' -- "$3" "$4" "${5:-1}"
find "$work/release" -name '*.rpm' -exec cp {} "$output_dir/" \;
