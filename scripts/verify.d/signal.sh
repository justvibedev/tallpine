# Sourced by scripts/verify after $tsv, failed and timings exist.
# B361: a signal that reaches verify (a reaper, a pkill, a stopped session) must leave a summary naming the
# signal and who sent it as far as we can tell, not a log that just stops. SIGKILL cannot be caught; the
# loop records that case from the missing exit status.
on_signal() { # on_signal <signal number> <name>
	trap '' TERM HUP INT PIPE
	echo
	echo "--- verify received SIG$2 (pid $$, parent: $(ps -o pid=,command= -p "$PPID" 2>/dev/null | cut -c1-200)) at $(date '+%H:%M:%S')"
	echo "  steps finished: ${timings[*]:-none}"
	echo "FAIL: ${failed[*]:-}${failed[*]:+ }verify-killed-by-SIG$2"
	exit $((128 + $1))
}
if [ "$tsv" != 1 ]; then
	trap 'on_signal 15 TERM' TERM
	trap 'on_signal 1 HUP' HUP
	trap 'on_signal 2 INT' INT
fi
