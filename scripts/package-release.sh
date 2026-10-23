#!/bin/sh
# Package a picoclaw-zo release archive from already-built binaries.
#
# Layout matches the previous picoclaw-afj p3/p4 releases:
#   picoclaw[.exe] / picoclaw-launcher[.exe] / BUILD-INFO.txt
# Archive name:
#   build/picoclaw-zo-<version>-<platform>-<arch>.{tar.gz,zip}
#
# Usage:
#   sh scripts/package-release.sh linux amd64
#   sh scripts/package-release.sh windows amd64
#
# Binaries are expected at:
#   build/picoclaw-<platform>-<arch>[.exe]
#   build/picoclaw-launcher-<platform>-<arch>[.exe]
set -e

platform=${1:?usage: package-release.sh <platform> <arch>}
arch=${2:?usage: package-release.sh <platform> <arch>}

cd "$(dirname "$0")/.." || exit 1

version=$(sh scripts/zo-version.sh)
commit=$(git rev-parse --short=8 HEAD)
branch=$(git rev-parse --abbrev-ref HEAD)
build_time=$(date +%FT%T%z)
go_bin=$(command -v go 2>/dev/null || true)
if [ -z "$go_bin" ] && [ -n "${GOROOT:-}" ] && [ -x "$GOROOT/bin/go" ]; then
	go_bin=$GOROOT/bin/go
fi
if [ -n "$go_bin" ]; then
	go_version=$("$go_bin" env GOVERSION 2>/dev/null || echo unknown)
else
	go_version=unknown
fi
upstream_base=$(printf '%s' "$version" | sed 's/+zo\..*//')

ext=
archive_ext=tar.gz
case "$platform" in
windows) ext=.exe; archive_ext=zip ;;
esac

binary=build/picoclaw-${platform}-${arch}${ext}
launcher=build/picoclaw-launcher-${platform}-${arch}${ext}
release_name=picoclaw-zo-${version}-${platform}-${arch}

for f in "$binary" "$launcher"; do
	[ -f "$f" ] || { echo "missing artifact: $f" >&2; exit 1; }
done

stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT

cp "$binary" "$stage/picoclaw${ext}"
cp "$launcher" "$stage/picoclaw-launcher${ext}"

cat > "$stage/BUILD-INFO.txt" <<EOF
project: picoclaw-zo
version: ${version}
upstream_base: ${upstream_base}
commit: ${commit}
branch: ${branch}
platform: ${platform}/${arch}
go_version: ${go_version}
build_time: ${build_time}
EOF

mkdir -p build
out="$(pwd)/build/${release_name}.${archive_ext}"
rm -f "$out"

case "$archive_ext" in
tar.gz)
	tar -C "$stage" -czf "$out" picoclaw picoclaw-launcher BUILD-INFO.txt
	;;
zip)
	if command -v zip >/dev/null 2>&1; then
		( cd "$stage" && zip -q -X "$out" picoclaw.exe picoclaw-launcher.exe BUILD-INFO.txt )
	else
		python3 - "$stage" "$out" <<'PY'
import os
import sys
import zipfile

stage, out = sys.argv[1], sys.argv[2]
with zipfile.ZipFile(out, "w", zipfile.ZIP_DEFLATED) as z:
    for name in ("picoclaw.exe", "picoclaw-launcher.exe", "BUILD-INFO.txt"):
        z.write(os.path.join(stage, name), arcname=name)
PY
	fi
	;;
esac

echo "archive: $out"
if command -v sha256sum >/dev/null 2>&1; then
	sha256sum "$out"
fi
