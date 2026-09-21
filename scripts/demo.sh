#!/bin/sh
# One-terminal demo: start an Arena server in the background, play on it from
# this terminal, and stop it again when you quit. Run it with `make demo`, which
# builds bin/arena first. PORT=9001 make demo uses another port.
#
# The server waits only 3 seconds for an opponent before a bot joins, so a
# single player is never left waiting. Its log is bin/arena.log.
set -eu
cd "$(dirname "$0")/.."
. scripts/lib.sh

if [ ! -x bin/arena ]; then
	echo "demo: bin/arena is missing; run this as: make demo" >&2
	exit 1
fi
if [ ! -t 0 ]; then
	echo "demo: this needs a terminal, but standard input is not one" >&2
	exit 1
fi

if ! command -v nc >/dev/null 2>&1; then
	echo "demo: nc (netcat) is not installed" >&2
	exit 1
fi

port=${PORT:-9000}
# Some systems let a second server share a port with the first, so ask first
# instead of relying on the bind failing.
if nc -z 127.0.0.1 "$port" >/dev/null 2>&1; then
	echo "demo: something is already listening on port $port; pick another, e.g. PORT=9001 make demo" >&2
	exit 1
fi

log=bin/arena.log
: >"$log"

./bin/arena -addr "127.0.0.1:$port" -fill-wait 3s >>"$log" 2>&1 &
server=$!

# However the demo ends, stop the server the way Ctrl-C would (it shuts down
# cleanly), wait for it, and say where its log is.
cleanup() {
	kill -INT "$server" 2>/dev/null || true
	wait "$server" 2>/dev/null || true
	echo "server stopped; its log is $log"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

wait_listening "$log" "$server"
sh scripts/play.sh 127.0.0.1 "$port"
