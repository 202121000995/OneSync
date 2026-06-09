#!/bin/sh
set -eu

REPO=${ONESYNC_REPO:-202121000995/OneSync}
RELAY_PORT=${RELAY_PORT:-443}
ACTION=${1:-install}

need_root() {
	if [ "$(id -u)" -ne 0 ]; then
		printf 'Please run this script with sudo.\n' >&2
		exit 1
	fi
}

need_command() {
	if ! command -v "$1" >/dev/null 2>&1; then
		printf '%s is required.\n' "$1" >&2
		exit 1
	fi
}

latest_linux_package_url() {
	api="https://api.github.com/repos/$REPO/releases/latest"
	curl -fsSL "$api" |
		sed -n 's/.*"browser_download_url": "\(.*onesync-acceptance-linux-amd64.*\.tar\.gz\)".*/\1/p' |
		head -n 1
}

install_relay() {
	if [ -z "${RELAY_HOSTS:-}" ]; then
		printf 'RELAY_HOSTS is required. Example:\n' >&2
		printf '  curl -fsSL https://raw.githubusercontent.com/%s/main/packaging/acceptance-scripts/linux/deploy-relaytls.sh | sudo env RELAY_HOSTS=1.2.3.4 RELAY_PORT=443 sh\n' "$REPO" >&2
		exit 1
	fi

	need_command curl
	need_command tar
	need_command systemctl

	tmp=${TMPDIR:-/tmp}/onesync-relaytls-deploy-$$
	mkdir -p "$tmp"
	trap 'rm -rf "$tmp"' EXIT

	url=$(latest_linux_package_url)
	if [ -z "$url" ]; then
		printf 'Cannot find latest OneSync Linux package from GitHub repo %s.\n' "$REPO" >&2
		exit 1
	fi

	printf 'Downloading OneSync Linux package:\n%s\n' "$url"
	curl -fL "$url" -o "$tmp/onesync-linux.tar.gz"
	tar -xzf "$tmp/onesync-linux.tar.gz" -C "$tmp"
	stage=$(find "$tmp" -maxdepth 1 -type d -name 'onesync-acceptance-linux-amd64-*' | head -n 1)
	if [ -z "$stage" ]; then
		printf 'Downloaded package has no Linux stage directory.\n' >&2
		exit 1
	fi

	printf 'Installing Relay TLS server...\n'
	RELAY_HOSTS=$RELAY_HOSTS \
	RELAY_PORT=$RELAY_PORT \
	ONESYNC_RELAY_CERT=${ONESYNC_RELAY_CERT:-} \
	ONESYNC_RELAY_KEY=${ONESYNC_RELAY_KEY:-} \
	"$stage/onesync-relayctl" install

	systemctl restart onesync-relay.service
	systemctl status onesync-relay.service --no-pager

	first_host=$(printf '%s' "$RELAY_HOSTS" | cut -d, -f1)
	printf '\nRelay TLS address for OneSync link:\n%s:%s\n' "$first_host" "$RELAY_PORT"
	printf '\nCommon commands:\n'
	printf '  sudo onesync-relayctl status\n'
	printf '  sudo onesync-relayctl logs\n'
	printf '  sudo onesync-relayctl restart\n'
	printf '  sudo onesync-relayctl upgrade\n'
	printf '  sudo onesync-relayctl uninstall\n'
}

need_root

case "$ACTION" in
	install|run)
		install_relay
		;;
	upgrade)
		need_command onesync-relayctl
		onesync-relayctl upgrade
		;;
	uninstall)
		need_command onesync-relayctl
		onesync-relayctl uninstall
		;;
	*)
		printf 'Usage: deploy-relaytls.sh [install|upgrade|uninstall]\n' >&2
		exit 2
		;;
esac
