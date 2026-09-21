#!/bin/sh
# Run the load test against a fresh server built with the race detector, so a
# data race under load fails the run. Run it with `make loadtest`.
#
# Settings, as environment variables: ROOMS (default 50), DURATION (default
# 10s), PORT (default 9100). Extra arguments go to bin/loadtest, for example
# `make loadtest ARGS=-bots`. The exit status is 0 only if the load test saw no
# failures and the server reported no race.
set -eu
cd "$(dirname "$0")/.."
. scripts/lib.sh

mkdir -p bin
go build -race -o bin/arena-race ./cmd/arena
go build -o bin/loadtest ./cmd/loadtest

addr=127.0.0.1:${PORT:-9100}
log=bin/loadtest-arena.log
: >"$log"

./bin/arena-race -addr "$addr" >>"$log" 2>&1 &
server=$!
stop_server() {
	kill -INT "$server" 2>/dev/null || true
	wait "$server" 2>/dev/null || true
}
trap stop_server EXIT
trap 'exit 130' INT TERM

wait_listening "$log" "$server"

status=0
./bin/loadtest -addr "$addr" -rooms "${ROOMS:-50}" -duration "${DURATION:-10s}" "$@" || status=$?

stop_server
trap - EXIT
if grep -q 'DATA RACE' "$log"; then
	echo "loadtest: the race detector reported a data race; see $log" >&2
	exit 1
fi
exit "$status"
