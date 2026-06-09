#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
ARTIFACT_DIR=${1:-"$ROOT/dist/acceptance"}
PACKAGE_DIR=${2:-"$ROOT/dist/acceptance-packages"}
BUILD_SCRIPT="$ROOT/packaging/build-acceptance.sh"

mkdir -p "$PACKAGE_DIR"

"$BUILD_SCRIPT" "$ARTIFACT_DIR"

commit=$(
	cd "$ROOT"
	git rev-parse --short HEAD 2>/dev/null || printf unknown
)

windows_name="onesync-acceptance-windows-amd64-$commit"
linux_name="onesync-acceptance-linux-amd64-$commit"
windows_stage="$PACKAGE_DIR/$windows_name"
linux_stage="$PACKAGE_DIR/$linux_name"
windows_zip="$PACKAGE_DIR/$windows_name.zip"
linux_tar="$PACKAGE_DIR/$linux_name.tar.gz"

rm -rf "$windows_stage" "$linux_stage"
mkdir -p "$windows_stage" "$linux_stage"

copy_common() {
	destination=$1
	cp "$ARTIFACT_DIR/BUILD.txt" "$destination/BUILD.txt"
	cp "$ARTIFACT_DIR/SHA256SUMS.txt" "$destination/SHA256SUMS.txt"
	cp "$ROOT/packaging/quickstart.md" "$destination/quickstart.md"
	cp "$ROOT/packaging/acceptance-report.md" "$destination/acceptance-report.md"
	cp "$ROOT/packaging/preflight-checklist.md" "$destination/preflight-checklist.md"
}

copy_common "$windows_stage"
cp "$ARTIFACT_DIR/onesync-windows-amd64.exe" "$windows_stage/OneSync.exe"
cp "$ARTIFACT_DIR/onesync-cert-windows-amd64.exe" "$windows_stage/onesync-cert.exe"
cp "$ROOT/packaging/icons/OneSync.ico" "$windows_stage/OneSync.ico"
cp "$ROOT"/packaging/acceptance-scripts/windows/*.cmd "$windows_stage/"
cp "$ROOT"/packaging/acceptance-scripts/windows/*.ps1 "$windows_stage/"

copy_common "$linux_stage"
cp "$ARTIFACT_DIR/onesync-linux-amd64" "$linux_stage/onesync"
cp "$ARTIFACT_DIR/onesync-cert-linux-amd64" "$linux_stage/onesync-cert"
cp "$ARTIFACT_DIR/onesync-relay-linux-amd64" "$linux_stage/onesync-relay"
cp "$ROOT"/packaging/acceptance-scripts/linux/* "$linux_stage/"
chmod +x "$linux_stage"/*.sh "$linux_stage"/onesyncctl "$linux_stage"/onesync-relayctl "$linux_stage"/onesync-menu

if ! command -v zip >/dev/null 2>&1; then
	printf 'zip is required to create the Windows acceptance package\n' >&2
	exit 1
fi
if ! command -v tar >/dev/null 2>&1; then
	printf 'tar is required to create the Linux acceptance package\n' >&2
	exit 1
fi

rm -f "$windows_zip" "$linux_tar"
(
	cd "$PACKAGE_DIR"
	zip -qr "$windows_zip" "$windows_name"
	env LC_ALL=C LANG=C tar -czf "$linux_tar" "$linux_name"
)

{
	printf 'commit=%s\n' "$commit"
	printf 'artifact_dir=%s\n' "$ARTIFACT_DIR"
	printf 'package_dir=%s\n' "$PACKAGE_DIR"
	printf '\npackages:\n'
	printf '%s\n' "- $(basename "$windows_zip")"
	printf '  Use on Windows source or target computers.\n'
	printf '%s\n' "- $(basename "$linux_tar")"
	printf '  Use on Linux source, target, or Relay computers.\n'
} > "$PACKAGE_DIR/MANIFEST.txt"

package_files="MANIFEST.txt
$(basename "$windows_zip")
$(basename "$linux_tar")"

if command -v shasum >/dev/null 2>&1; then
	(
		cd "$PACKAGE_DIR"
		printf '%s\n' "$package_files" | xargs env LC_ALL=C LANG=C shasum -a 256
	) > "$PACKAGE_DIR/PACKAGE-SHA256SUMS.txt"
elif command -v sha256sum >/dev/null 2>&1; then
	(
		cd "$PACKAGE_DIR"
		printf '%s\n' "$package_files" | xargs env LC_ALL=C LANG=C sha256sum
	) > "$PACKAGE_DIR/PACKAGE-SHA256SUMS.txt"
else
	printf 'shasum or sha256sum is required to write PACKAGE-SHA256SUMS.txt\n' >&2
	exit 1
fi

printf 'acceptance packages written to %s\n' "$PACKAGE_DIR"
printf 'package checksums written to %s\n' "$PACKAGE_DIR/PACKAGE-SHA256SUMS.txt"
