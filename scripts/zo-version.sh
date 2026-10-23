#!/bin/sh
# Print the picoclaw-zo version: <upstream-base>+zo.<commits-since-base>
#
# The base is the newest upstream release tag (vX.Y.Z) reachable from HEAD, so
# the fork always advertises the upstream version it is aligned with. The zo.N
# suffix is the number of commits on top of that base, which increases with
# every release without any manual bookkeeping. Tags such as "nightly" or the
# fork's own "session-header-*" tags never become the base.
#
# Examples:
#   v0.5.1+zo.23
#   v0.5.1+zo.23.dirty   (working tree has uncommitted changes)
#
# Usage: sh scripts/zo-version.sh
set -e

cd "$(dirname "$0")/.." 2>/dev/null || exit 1

base=$(git tag --merged HEAD --sort=-v:refname 2>/dev/null \
	| grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' \
	| head -n 1)
[ -n "$base" ] || base=v0.0.0

count=$(git rev-list --count "$base..HEAD" 2>/dev/null || echo 0)

dirty=
if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
	dirty=.dirty
fi

printf '%s+zo.%s%s\n' "$base" "$count" "$dirty"
