#!/bin/sh
set -eu

REPO=${ONESYNC_REPO:-202121000995/OneSync}
ACTION=${1:-install}
GH_PROXY=${GH_PROXY:-}
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

latest_linux_package_url() {
	if [ -n "$PACKAGE_URL" ]; then
		printf '%s\n' "$PACKAGE_URL"
		return
	fi
	if [ -n "$RELEASE_TAG" ]; then
		release_commit=${RELEASE_TAG#acceptance-}
		printf 'https://github.com/%s/releases/download/%s/onesync-acceptance-linux-amd64-%s.tar.gz\n' "$REPO" "$RELEASE_TAG" "$release_commit"
		return
	fi
	api="https://api.github.com/repos/$REPO/releases/latest"
	curl -fsSL "$(proxy_url "$api")" |
		sed -n 's/.*"browser_download_url": "\(.*onesync-acceptance-linux-amd64.*\.tar\.gz\)".*/\1/p' |
		head -n 1
}

install_client() {
	need_command curl
	need_command tar
	need_command systemctl

	tmp=${TMPDIR:-/tmp}/onesync-client-deploy-$$
	mkdir -p "$tmp"
	trap 'rm -rf "$tmp"' EXIT

	url=$(latest_linux_package_url)
	if [ -z "$url" ]; then
		printf 'Cannot find latest OneSync Linux package from GitHub repo %s.\n' "$REPO" >&2
		printf 'If GitHub API is blocked by the proxy, retry with RELEASE_TAG=acceptance-f93bf8a.\n' >&2
		exit 1
	fi

	download_url=$(proxy_url "$url")
	printf 'Downloading OneSync Linux package:\n%s\n' "$download_url"
	curl -fL "$download_url" -o "$tmp/onesync-linux.tar.gz"
	tar -xzf "$tmp/onesync-linux.tar.gz" -C "$tmp"
	stage=$(find "$tmp" -maxdepth 1 -type d -name 'onesync-acceptance-linux-amd64-*' | head -n 1)
	if [ -z "$stage" ]; then
		printf 'Downloaded package has no Linux stage directory.\n' >&2
		exit 1
	fi

	printf 'Installing OneSync client service...\n'
	ONESYNC_PORT=${ONESYNC_PORT:-8765} \
	ONESYNC_WEB_BIND=${ONESYNC_WEB_BIND:-0.0.0.0} \
	ONESYNC_SYNC_PORT=${ONESYNC_SYNC_PORT:-7443} \
	"$stage/onesyncctl" install

	systemctl restart onesync.service
	systemctl status onesync.service --no-pager

	port=${ONESYNC_PORT:-8765}
	bind=${ONESYNC_WEB_BIND:-0.0.0.0}
	printf '\nOneSync Linux client installed.\n'
	printf 'Management page:\n'
	if [ "$bind" = "127.0.0.1" ]; then
		printf '  http://127.0.0.1:%s\n' "$port"
	else
		printf '  http://服务器IP:%s\n' "$port"
	fi
	printf '\nCommon menu command:\n'
	printf '  onesync\n'
	printf '\nCommon control commands:\n'
	printf '  sudo onesyncctl status\n'
	printf '  sudo onesyncctl logs\n'
	printf '  sudo onesyncctl restart\n'
	printf '  sudo onesyncctl upgrade\n'
	printf '  sudo onesyncctl uninstall\n'
}

need_root

case "$ACTION" in
	install|run)
		install_client
		;;
	upgrade)
		need_command onesyncctl
		GH_PROXY=$GH_PROXY RELEASE_TAG=$RELEASE_TAG PACKAGE_URL=$PACKAGE_URL onesyncctl upgrade
		;;
	uninstall)
		need_command onesyncctl
		onesyncctl uninstall
		;;
	*)
		printf 'Usage: deploy-onesync.sh [install|upgrade|uninstall]\n' >&2
		exit 2
		;;
esac
