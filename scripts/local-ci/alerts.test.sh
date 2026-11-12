#!/usr/bin/env bash
# Tests for scripts/local-ci/alerts: prints only alerts not yet passed on, complete lines only.
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/local-ci-alerts.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT
fails=0
expect() { # expect <label> <want> <got>
	[ "$2" = "$3" ] || { fails=$((fails + 1)); printf 'FAIL: %s\n  want: %q\n  got:  %q\n' "$1" "$2" "$3"; }
}
export TALLPINE_LOCAL_CI_DIR="$tmp/ci"
export TALLPINE_ALERT_DESKTOP_NOTIFY="" # no real notifications; the reader-stale cases below use a recorder
res="$tmp/ci/results"
run() { "$here/alerts" "$@"; }

expect "no alerts file prints nothing" "" "$(run)"
mkdir -p "$res"
# shellcheck disable=SC1091
. "$here/../lib/notify.sh"
tp_record_alert "$res/alerts.tsv" "[local-ci] main abc FAILED: go-test" >/dev/null
tp_record_alert "$res/alerts.tsv" "[local-ci] build abc OK" >/dev/null
got="$(run --peek)"
expect "peek prints both alerts" 2 "$(printf '%s\n' "$got" | grep -c '^20[0-9-]*T[0-9:]*Z \[local-ci\]')"
expect "peek does not advance" "$got" "$(run)"
expect "printed alerts are not printed again" "" "$(run)"
tp_record_alert "$res/alerts.tsv" "[local-ci] main def is green again" >/dev/null
expect "only the new alert" "[local-ci] main def is green again" "$(run | cut -d' ' -f2-)"
# A line still being written waits for the next run.
printf '2026-01-01T00:00:00Z\thalf' >>"$res/alerts.tsv"
expect "a partial line is held back" "" "$(run)"
printf ' done\n' >>"$res/alerts.tsv"
expect "the completed line follows" "2026-01-01T00:00:00Z half done" "$(run)"
# A truncated file is read from the start.
printf '2026-01-02T00:00:00Z\tafter rotation\n' >"$res/alerts.tsv"
expect "rotation restarts at the beginning" "2026-01-02T00:00:00Z after rotation" "$(run)"
expect "no target file prints an empty line" "" "$(run --target)"
echo " orch-9 " >"$tmp/ci/notify-target"
expect "target names the main-health orchestrator" "orch-9" "$(run --target)"
# An alert with no recent reader is also shown on the desktop, so it is never parked unseen.
printf '#!/bin/sh\nfor a; do last="$a"; done\nprintf "%%s\\n" "$last" >>"%s/notified"\n' "$tmp" >"$tmp/notify"
chmod +x "$tmp/notify"
export TALLPINE_ALERT_DESKTOP_NOTIFY="$tmp/notify"
notified() { cat "$tmp/notified" 2>/dev/null; }
run >/dev/null
tp_record_alert "$res/alerts.tsv" "[local-ci] reader ran just now" >/dev/null
expect "a recent reader means no desktop notification" "" "$(notified)"
touch -t 202001010000 "$res/alerts.cursor"
out="$(tp_record_alert "$res/alerts.tsv" "[local-ci] reader stale")"
expect "a stale reader shows the alert on the desktop" "[local-ci] reader stale" "$(notified)"
expect "a stale reader is called out in the log" 1 "$(printf '%s\n' "$out" | grep -c '^WARNING: no reader has passed local-CI alerts on')"
run >/dev/null
touch -t 202001010000 "$res/alerts.cursor"
expect "a run with nothing new still stamps the reader" "" "$(run)"
tp_record_alert "$res/alerts.tsv" "[local-ci] after an empty run" >/dev/null
expect "an empty reader run counts as recent" "[local-ci] reader stale" "$(notified)"
rm -f "$res/alerts.cursor"
tp_record_alert "$res/alerts.tsv" "[local-ci] no reader ever" >/dev/null
expect "a reader that never ran shows the alert on the desktop" "no reader ever" "$(notified | tail -n 1 | cut -d' ' -f2-)"
export TALLPINE_ALERT_DESKTOP_NOTIFY=""
run --bogus >/dev/null 2>&1 && { fails=$((fails + 1)); echo "FAIL: unknown argument accepted"; }
[ "$fails" = 0 ] && echo "local-ci alerts tests: ok"
exit "$fails"
