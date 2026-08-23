#!/bin/sh
set -eu

DATA_DIR="${NYARELAY_DATA:-/data}"
SECRETS_KEY_SOURCE="${NYARELAY_SECRETS_KEY_FILE:-}"

mkdir -p "$DATA_DIR"
chown -R nyarelay:nyarelay "$DATA_DIR"

if [ -n "$SECRETS_KEY_SOURCE" ]; then
	if [ ! -r "$SECRETS_KEY_SOURCE" ]; then
		echo "controller secrets key file is not readable: $SECRETS_KEY_SOURCE" >&2
		exit 1
	fi
	secrets_key_copy="$(mktemp /tmp/nyarelay-secrets.XXXXXX)"
	cat "$SECRETS_KEY_SOURCE" > "$secrets_key_copy"
	chown nyarelay:nyarelay "$secrets_key_copy"
	chmod 0400 "$secrets_key_copy"
	export NYARELAY_SECRETS_KEY_FILE="$secrets_key_copy"
fi

exec su-exec nyarelay /usr/local/bin/nyarelay-controller "$@"
