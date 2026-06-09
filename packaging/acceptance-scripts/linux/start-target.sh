#!/bin/sh
set -eu

mkdir -p data/target logs

./onesync \
  -data-dir data/target \
  -log-file logs/target.log \
  -sync-interval 10s
