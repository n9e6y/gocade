#!/bin/sh
# Connect to an Arena server from this terminal: play.sh [host [port]].
#
# The terminal is put in character mode (no line buffering, no local echo) so
# every key press reaches the game at once, and it is put back exactly as it
# was when you leave, however you leave.
set -eu

host=${1:-localhost}
port=${2:-9000}

if [ ! -t 0 ]; then
	echo "play: this needs a terminal, but standard input is not one" >&2
	exit 1
fi
if ! command -v nc >/dev/null 2>&1; then
	echo "play: nc (netcat) is not installed" >&2
	exit 1
fi

saved=$(stty -g)
# EXIT restores the terminal. INT and TERM turn into a normal exit, so the EXIT
# trap runs for them too.
trap 'stty "$saved"' EXIT
trap 'exit 130' INT TERM

stty -icanon -echo
if ! nc "$host" "$port"; then
	echo "play: could not stay connected to $host:$port (is the server running?)" >&2
	exit 1
fi
