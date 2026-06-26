#!/bin/sh
set -eu

DATA_DIR="${NYARELAY_DATA:-/data}"

mkdir -p "$DATA_DIR"
chown -R nyarelay:nyarelay "$DATA_DIR"

exec su-exec nyarelay /usr/local/bin/nyarelay-controller "$@"
