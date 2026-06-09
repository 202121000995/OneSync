#!/bin/sh
set -eu

# Run this on the source computer. OneSync loads a source TLS certificate automatically.
mkdir -p data/source logs

./onesync \
  -data-dir data/source \
  -log-file logs/source.log \
  -sync-interval 10s
