#!/bin/sh
set -eu
if [ "$#" -lt 3 ] || [ "$#" -gt 4 ]; then
  printf '%s\n' 'Usage: scripts/package-rpm.sh LINUX_BINARY VERSION x86_64|aarch64 [RPM_RELEASE]' >&2; exit 1
fi
case "$2" in *[!0-9.]*|'') printf '%s\n' 'RPM version must be numeric dotted version.' >&2; exit 1;; esac
case "$3" in x86_64|aarch64) ;; *) exit 1;; esac
rpm_release=${4:-1}
case "$rpm_release" in *[!A-Za-z0-9.]*|'') printf '%s\n' 'RPM release must be alphanumeric dotted text.' >&2; exit 1;; esac
project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT HUP INT TERM
mkdir -p "$work/SOURCES" "$work/BUILD" "$work/BUILDROOT" "$work/RPMS" "$work/SRPMS" "$work/SPECS" "$project_dir/release"
cp "$1" "$work/SOURCES/meshway"
cp "$project_dir/deploy/systemd/meshway.service" "$work/SOURCES/"
cp "$project_dir/LICENSE" "$work/SOURCES/"
cp "$project_dir/THIRD_PARTY_NOTICES.txt" "$work/SOURCES/"
cp "$project_dir/deploy/meshway.spec" "$work/SPECS/"
rpmbuild --target "$3" --define "_topdir $work" --define "meshway_version $2" --define "meshway_release $rpm_release" --define "meshway_arch $3" --define '_build_id_links none' -bb "$work/SPECS/meshway.spec"
find "$work/RPMS" -name '*.rpm' -exec cp {} "$project_dir/release/" \;
printf '%s\n' 'Created RPM artifacts locally; nothing was installed.'
