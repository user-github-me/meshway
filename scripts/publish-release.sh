#!/bin/sh
set -eu
if [ "$#" != 4 ]; then
  printf '%s\n' 'Usage: scripts/publish-release.sh OWNER/REPO TAG ARTIFACT_DIRECTORY PINNED_PUBLIC_KEY' >&2; exit 1
fi
repository=$1
tag=$2
artifacts=$3
public_key=$4
case "$repository" in *[!A-Za-z0-9._/-]*|*/*/*|/*|*/|'') printf '%s\n' 'Invalid explicit repository.' >&2; exit 1;; */*) ;; *) exit 1;; esac
case "$tag" in v[0-9]*.[0-9]*.[0-9]*) ;; *) printf '%s\n' 'Expected a semantic version tag.' >&2; exit 1;; esac
node scripts/release-tool.mjs verify "$artifacts" "$public_key"
# All uploads stay draft until the owner reviews and publishes on GitHub.
gh release create "$tag" "$artifacts"/* --repo "$repository" --draft --verify-tag --title "Meshway $tag" --notes-file docs/RELEASE_NOTES.md
printf '%s\n' 'Verified artifacts uploaded to an explicit repository as a draft release.'
