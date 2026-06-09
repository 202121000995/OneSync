#!/bin/sh
set -eu

# Edit RELAY_HOSTS before running. Include the DNS name or public IP used by clients.
RELAY_HOSTS=${RELAY_HOSTS:-relay.example.com}

mkdir -p certs

./onesync-cert -hosts "$RELAY_HOSTS" -cert certs/relay.crt -key certs/relay.key

printf '\nGenerated certs/relay.crt and certs/relay.key\n'
printf 'When using the management page link workflow, the Relay public certificate is carried in the synchronization link.\n'
