#!/bin/sh
set -eu
if [ "$(uname -s)" != Darwin ] || [ "$#" != 2 ]; then
  printf '%s\n' 'Usage: scripts/export-ios.sh SIGNED_ARCHIVE_PATH OWNER_EXPORT_OPTIONS_PLIST' >&2
  exit 1
fi
archive=$1
options=$2
if [ ! -d "$archive" ] || [ ! -f "$options" ]; then
  printf '%s\n' 'Provide an existing owner-signed archive and explicit export options.' >&2
  exit 1
fi
project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
plutil -lint "$options" >/dev/null
xcodebuild -exportArchive -archivePath "$archive" -exportOptionsPlist "$options" -exportPath "$project_dir/dist/ios-export"
printf '%s\n' 'Exported the owner-signed iOS archive to dist/ios-export.'
