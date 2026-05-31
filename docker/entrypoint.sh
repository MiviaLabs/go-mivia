#!/bin/sh
set -eu

internal_addr="${MIVIA_INTERNAL_ADDR:-127.0.0.1:18080}"
public_port="${MIVIA_PUBLIC_PORT:-8080}"
export MIVIA_HTTP_ADDR="${internal_addr}"

shutdown() {
	if [ "${proxy_pid:-}" ]; then
		kill "$proxy_pid" 2>/dev/null || true
	fi
	if [ "${server_pid:-}" ]; then
		kill "$server_pid" 2>/dev/null || true
	fi
	wait 2>/dev/null || true
}

trap shutdown INT TERM

mivia-server &
server_pid="$!"

socat "TCP-LISTEN:${public_port},fork,reuseaddr,bind=0.0.0.0" "TCP:${internal_addr}" &
proxy_pid="$!"

while :; do
	if ! kill -0 "$server_pid" 2>/dev/null; then
		wait "$server_pid"
		exit "$?"
	fi
	if ! kill -0 "$proxy_pid" 2>/dev/null; then
		wait "$proxy_pid"
		exit "$?"
	fi
	sleep 1
done
