#!/usr/bin/env bash
# Fetch upstream afj main, rebase this branch, then build and test the fork.
#
# Usage:
#   scripts/sync-afj.sh            # rebase onto upstream/main
#   scripts/sync-afj.sh --check    # only report divergence, no rebase
set -euo pipefail

UPSTREAM_REMOTE=${UPSTREAM_REMOTE:-upstream}
BUILD_TAGS=${BUILD_TAGS:-goolm,stdjson}

cd "$(git rev-parse --show-toplevel)"

if ! git remote get-url "$UPSTREAM_REMOTE" >/dev/null 2>&1; then
  echo "adding remote $UPSTREAM_REMOTE -> https://github.com/afjcjsbx/picoclaw.git"
  git remote add "$UPSTREAM_REMOTE" https://github.com/afjcjsbx/picoclaw.git
fi

git fetch --prune "$UPSTREAM_REMOTE"

echo "== upstream =="
git log --oneline -1 "$UPSTREAM_REMOTE/main"
echo "== ours =="
git log --oneline -1 HEAD
echo "== divergence =="
git rev-list --left-right --count "$UPSTREAM_REMOTE/main"...HEAD

if [ "${1:-}" = "--check" ]; then
  exit 0
fi

echo "== rebasing onto $UPSTREAM_REMOTE/main =="
if ! git rebase "$UPSTREAM_REMOTE/main"; then
  cat <<'MSG'

Rebase stopped on conflicts. The fork only touches afj files through 3 small
patches (see patches/). Resolve, then:

  git add <files>
  git rebase --continue
  go build -tags goolm,stdjson ./...
  go test  -tags goolm,stdjson ./pkg/channels/a2a/ ./pkg/tools/ ./pkg/commands/

All other files are additive and should not conflict.
MSG
  exit 1
fi

echo "== build =="
go build -tags "$BUILD_TAGS" ./...
echo "== tests =="
go test -tags "$BUILD_TAGS" ./pkg/channels/a2a/ ./pkg/tools/ ./pkg/commands/
echo "sync ok"
