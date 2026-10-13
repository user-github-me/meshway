#!/bin/sh
set -eu
if [ "$(uname -s)" != Linux ] || [ "$(id -u)" != 0 ]; then
  printf '%s\n' 'Run this installer explicitly as root on Linux with a locally built binary.' >&2
  exit 1
fi
if [ "$#" != 1 ] || [ ! -f "$1" ]; then
  printf '%s\n' 'Usage: sudo ./deploy/install-systemd.sh /path/to/meshway-linux-ARCH' >&2
  exit 1
fi
install_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if ! id meshway >/dev/null 2>&1; then useradd --system --home /var/lib/meshway --shell /usr/sbin/nologin meshway; fi
install -m 0755 "$1" /usr/local/bin/meshway
install -d -m 0700 -o meshway -g meshway /var/lib/meshway
if [ ! -f /var/lib/meshway/config.json ]; then
  runuser -u meshway -- /usr/local/bin/meshway init --config /var/lib/meshway/config.json
fi
install -m 0644 "$install_dir/systemd/meshway.service" /etc/systemd/system/meshway.service
systemctl daemon-reload
printf '%s\n' 'Installed Meshway. Edit /var/lib/meshway/config.json to select roles and authorization.' 'Start when ready: systemctl enable --now meshway' 'No service was started automatically.'
