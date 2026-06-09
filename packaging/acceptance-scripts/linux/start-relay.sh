#!/bin/sh
set -eu

# Run this on the Relay computer after make-relay-cert.sh.
mkdir -p logs
RELAY_TOKEN=${RELAY_TOKEN:-}

token_args=
if [ -n "$RELAY_TOKEN" ]; then
  token_args="-access-token $RELAY_TOKEN"
fi

./onesync-relay \
  -listen :7443 \
  -cert certs/relay.crt \
  -key certs/relay.key \
  $token_args \
  -log-file logs/relay.log
