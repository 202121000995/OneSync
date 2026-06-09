#!/bin/sh
set -eu

# Run this on the Relay computer after make-relay-cert.sh.
mkdir -p logs

./onesync-relay \
  -listen :7443 \
  -cert certs/relay.crt \
  -key certs/relay.key \
  -log-file logs/relay.log
