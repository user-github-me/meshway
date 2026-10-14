#!/bin/sh
# Run only with explicit authorization to administer this repository.
set -eu
project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
repository=${1:-user-github-me/meshway}
if [ "$repository" != user-github-me/meshway ]; then
  printf '%s\n' 'This policy is scoped to the owner-authorized Meshway repository.' >&2
  exit 1
fi
gh api --method PUT "repos/$repository/vulnerability-alerts" >/dev/null
gh api --method PUT "repos/$repository/automated-security-fixes" >/dev/null
gh api --method PUT "repos/$repository/private-vulnerability-reporting" >/dev/null
gh api --method PATCH "repos/$repository" -F allow_squash_merge=true -F allow_merge_commit=false -F allow_rebase_merge=false -F delete_branch_on_merge=true >/dev/null
gh api --method PUT "repos/$repository/branches/main/protection" --input "$project_dir/config/github-branch-protection.json" >/dev/null
# Replace the existing named policy instead of creating duplicate rulesets.
rule_id=$(gh api "repos/$repository/rulesets" --jq '.[] | select(.name == "Maintainer release tags") | .id')
if [ -n "$rule_id" ]; then
  gh api --method PUT "repos/$repository/rulesets/$rule_id" --input "$project_dir/config/github-release-rules.json" >/dev/null
else
  gh api --method POST "repos/$repository/rulesets" --input "$project_dir/config/github-release-rules.json" >/dev/null
fi
printf '%s\n' 'Applied main review/check protection, release tag protection and private security reporting.'
