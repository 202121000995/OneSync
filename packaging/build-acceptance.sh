#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
OUT=${1:-"$ROOT/dist/acceptance"}
GO_BIN=${GO_BIN:-go}
CACHE_ROOT=${ONESYNC_BUILD_CACHE:-"${TMPDIR:-/tmp}/onesync-acceptance-build-cache"}

mkdir -p "$OUT"
mkdir -p "$CACHE_ROOT/gocache" "$CACHE_ROOT/gomodcache"
export GOCACHE="$CACHE_ROOT/gocache"
export GOMODCACHE="$CACHE_ROOT/gomodcache"

commit=$(
	cd "$ROOT"
	git rev-parse --short HEAD 2>/dev/null || printf unknown
)
version=${ONESYNC_VERSION:-}
if [ -z "$version" ] && [ -f "$ROOT/VERSION" ]; then
	version=$(tr -d '[:space:]' < "$ROOT/VERSION")
fi
if [ -z "$version" ]; then
	version=$commit
fi

build() {
	goos=$1
	goarch=$2
	output=$3
	package=$4
	ldflags=${5:-}
	printf 'building %s\n' "$output"
	if [ -n "$ldflags" ]; then
		GOOS=$goos GOARCH=$goarch "$GO_BIN" build -trimpath -ldflags "$ldflags" -o "$OUT/$output" "$package"
	else
		GOOS=$goos GOARCH=$goarch "$GO_BIN" build -trimpath -o "$OUT/$output" "$package"
	fi
}

cd "$ROOT"

build windows amd64 onesync-windows-amd64.exe ./cmd/onesync "-H windowsgui -X main.version=$version"
build windows amd64 onesync-cert-windows-amd64.exe ./cmd/onesync-cert
build linux amd64 onesync-linux-amd64 ./cmd/onesync "-X main.version=$version"
build linux amd64 onesync-cert-linux-amd64 ./cmd/onesync-cert
build linux amd64 onesync-relay-linux-amd64 ./cmd/relay

{
	printf 'version=%s\n' "$version"
	printf 'commit=%s\n' "$commit"
	printf 'go=%s\n' "$("$GO_BIN" version)"
	printf 'output=%s\n' "$OUT"
} > "$OUT/BUILD.txt"

files='BUILD.txt
onesync-windows-amd64.exe
onesync-cert-windows-amd64.exe
onesync-linux-amd64
onesync-cert-linux-amd64
onesync-relay-linux-amd64'

if command -v shasum >/dev/null 2>&1; then
	(
		cd "$OUT"
		printf '%s\n' "$files" | xargs env LC_ALL=C LANG=C shasum -a 256
	) > "$OUT/SHA256SUMS.txt"
elif command -v sha256sum >/dev/null 2>&1; then
	(
		cd "$OUT"
		printf '%s\n' "$files" | xargs env LC_ALL=C LANG=C sha256sum
	) > "$OUT/SHA256SUMS.txt"
else
	printf 'shasum or sha256sum is required to write SHA256SUMS.txt\n' >&2
	exit 1
fi

printf 'acceptance artifacts written to %s\n' "$OUT"
printf 'checksums written to %s\n' "$OUT/SHA256SUMS.txt"
