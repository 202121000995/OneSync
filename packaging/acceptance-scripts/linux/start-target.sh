#!/bin/sh
set -eu

# Copy certs/source.crt from the source computer before running this script.
mkdir -p data/target logs

./onesync \
  -ca certs/source.crt \
  -data-dir data/target \
  -log-file logs/target.log \
  -sync-interval 10s
