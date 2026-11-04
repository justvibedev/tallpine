#!/usr/bin/env bash
# Tests for scripts/local-ci/loop.sh helpers, run against a temp dir with a fake tallpine CLI.
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/local-ci-loop.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT
fails=0
check() { if ! "$@"; then fails=$((fails + 1)); echo "FAIL: $*"; fi; }

# No legacy CLI/app/job fallbacks are left in the loop.
# (pattern assembled from pieces so this file carries no legacy-brand literal itself)
legacy="Vibe"" Orchestrator|daemon/v""o|command -v v""o|a""o-e2e|dock""er|gor""un"
check bash -c '! grep -nE "$2" "$1"' _ "$here/loop.sh" "$legacy"

export TALLPINE_LOCAL_CI_DIR="$tmp/ci" HOME="$tmp/home"
mkdir -p "$HOME"
# shellcheck disable=SC1091
LOOP_SOURCE_ONLY=1 source "$here/loop.sh"

# verify's summary parsing.
cat >"$tmp/v.out" <<'OUT'
=== go-test
--- FAIL: TestX (0.00s)
FAIL	tallpine.eu/x	0.1s
--- go-test FAILED (3s)
verify --all summary (logs: /x)
  flaky    fe-vitest: src/a.test.ts (failed, then passed on retry)
  flaky-detail | src/a.test.ts > A > breaks
  flaky-detail | AssertionError: expected 1 to be 2
FAIL: go-test go-lint
OUT
[ "$(failed_jobs "$tmp/v.out")" = "go-test go-lint" ] || { fails=$((fails + 1)); echo "FAIL: failed_jobs"; }
[ "$(flaky_items "$tmp/v.out")" = "fe-vitest: src/a.test.ts (failed, then passed on retry)" ] || { fails=$((fails + 1)); echo "FAIL: flaky_items"; }
[ "$(flaky_detail "$tmp/v.out" | tail -1)" = "AssertionError: expected 1 to be 2" ] || { fails=$((fails + 1)); echo "FAIL: flaky_detail"; }
job_output "$tmp/v.out" go-test | grep -q -- '--- FAIL: TestX' || { fails=$((fails + 1)); echo "FAIL: job_output"; }

# Notify goes through `tallpine orchestrator notify` with the project and the notify-target.
mkdir -p "$tmp/bin"
cat >"$tmp/bin/tallpine" <<'SH'
#!/bin/sh
echo "$@" >>"$CALLS"
[ "$1 $2" = "orchestrator --help" ] && printf 'Available Commands:\n  ls  List\n  notify  Notify\n'
exit 0
SH
chmod +x "$tmp/bin/tallpine"
export CALLS="$tmp/calls" PATH="$tmp/bin:$PATH"
echo "orch-7" >"$H/notify-target"
send_to_orchestrator "hello"
grep -q -- "orchestrator notify --project agent-orchestrator --target orch-7 --message hello" "$CALLS" || { fails=$((fails + 1)); echo "FAIL: notify args: $(cat "$CALLS" 2>/dev/null)"; }

# An installed CLI that predates `orchestrator notify`: its `orchestrator --help` exits 0 and lists no
# notify, and `orchestrator notify` fails with an unknown flag. The loop must fall back to ls + send.
mkdir -p "$tmp/oldbin"
cp "$here/../testdata/old-tallpine-cli.sh" "$tmp/oldbin/tallpine"
chmod +x "$tmp/oldbin/tallpine"
: >"$CALLS"; rm -f "$H/notify-target"
PATH="$tmp/oldbin:$PATH" send_to_orchestrator "legacy hello" >/dev/null
grep -q -- "send --session orch-old --message legacy hello" "$CALLS" || { fails=$((fails + 1)); echo "FAIL: old CLI did not get the send fallback: $(cat "$CALLS")"; }
grep -q -- "orchestrator notify" "$CALLS" && { fails=$((fails + 1)); echo "FAIL: old CLI was asked for orchestrator notify"; }

# B341: a build request made at main commit X is never fulfilled by a commit older than X.
WORK="$tmp/repo"; TSV="$tmp/t.tsv"; OUT="$tmp/out"; mkdir -p "$OUT"
git init -q "$WORK"; g() { git -C "$WORK" -c user.name=t -c user.email=t@t commit -q --allow-empty -m "$1"; git -C "$WORK" rev-parse HEAD; }
old="$(g a)"; new="$(g b)"
m="$HOME/.tallpine/release-builds/request-build"; mkdir -p "$(dirname "$m")"
printf '%s\n' "$new" >"$m"; printf 'main\t%s\tpass\tok\n' "$old" >"$TSV"
build_on_green "$old" >/dev/null 2>&1
[ -f "$m" ] || { fails=$((fails + 1)); echo "FAIL: older commit consumed a newer request"; }

# B355: a run that lacks the requested commit is killed (whole process group) once origin/main has it;
# a run that already includes it is left alone.
rm -rf "$tmp/up" "$tmp/w2"; git init -q --bare "$tmp/up"; git -C "$WORK" push -q "$tmp/up" HEAD:refs/heads/main 2>/dev/null
git clone -q "$tmp/up" "$tmp/w2"; WORK="$tmp/w2"; git -C "$WORK" checkout -q --detach "$old" 2>/dev/null
mkdir -p "$WORK/scripts"; printf '#!/bin/bash\nsleep 300 & echo $$ >"$KIDS"; echo $! >>"$KIDS"; wait\n' >"$WORK/scripts/verify"; chmod +x "$WORK/scripts/verify"
export KIDS="$tmp/kids"; printf '%s\n' "$new" >"$m"
( cd "$WORK" && SUPERSEDE_CHECK=1 run_verify_supervised "$old" "$tmp/v.log" "$tmp/v.rc" --all >/dev/null 2>&1 ); rc=$?
[ "$rc" = 3 ] || { fails=$((fails + 1)); echo "FAIL: stale run not superseded (rc $rc)"; }
kid="$(tail -1 "$KIDS")"; kill -0 "$kid" 2>/dev/null && { fails=$((fails + 1)); echo "FAIL: superseded run left a child"; kill "$kid"; }
printf '%s\n' "$old" >"$m"; printf '#!/bin/bash\nsleep 3\n' >"$WORK/scripts/verify"
( cd "$WORK" && SUPERSEDE_CHECK=1 run_verify_supervised "$new" "$tmp/v.log" "$tmp/v.rc" --all >/dev/null 2>&1 ); rc=$?
[ "$rc" = 0 ] || { fails=$((fails + 1)); echo "FAIL: run including the request was superseded (rc $rc)"; }

# B355: stopping the loop reaps the supervised verify run (its own session), so a restart leaves no orphan.
printf '%s\n' "$old" >"$m"; printf '#!/bin/bash\nsleep 300 & echo $! >"$KIDS"; wait\n' >"$WORK/scripts/verify"
( cd "$WORK" && install_kill_trap && SUPERSEDE_CHECK=1000 run_verify_supervised "$new" "$tmp/v.log" "$tmp/v.rc" --all >/dev/null 2>&1 ) &
sub=$!; sleep 2; kill -TERM "$sub"; wait "$sub" 2>/dev/null; sleep 1
kid="$(tail -1 "$KIDS")"; kill -0 "$kid" 2>/dev/null && { fails=$((fails + 1)); echo "FAIL: loop stop left verify orphaned"; kill "$kid"; }

# B355: a checkout-mismatch early return must leave SUPERSEDED_NOW at 0 (a stale 1 would skip the POLL sleep forever).
SUPERSEDED_NOW=1; TSV="$tmp/t2.tsv"; WORK="$tmp/w2"; run_suite "0000000000000000000000000000000000000000" >/dev/null 2>&1
[ "$SUPERSEDED_NOW" = 0 ] || { fails=$((fails + 1)); echo "FAIL: stale SUPERSEDED_NOW after checkout mismatch"; }

# B361: a crashed verify leaves its exit status or signal in the log.
printf '137\n' >"$tmp/rc"; [ "$(describe_verify_rc "$tmp/rc")" = "killed by SIGKILL (exit 137)" ] || { fails=$((fails + 1)); echo "FAIL: rc 137 not described as SIGKILL"; }
printf '2\n' >"$tmp/rc"; [ "$(describe_verify_rc "$tmp/rc")" = "exit 2" ] || { fails=$((fails + 1)); echo "FAIL: rc 2 not described"; }
case "$(describe_verify_rc "$tmp/none")" in "no exit status recorded"*) ;; *) fails=$((fails + 1)); echo "FAIL: missing rc file not described" ;; esac
# B361: the FAIL token verify prints when a signal stops it gets the log tail, not an empty section.
grep -qE 'verify-crashed\|verify-killed-by-\*\)' "$(dirname "$0")/loop.sh" || { fails=$((fails + 1)); echo "FAIL: verify-killed-by-* has no report case"; }

# B361: while verify runs the loop keeps a ps snapshot of its tree; the last one survives the run.
printf '#!/bin/bash\nsleep 3\n' >"$WORK/scripts/verify"
echo stale >"$tmp/s.ps"; printf '#!/bin/bash\nexit 1\n' >"$WORK/scripts/verify"
( cd "$WORK" && SNAPSHOT_EVERY=1000 run_verify_supervised "$new" "$tmp/s.log" "$tmp/s.rc" --all >/dev/null 2>&1 )
[ ! -e "$tmp/s.ps" ] || { fails=$((fails + 1)); echo "FAIL: stale ps snapshot survived a new run"; }
printf '#!/bin/bash\nsleep 3\n' >"$WORK/scripts/verify"
( cd "$WORK" && SNAPSHOT_EVERY=1 SUPERSEDE_CHECK=1000 run_verify_supervised "$new" "$tmp/s.log" "$tmp/s.rc" --all >/dev/null 2>&1 )
grep -q 'sleep 3' "$tmp/s.ps" && head -1 "$tmp/s.ps" | grep -q '^# ' && grep -q 'PPID' "$tmp/s.ps" || { fails=$((fails + 1)); echo "FAIL: no ps snapshot of the verify tree: $(cat "$tmp/s.ps" 2>&1)"; }

# B360: LOOP_CORE_BUDGET is clamped (non-numeric -> 12, below 4 -> 4) so -p / GOMAXPROCS are never 0.
for b in 0 abc 2 ""; do
	got="$(LOOP_CORE_BUDGET="$b" bash -c "$(sed -n '/^BUDGET=/,/^export VERIFY_CORE/p' "$here/loop.sh"); echo \"\$GOFLAGS \$GOMAXPROCS\"")"
	case "$got" in *"-p=0"*|*" 0") fails=$((fails + 1)); echo "FAIL: LOOP_CORE_BUDGET='$b' gave '$got'" ;; esac
done

# ps snapshot: a degraded "(name)" ps is retried, and never replaces an earlier readable snapshot.
fakeps="$tmp/fakeps"; cnt="$tmp/pscnt"; echo 0 >"$cnt"
printf '#!/bin/bash\nn=$(cat "%s"); echo $((n+1)) >"%s"\necho "PID PPID PGID STAT COMMAND"\nif [ "$n" -lt 1 ]; then echo "$$ 1 77 S (bash)"; echo "$$ 1 77 S (sleep)"; else echo "5 1 77 S sleep 3"; echo "6 1 77 S bash x"; fi\n' "$cnt" "$cnt" >"$fakeps"; chmod +x "$fakeps"
rm -f "$tmp/q.ps"; PS_CMD="$fakeps" snapshot_tree 77 "$tmp/q.ps"
grep -q 'sleep 3' "$tmp/q.ps" 2>/dev/null || { fails=$((fails + 1)); echo "FAIL: degraded ps was not retried"; }
printf '#!/bin/bash\necho "PID PPID PGID STAT COMMAND"; echo "5 1 77 S (bash)"; echo "6 1 77 S (sleep)"\n' >"$fakeps"
echo good >"$tmp/q.ps"; PS_CMD="$fakeps" snapshot_tree 77 "$tmp/q.ps"
[ "$(cat "$tmp/q.ps")" = good ] || { fails=$((fails + 1)); echo "FAIL: degraded ps replaced a readable snapshot"; }
[ "$fails" = 0 ] && echo "local-ci loop tests: ok"
exit "$fails"
