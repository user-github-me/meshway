#!/bin/sh
set -eu
if [ "$#" -lt 2 ]; then
  printf '%s\n' 'Usage: scripts/validate-android.sh EXPLICIT_ADB_SERIAL APK [ADB_BINARY]' >&2; exit 1
fi
serial=$1
apk=$2
adb=${3:-adb}
test -f "$apk"
"$adb" -s "$serial" get-state >/dev/null
"$adb" -s "$serial" install -r "$apk" >/dev/null
"$adb" -s "$serial" shell am start -W -n network.meshway.android/.MainActivity >/dev/null
"$adb" -s "$serial" shell pidof network.meshway.android >/dev/null
printf '%s\n' 'Android install and foreground startup passed.' 'For actual VPN validation use an emulator or a disposable device: grant VpnService consent, run the encrypted kernel ICMP lab, then check DNS, disconnect, sleep/wake and mobile-network changes as documented in docs/VALIDATION.md.'
# Leave the explicitly selected app in its normal screen; do not grant VPN consent automatically.
