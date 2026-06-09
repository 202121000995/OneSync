#!/bin/sh
set -eu

# Override SOURCE_HOSTS only when automatic IP detection is not enough.
# Normal source startup does not need this script; OneSync prepares source TLS automatically.
if [ -z "${SOURCE_HOSTS:-}" ]; then
	private_ips=$(
		{
			ip -o -4 addr show scope global 2>/dev/null || true
			hostname -I 2>/dev/null || true
			ifconfig 2>/dev/null || true
		} | grep -Eo '10\.[0-9.]+|192\.168\.[0-9.]+|172\.(1[6-9]|2[0-9]|3[0-1])\.[0-9.]+' | sort -u | paste -sd, -
	)
	if [ -n "$private_ips" ]; then
		SOURCE_HOSTS="$private_ips,localhost,127.0.0.1"
	else
		SOURCE_HOSTS="localhost,127.0.0.1"
	fi
fi

mkdir -p certs

printf 'Source certificate hosts: %s\n' "$SOURCE_HOSTS"
./onesync-cert -hosts "$SOURCE_HOSTS" -cert certs/source.crt -key certs/source.key

printf '\nGenerated certs/source.crt and certs/source.key\n'
printf 'The generated synchronization link will carry certs/source.crt to the target.\n'
printf 'Do not copy certs/source.key.\n'
