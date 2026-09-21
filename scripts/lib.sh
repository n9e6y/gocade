# Helpers shared by the scripts in this directory. Source it with: . scripts/lib.sh

# wait_listening LOG PID waits (for up to five seconds) until the server whose
# log is LOG says it is listening. It gives up at once, printing the end of the
# log, if the process PID has died (a busy port, say).
wait_listening() {
	log=$1
	pid=$2
	tries=0
	until grep -q 'msg=listening' "$log"; do
		if ! kill -0 "$pid" 2>/dev/null; then
			echo "the server did not start:" >&2
			tail -n 5 "$log" >&2
			return 1
		fi
		tries=$((tries + 1))
		if [ "$tries" -gt 50 ]; then
			echo "the server did not report listening within 5 seconds (see $log)" >&2
			return 1
		fi
		sleep 0.1
	done
}
