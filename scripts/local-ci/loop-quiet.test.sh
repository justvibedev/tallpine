#!/usr/bin/env bash
# Tests for loop.sh quiet_wait: a stubbed LOOP_LOAD_CMD stands in for the machine load.
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/local-ci-quiet.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT
fails=0
check() { if ! "$@"; then fails=$((fails + 1)); echo "FAIL: $*"; fi; }
export TALLPINE_LOCAL_CI_DIR="$tmp/ci" HOME="$tmp/home"; mkdir -p "$HOME"
# shellcheck disable=SC1091
LOOP_SOURCE_ONLY=1 source "$here/loop.sh"
export LOOP_CORES=8 LOOP_QUIET_POLL=0

# Busy twice, then quiet: waits, logs, and records the wait.
echo 0 >"$tmp/n"
export LOOP_LOAD_CMD="n=\$(cat $tmp/n); echo \$((n+1)) >$tmp/n; if [ \$n -lt 2 ]; then echo 9.5; else echo 1.2; fi"
export LOOP_QUIET_WAIT_MAX=100
out="$(quiet_wait; echo "note=$QUIET_NOTE")"
check bash -c 'printf "%s\n" "$1" | grep -q -- "$2"' _ "$out" 'machine busy (load 9.5/8 cores)'
check bash -c 'printf "%s\n" "$1" | grep -q -- "$2"' _ "$out" 'machine quiet (load 1.2/8 cores)'
check [ "$(cat "$tmp/n")" = 3 ]

# Busy past LOOP_QUIET_WAIT_MAX: runs anyway and says so.
export LOOP_LOAD_CMD="echo 20" LOOP_QUIET_WAIT_MAX=3 LOOP_QUIET_POLL=1
out="$(quiet_wait; echo "note=$QUIET_NOTE")"
check bash -c 'printf "%s\n" "$1" | grep -q -- "$2"' _ "$out" 'running anyway'
check bash -c 'printf "%s\n" "$1" | grep -q -- "$2"' _ "$out" 'note= \[ran busy after 3s wait\]'

# A read failure runs immediately, no wait.
LOOP_QUIET_POLL=60; export LOOP_QUIET_WAIT_MAX=100
for cmd in "exit 1" "echo garbage" "true"; do
  export LOOP_LOAD_CMD="$cmd"
  start=$SECONDS; out="$(quiet_wait; echo "note=$QUIET_NOTE")"
  check [ $((SECONDS - start)) -lt 5 ]
  check bash -c 'printf "%s\n" "$1" | grep -qx note=' _ "$out"
done

# Disabled by LOOP_QUIET_WAIT_MAX=0.
export LOOP_LOAD_CMD="echo 99" LOOP_QUIET_WAIT_MAX=0
start=$SECONDS; quiet_wait; check [ $((SECONDS - start)) -lt 5 ]

[ "$fails" = 0 ] && echo "loop-quiet tests passed" || { echo "$fails failure(s)"; exit 1; }
