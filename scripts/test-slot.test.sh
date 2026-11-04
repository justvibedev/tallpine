#!/usr/bin/env bash
# Tests for scripts/test-slot (B363): queueing, killed-holder recovery, narrow runs, nesting, loop lane.
set -u
here="$(cd "$(dirname "$0")" && pwd)"
slot="$here/test-slot"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/slot-test.XXXXXX")"
trap 'kill $(jobs -p) 2>/dev/null; rm -rf "$tmp"' EXIT
export TALLPINE_DATA_DIR="$tmp/data" TALLPINE_TEST_SLOTS=1
unset TALLPINE_SLOT_HELD VERIFY_PRIORITY
tmo() { perl -e 'alarm shift; exec @ARGV' -- "$@"; } # no GNU timeout on macOS
fails=0
fail() { fails=$((fails + 1)); echo "FAIL: $*"; }

# 1. Passes the command's exit status through.
"$slot" -- sh -c 'exit 7'; [ $? = 7 ] || fail "exit status not passed through"

# 2. A second run queues behind a holder, says so, and runs once the holder ends.
"$slot" -- sh -c 'sleep 4' & h=$!
sleep 1
"$slot" -- echo second >"$tmp/o2" 2>"$tmp/e2" &
w=$!
sleep 3
grep -q 'waiting for a test slot (0 ahead)' "$tmp/e2" || fail "no waiting message: $(cat "$tmp/e2")"
[ ! -s "$tmp/o2" ] || fail "waiter ran while the slot was held"
wait "$h"; wait "$w"
grep -q second "$tmp/o2" || fail "waiter never ran after the holder finished"

# 3. A killed holder frees its slot (SIGKILL leaves the lock dir behind; the next waiter reaps it).
"$slot" -- sh -c 'echo $$ >"$1"; exec sleep 60' sh "$tmp/child" & h=$!
sleep 1
kill -9 "$h"; wait "$h" 2>/dev/null
kill "$(cat "$tmp/child")" 2>/dev/null # only this test's own orphan
out="$(tmo 20 "$slot" -- echo reaped 2>/dev/null)"
[ "$out" = reaped ] || fail "killed holder did not free its slot (got '$out')"

# 4. Narrow runs bypass a full pool: --skip-if, --throttle-if, --fixed.
"$slot" -- sleep 30 & h=$!
sleep 1
[ "$(tmo 3 "$slot" --skip-if '\.test\.' -- echo a.test.ts)" = a.test.ts ] || fail "--skip-if not unthrottled"
[ "$(tmo 3 "$slot" --throttle-if '\.\.\.$' -- echo ./pkg)" = ./pkg ] || fail "--throttle-if without a tree arg was throttled"
[ "$(tmo 3 "$slot" --fixed 1 -- echo src/x)" = src/x ] || fail "--fixed with a filter arg was throttled"
tmo 3 "$slot" --throttle-if '\.\.\.$' -- echo ./... >/dev/null 2>&1 && fail "--throttle-if with a tree arg was not throttled"
tmo 3 "$slot" --fixed 1 -- echo --flag >/dev/null 2>&1 && fail "--fixed with only flags was not throttled"

# 5. The CI loop lane and nested runs inside a held slot never queue.
[ "$(VERIFY_PRIORITY=1 tmo 3 "$slot" -- echo loop)" = loop ] || fail "loop lane queued"
[ "$(TALLPINE_SLOT_HELD=1 tmo 3 "$slot" -- echo nested)" = nested ] || fail "nested run queued"
kill "$h" 2>/dev/null; wait "$h" 2>/dev/null

# 6. The holder's command sees TALLPINE_SLOT_HELD=1 (so scripts/go test inside it does not re-queue).
[ "$("$slot" -- sh -c 'echo $TALLPINE_SLOT_HELD')" = 1 ] || fail "TALLPINE_SLOT_HELD not exported to the child"

[ "$fails" = 0 ] && echo "test-slot tests: ok"
exit "$fails"
