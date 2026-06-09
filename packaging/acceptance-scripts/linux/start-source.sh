#!/bin/sh
set -eu

# Run this on the source computer after make-source-cert.sh.
mkdir -p data/source logs

./onesync \
  -cert certs/source.crt \
  -key certs/source.key \
  -ca certs/source.crt \
  -data-dir data/source \
  -log-file logs/source.log \
  -sync-interval 10s
