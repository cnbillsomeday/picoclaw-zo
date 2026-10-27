#!/bin/sh
# Print the picoclaw-zo version: <upstream-base>+zo.<N>
#
# base is the newest upstream release tag (vX.Y.Z) reachable from HEAD, so the
# fork always advertises the upstream version it is aligned with.
#
# N is monotonic across upstream base bumps: it starts from the highest
# "+zo.<N>" tag reachable from HEAD and adds the local commits made since that
# tag that did not come from the upstream base. Bumping the base (for example
# merging v0.5.2 into the fork) therefore never resets N to zero; it only adds
# the merge itself plus whatever was committed on top.
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

max_n=0
max_tag=
for t in $(git tag --merged HEAD 2>/dev/null | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+\+zo\.[0-9]+$' || true); do
	n=${t##*+zo.}
	if [ "$n" -gt "$max_n" ]; then
		max_n=$n
		max_tag=$t
	fi
done

if [ -n "$max_tag" ]; then
	count=$(git rev-list --count "$max_tag..HEAD" --not "$base" 2>/dev/null || echo 0)
	n=$((max_n + count))
else
	n=$(git rev-list --count "$base..HEAD" 2>/dev/null || echo 0)
fi

dirty=
if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
	dirty=.dirty
fi

printf '%s+zo.%s%s\n' "$base" "$n" "$dirty"
