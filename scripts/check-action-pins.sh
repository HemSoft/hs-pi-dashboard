#!/usr/bin/env bash
# check-action-pins.sh — regression guard for CI action pins.
#
# GitHub resolves `uses: owner/repo@<sha>` BEFORE any step runs, so a
# mistyped SHA kills every job with "unable to find version <sha>" and no
# in-workflow step can ever catch it. This script runs outside that phase:
# for every `uses:` pin it compares the pinned SHA against the real upstream
# SHA of the tag named in the trailing comment (`# vX.Y.Z`).
#
# Exits 1 if any pin mismatches. Usage: scripts/check-action-pins.sh [workflow-file]
set -uo pipefail
wf="${1:-.github/workflows/ci.yml}"
rc=0
while IFS= read -r line; do
  ref="${line#*uses:}"
  ref="${ref%%#*}"
  ref="$(printf '%s' "$ref" | tr -d '[:space:]')"
  repo="${ref%%@*}"
  pin="${ref#*@}"
  tag="$(printf '%s' "$line" | sed -n 's/.*#[[:space:]]*\(v[0-9][0-9.]*\).*/\1/p')"
  if [ -z "$tag" ]; then
    echo "SKIP $repo@$pin (no tag comment to verify against)"
    continue
  fi
  actual="$(git ls-remote --tags "https://github.com/$repo" "refs/tags/$tag" | awk 'NR==1{print $1}')"
  if [ "$pin" = "$actual" ]; then
    echo "OK   $repo@$pin == $tag"
  else
    echo "RED  $repo@$pin != upstream $tag ($actual)"
    rc=1
  fi
done < <(grep -E '^[[:space:]]*uses:' "$wf")
exit "$rc"
