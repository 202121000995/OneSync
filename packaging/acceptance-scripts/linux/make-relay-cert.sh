#!/bin/sh
set -eu

# Edit RELAY_HOSTS before running. Include the DNS name or public IP used by clients.
RELAY_HOSTS=${RELAY_HOSTS:-relay.example.com,203.0.113.10}

mkdir -p certs

./onesync-cert -hosts "$RELAY_HOSTS" -cert certs/relay.crt -key certs/relay.key

printf '\nGenerated certs/relay.crt and certs/relay.key\n'
printf 'Copy certs/relay.crt to source and target computers when using Relay.\n'
