#!/bin/sh
set -eu

REPO=${ONESYNC_REPO:-202121000995/OneSync}
RELAY_PORT=${RELAY_PORT:-443}
ACTION=${1:-install}
GH_PROXY=${GH_PROXY:-}
DEFAULT_RELEASE_TAG=${ONESYNC_DEFAULT_RELEASE_TAG:-v1.13}
RELEASE_TAG=${ONESYNC_RELEASE_TAG:-${RELEASE_TAG:-}}
PACKAGE_URL=${ONESYNC_LINUX_PACKAGE_URL:-${PACKAGE_URL:-}}

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
	if [ -n "$PACKAGE_URL" ]; then
		printf '%s\n' "$PACKAGE_URL"
		return
	fi
	RELEASE_TAG=${RELEASE_TAG:-$DEFAULT_RELEASE_TAG}
	if [ -n "$RELEASE_TAG" ]; then
		case "$RELEASE_TAG" in
			acceptance-*)
				release_commit=${RELEASE_TAG#acceptance-}
				printf 'https://github.com/%s/releases/download/%s/onesync-acceptance-linux-amd64-%s.tar.gz\n' "$REPO" "$RELEASE_TAG" "$release_commit"
				;;
			*)
				printf 'https://github.com/%s/releases/download/%s/onesync-linux-amd64-%s.tar.gz\n' "$REPO" "$RELEASE_TAG" "$RELEASE_TAG"
				;;
		esac
		return
	fi
	api="https://api.github.com/repos/$REPO/releases/latest"
	curl -fsSL "$(proxy_url "$api")" |
		sed -n 's/.*"browser_download_url": "\(.*onesync.*linux-amd64.*\.tar\.gz\)".*/\1/p' |
		head -n 1
}

proxy_url() {
	url=$1
	if [ -z "$GH_PROXY" ]; then
		printf '%s' "$url"
		return
	fi
	case "$url" in
		"$GH_PROXY"*)
			printf '%s' "$url"
			return
			;;
	esac
	case "$GH_PROXY" in
		*/)
			printf '%s%s' "$GH_PROXY" "$url"
			;;
		*)
			printf '%s/%s' "$GH_PROXY" "$url"
			;;
	esac
}

install_relay() {
	if [ -z "${RELAY_HOSTS:-}" ]; then
		printf 'RELAY_HOSTS is required. Example:\n' >&2
		printf '  curl -fsSL https://raw.githubusercontent.com/%s/main/packaging/acceptance-scripts/linux/deploy-relaytls.sh | sudo env RELAY_HOSTS=relay.example.com RELAY_PORT=443 RELAY_TOKEN=your-secret sh\n' "$REPO" >&2
		printf '  curl -fsSL https://gh-proxy.org/https://raw.githubusercontent.com/%s/main/packaging/acceptance-scripts/linux/deploy-relaytls.sh | sudo env RELAY_HOSTS=relay.example.com RELAY_PORT=443 RELAY_TOKEN=your-secret RELEASE_TAG=v1.13 GH_PROXY=https://gh-proxy.org/ sh\n' "$REPO" >&2
		printf '  With BT/1Panel certificate paths: sudo env RELAY_HOSTS=relay.example.com ONESYNC_RELAY_CERT=/path/fullchain.pem ONESYNC_RELAY_KEY=/path/privkey.pem ... sh\n' >&2
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
		printf 'If GitHub API is blocked by the proxy, retry with RELEASE_TAG=v1.13 or PACKAGE_URL=https://...tar.gz.\n' >&2
		exit 1
	fi

	download_url=$(proxy_url "$url")
	printf 'Downloading OneSync Linux package:\n%s\n' "$download_url"
	curl -fL "$download_url" -o "$tmp/onesync-linux.tar.gz"
	tar -xzf "$tmp/onesync-linux.tar.gz" -C "$tmp"
	stage=$(find "$tmp" -maxdepth 1 -type d \( -name 'onesync-linux-amd64-*' -o -name 'onesync-acceptance-linux-amd64-*' \) | head -n 1)
	if [ -z "$stage" ]; then
		printf 'Downloaded package has no Linux stage directory.\n' >&2
		exit 1
	fi

	printf 'Installing Relay TLS server...\n'
	RELAY_HOSTS=$RELAY_HOSTS \
	RELAY_PORT=$RELAY_PORT \
	RELAY_TOKEN=${RELAY_TOKEN:-} \
	ONESYNC_RELAY_CERT=${ONESYNC_RELAY_CERT:-} \
	ONESYNC_RELAY_KEY=${ONESYNC_RELAY_KEY:-} \
	"$stage/onesync-relayctl" install

	systemctl restart onesync-relay.service
	systemctl status onesync-relay.service --no-pager

	printf '\nRelay info for OneSync link:\n'
	/usr/local/bin/onesync-relayctl info
	printf '\nRelay admin panel:\n'
	printf '  http://<server-ip-or-domain>:8766\n'
	printf '  First visit sets the admin account and password.\n'
	printf '\nCommon menu command:\n'
	printf '  onesyncr\n'
	printf '\nCommon commands:\n'
	printf '  sudo onesync-relayctl status\n'
	printf '  sudo onesync-relayctl logs\n'
	printf '  sudo onesync-relayctl info\n'
	printf '  sudo onesync-relayctl token\n'
	printf '  sudo onesync-relayctl rotate-token\n'
	printf '  sudo onesync-relayctl cert\n'
	printf '  sudo onesync-relayctl cert-info\n'
	printf '  sudo onesync-relayctl set-cert /path/fullchain.pem /path/privkey.pem\n'
	printf '  sudo RELAY_HOSTS=relay.example.com RELAY_PORT=%s onesync-relayctl regen-cert\n' "$RELAY_PORT"
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
		GH_PROXY=$GH_PROXY RELEASE_TAG=$RELEASE_TAG PACKAGE_URL=$PACKAGE_URL onesync-relayctl upgrade
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
