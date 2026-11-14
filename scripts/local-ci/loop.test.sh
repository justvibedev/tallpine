#!/usr/bin/env bash
# Tests for scripts/local-ci/loop.sh helpers, run against a temp dir with a fake tallpine CLI.
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/local-ci-loop.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT
fails=0
export TALLPINE_ALERT_DESKTOP_NOTIFY="" # alerts here must not reach the real desktop
check() { if ! "$@"; then fails=$((fails + 1)); echo "FAIL: $*"; fi; }

# No legacy CLI/app/job fallbacks are left in the loop.
# (pattern assembled from pieces so this file carries no legacy-brand literal itself)
legacy="Vibe"" Orchestrator|daemon/v""o|command -v v""o|a""o-e2e|dock""er|gor""un"
check bash -c '! grep -nE "$2" "$1"' _ "$here/loop.sh" "$legacy"

export TALLPINE_LOCAL_CI_DIR="$tmp/ci" HOME="$tmp/home"
mkdir -p "$HOME"
# shellcheck disable=SC1091
LOOP_SOURCE_ONLY=1 source "$here/loop.sh"
# Every verify run in this file wipes its scratch dir: keep it isolated and short, never the live loop's.
LIVE_SCRATCH="/tmp/tp-ci-$(id -u)"; live_before="$(stat -f %i "$LIVE_SCRATCH" 2>/dev/null)"
export VERIFY_SCRATCH="$(mktemp -d /tmp/tplt.XXXXXX)"; trap 'rm -rf "$tmp" "$VERIFY_SCRATCH"' EXIT

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

# B631: an alert is recorded in $ALERTS (alerts.tsv next to local-ci.tsv), never sent: a signed daemon
# refuses the CLI run from here. The fake CLI records any call, so a send would show.
mkdir -p "$tmp/bin"
cat >"$tmp/bin/tallpine" <<'SH'
#!/bin/sh
echo "$@" >>"$CALLS"
exit 0
SH
chmod +x "$tmp/bin/tallpine"
export CALLS="$tmp/calls" PATH="$tmp/bin:$PATH"
: >"$CALLS"
[ "$ALERTS" = "$OUT/alerts.tsv" ] || { fails=$((fails + 1)); echo "FAIL: alerts file is $ALERTS"; }
record_alert "$(printf 'hello\tthere\nagain')" >/dev/null
grep -qE "^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:]{8}Z	hello there again$" "$ALERTS" || { fails=$((fails + 1)); echo "FAIL: alert line: $(cat "$ALERTS" 2>/dev/null)"; }
[ ! -s "$CALLS" ] || { fails=$((fails + 1)); echo "FAIL: an alert called the CLI: $(cat "$CALLS")"; }
# The detached build child (env -i, bash -c) gets every alert helper and setting, so with no reader its
# alert still reaches the desktop.
printf '#!/bin/sh\nfor a; do last="$a"; done\necho "$last" >>"%s/notified"\n' "$tmp" >"$tmp/notifyrec"; chmod +x "$tmp/notifyrec"
rm -f "$(dirname "$ALERTS")/alerts.cursor"
defs="$(TALLPINE_ALERT_DESKTOP_NOTIFY="$tmp/notifyrec" alert_child_defs)"
env -i HOME="$HOME" PATH="$PATH" bash -c "$defs"$'\n''record_alert "from the child"' >"$tmp/child.out" 2>&1
grep -qx "from the child" "$tmp/notified" 2>/dev/null || { fails=$((fails + 1)); echo "FAIL: the detached child did not notify the desktop: $(cat "$tmp/child.out")"; }
grep -q "WARNING: no reader" "$tmp/child.out" || { fails=$((fails + 1)); echo "FAIL: the detached child did not warn: $(cat "$tmp/child.out")"; }

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
# The fake verify stays alive (with a recognisable child) until the loop has written a snapshot, so no load can end it first.
printf '#!/bin/bash\nsleep 7 & k=$!\nfor _ in $(seq 1 1200); do [ -s "%s" ] && break; sleep 0.1; done\nkill $k\n' "$tmp/s.ps" >"$WORK/scripts/verify"
( cd "$WORK" && SNAPSHOT_EVERY=1 SUPERSEDE_CHECK=1000 run_verify_supervised "$new" "$tmp/s.log" "$tmp/s.rc" --all >/dev/null 2>&1 )
grep -q 'sleep 7' "$tmp/s.ps" && head -1 "$tmp/s.ps" | grep -q '^# ' && grep -q 'PPID' "$tmp/s.ps" || { fails=$((fails + 1)); echo "FAIL: no ps snapshot of the verify tree: $(cat "$tmp/s.ps" 2>&1)"; }

# B602: a verify run gets its own short TMPDIR, and nothing it leaves there survives.
SCR="$VERIFY_SCRATCH"; mkdir -p "$SCR"; echo stale >"$SCR/left-by-killed-run"
printf '#!/bin/bash\necho "$TMPDIR" >"%s"; ls "$TMPDIR" >>"%s"; touch "$TMPDIR/go-build1"\n' "$tmp/td" "$tmp/td" >"$WORK/scripts/verify"
( cd "$WORK" && SUPERSEDE_CHECK=1000 run_verify_supervised "$new" "$tmp/s.log" "$tmp/s.rc" --all >/dev/null 2>&1 )
[ "$(sed -n 1p "$tmp/td")" = "$SCR" ] || { fails=$((fails + 1)); echo "FAIL: verify TMPDIR was $(sed -n 1p "$tmp/td")"; }
grep -q left-by-killed-run "$tmp/td" && { fails=$((fails + 1)); echo "FAIL: a killed run's scratch was not swept"; }
[ ! -e "$SCR" ] || { fails=$((fails + 1)); echo "FAIL: verify scratch survived the run"; }
DEF="$(sed -n 's/.*VERIFY_SCRATCH:-\(.*\)}" #.*/\1/p' "$here/loop.sh")"; DEF="$(eval "echo \"$DEF\"")"
[ -n "$DEF" ] && [ "${#DEF}" -le 30 ] || { fails=$((fails + 1)); echo "FAIL: default scratch '$DEF' is ${#DEF} bytes; tests bind unix sockets under it (104-byte sun_path limit)"; }

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
# B590: GitHub access is per repo, and an unreachable origin is reported rather than idled through.
fetch_state="$H/fetch-failing"; : >"$ALERTS"; rm -f "$fetch_state" "$H/github-user"
printf 'fatal: repository not found https://user:s3cret@github.com/x.git\n' >"$FETCH_ERR"
FETCH_FAIL_NOTIFY_AFTER=2 note_fetch_failure >/dev/null
grep -q 'cannot reach' "$ALERTS" && { fails=$((fails + 1)); echo "FAIL: notified before the threshold"; }
[ "$(sed -n 1p "$fetch_state" | cut -d' ' -f1)" = 1 ] || { fails=$((fails + 1)); echo "FAIL: first failure not recorded: $(cat "$fetch_state")"; }
FETCH_FAIL_NOTIFY_AFTER=2 note_fetch_failure >/dev/null
grep -q 'cannot reach origin.*repository not found' "$ALERTS" || { fails=$((fails + 1)); echo "FAIL: repeated failure did not notify: $(cat "$ALERTS")"; }
grep -q 's3cret' "$ALERTS" "$fetch_state" && { fails=$((fails + 1)); echo "FAIL: URL credentials leaked into the notice or state"; }
n="$(grep -c 'cannot reach origin' "$ALERTS")"; FETCH_FAIL_NOTIFY_AFTER=2 note_fetch_failure >/dev/null
[ "$(grep -c 'cannot reach origin' "$ALERTS")" = "$n" ] || { fails=$((fails + 1)); echo "FAIL: renotified on every tick"; }
: >"$ALERTS"; note_fetch_ok >/dev/null
[ ! -e "$fetch_state" ] || { fails=$((fails + 1)); echo "FAIL: recovery left the failure state"; }
grep -q 'reachable again' "$ALERTS" || { fails=$((fails + 1)); echo "FAIL: no all-clear after a notified failure: $(cat "$ALERTS")"; }
# A blip that never reached the orchestrator gets no all-clear.
: >"$ALERTS"; FETCH_FAIL_NOTIFY_AFTER=9 note_fetch_failure >/dev/null; note_fetch_ok >/dev/null
grep -q 'reachable again' "$ALERTS" && { fails=$((fails + 1)); echo "FAIL: all-clear for an unannounced blip"; }

# Fake gh with two accounts; only "good" can reach the repo. The fake git fails unless its credential helper yields good's token.
cat >"$tmp/bin/gh" <<'SH'
#!/bin/sh
case "$*" in
  "auth status --hostname github.com") printf '  x Failed to log in to github.com account broken (keyring)\n  ok Logged in to github.com account bad (keyring)\n  ok Logged in to github.com account good (keyring)\n' ;;
  *"auth token --hostname github.com --user good") echo good-token ;;
  *"auth token --hostname github.com --user bad") echo bad-token ;;
  *) exit 1 ;;
esac
SH
chmod +x "$tmp/bin/gh"
[ "$(gh_logins | tr '\n' ' ')" = "bad good " ] || { fails=$((fails + 1)); echo "FAIL: gh_logins: $(gh_logins | tr '\n' ' ')"; }
# The real git credential path: the chosen account answers even when a machine-wide helper is configured.
git config --global credential.helper '!f() { echo username=machine; echo password=machine-token; }; f'
cred() { printf 'protocol=https\nhost=github.com\n\n' | ci_git credential fill 2>/dev/null | sed -n 's/^password=//p'; }
got="$(CI_GH_LOGIN=good cred)"; [ "$got" = good-token ] || { fails=$((fails + 1)); echo "FAIL: chosen account's token not used: '$got'"; }
got="$(CI_GH_LOGIN="" cred)"; [ "$got" = machine-token ] || { fails=$((fails + 1)); echo "FAIL: no chosen account must keep the ambient login: '$got'"; }
git config --global --unset credential.helper
realgit="$(command -v git)"
cat >"$tmp/bin/git" <<SH
#!/bin/bash
case "\$*" in *ls-remote*) ;; *) exec "$realgit" "\$@" ;; esac
case "\$*" in *"--user good"*) echo "abc	refs/heads/main"; exit 0 ;; esac
echo "fatal: repository not found" >&2; exit 128
SH
chmod +x "$tmp/bin/git"; hash -r # bash cached the real git
CI_GH_LOGIN=""; ci_try -C "$WORK" ls-remote origin refs/heads/main >/dev/null || { fails=$((fails + 1)); echo "FAIL: did not fall back to the account that can reach the repo"; }
[ "$CI_GH_LOGIN" = good ] && [ "$(printf '%s' "$CI_OUT" | cut -f1)" = abc ] || { fails=$((fails + 1)); echo "FAIL: fallback not remembered: '$CI_GH_LOGIN' '$CI_OUT'"; }
# A pinned account is the only one used, and its failure stays a failure.
echo bad >"$H/github-user"; CI_GH_LOGIN=""
ci_try -C "$WORK" ls-remote origin refs/heads/main >/dev/null 2>&1 && { fails=$((fails + 1)); echo "FAIL: a pinned account fell back to another one"; }
[ "$CI_GH_LOGIN" = bad ] || { fails=$((fails + 1)); echo "FAIL: pinned account not selected: '$CI_GH_LOGIN'"; }
rm -f "$H/github-user" "$tmp/bin/git" "$tmp/bin/gh"; hash -r; CI_GH_LOGIN=""

# The real loop against an unreachable origin: it reports and retries, and never evaluates anything.
loopdir="$tmp/ci2"; mkdir -p "$loopdir"; touch "$loopdir/housekeeping.stamp"; LALERTS="$loopdir/results/alerts.tsv"; rm -f "$LALERTS"
( TALLPINE_LOCAL_CI_DIR="$loopdir" REPO_URL="$tmp/no-such-repo" bash "$here/loop.sh" --once >"$tmp/once.out" 2>&1 ); rc=$?
[ "$rc" = 1 ] || { fails=$((fails + 1)); echo "FAIL: --once on an unreachable origin exited $rc: $(cat "$tmp/once.out")"; }
rm -f "$loopdir/fetch-failing"
TALLPINE_LOCAL_CI_DIR="$loopdir" REPO_URL="$tmp/no-such-repo" POLL=1 FETCH_FAIL_NOTIFY_AFTER=2 bash "$here/loop.sh" >"$tmp/loop.out" 2>&1 &
loop_pid=$!; for _ in $(seq 1 30); do grep -q 'cannot reach origin' "$LALERTS" 2>/dev/null && break; sleep 0.5; done; kill "$loop_pid" 2>/dev/null; wait "$loop_pid" 2>/dev/null
grep -q 'cannot reach origin' "$LALERTS" || { fails=$((fails + 1)); echo "FAIL: running loop did not report: $(cat "$tmp/loop.out")"; }
[ -s "$loopdir/fetch-failing" ] || { fails=$((fails + 1)); echo "FAIL: running loop left no failure state"; }
grep -q 'evaluating main' "$tmp/loop.out" && { fails=$((fails + 1)); echo "FAIL: loop evaluated main with no origin"; }

# Recovery on the real loop: origin appears, the failure clears, and a recorded head is not re-run.
rm -rf "$tmp/up2"; git init -q --bare "$tmp/up2"; git -C "$tmp/w2" push -q "$tmp/up2" HEAD:refs/heads/main 2>/dev/null
up_sha="$(git -C "$tmp/up2" rev-parse refs/heads/main)"; mkdir -p "$loopdir/results"; printf 'main\t%s\tpass\tok\n' "$up_sha" >"$loopdir/results/local-ci.tsv"
printf '3 1 1\nold error\n' >"$loopdir/fetch-failing"; rm -f "$LALERTS"
TALLPINE_LOCAL_CI_DIR="$loopdir" REPO_URL="$tmp/up2" POLL=1 bash "$here/loop.sh" >"$tmp/loop2.out" 2>&1 &
loop_pid=$!; for _ in $(seq 1 30); do [ ! -e "$loopdir/fetch-failing" ] && break; sleep 0.5; done; kill "$loop_pid" 2>/dev/null; wait "$loop_pid" 2>/dev/null
[ ! -e "$loopdir/fetch-failing" ] || { fails=$((fails + 1)); echo "FAIL: loop did not clear the failure: $(cat "$tmp/loop2.out")"; }
grep -q 'reachable again' "$LALERTS" || { fails=$((fails + 1)); echo "FAIL: loop sent no all-clear: $(cat "$tmp/loop2.out")"; }
grep -q 'evaluating main' "$tmp/loop2.out" && { fails=$((fails + 1)); echo "FAIL: recorded head was re-evaluated"; }

# The incident itself: an existing checkout whose origin stops answering (ls-remote fails), then one where only fetch fails.
run_failing_loop() { # run_failing_loop <label>: the loop must report without evaluating anything
  rm -f "$LALERTS"; rm -f "$loopdir/fetch-failing"
  TALLPINE_LOCAL_CI_DIR="$loopdir" REPO_URL="$tmp/up2" POLL=1 FETCH_FAIL_NOTIFY_AFTER=1 bash "$here/loop.sh" >"$tmp/loop3.out" 2>&1 &
  loop_pid=$!; for _ in $(seq 1 30); do grep -q 'cannot reach origin' "$LALERTS" 2>/dev/null && break; sleep 0.5; done; kill "$loop_pid" 2>/dev/null; wait "$loop_pid" 2>/dev/null
  grep -q 'cannot reach origin' "$LALERTS" || { fails=$((fails + 1)); echo "FAIL: $1 was not reported: $(cat "$tmp/loop3.out")"; }
  grep -q 'evaluating main' "$tmp/loop3.out" && { fails=$((fails + 1)); echo "FAIL: $1 evaluated a stale main"; }
}
rm -f "$loopdir/work/main/.git/FETCH_HEAD"; mkdir "$loopdir/work/main/.git/FETCH_HEAD" # ls-remote still answers; fetch cannot write
run_failing_loop "a failing fetch"
mv "$tmp/up2" "$tmp/up2.gone"
run_failing_loop "a failing ls-remote"

# npmseed installs only when what npm would install changed or a tree is incomplete.
seed="$tmp/seed"; npmbin="$tmp/npmbin"; mkdir -p "$npmbin" "$H"
cat >"$npmbin/npm" <<'NPM'
#!/usr/bin/env bash
echo "$PWD $*" >>"$NPM_LOG"
[ "$1" = ci ] || exit 0
[ -z "${NPM_FAIL:-}" ] || exit 1
rm -rf node_modules; mkdir node_modules; : >node_modules/.package-lock.json
NPM
chmod +x "$npmbin/npm"
export NPM_LOG="$tmp/npm.log"
seed_locks() { # own version, dependency version
  local d
  for d in . frontend frontend/src/docs packages/product-ui; do
    mkdir -p "$seed/$d"
    printf '{"name":"x","version":"%s","lockfileVersion":3,"packages":{"":{"name":"x","version":"%s"},"node_modules/a":{"version":"%s"}}}\n' "$1" "$1" "$2" >"$seed/$d/package-lock.json"
  done
}
seed_installs() { : >"$NPM_LOG"; (cd "$seed" && PATH="$npmbin:$PATH" npmseed); grep -c ' ci ' "$NPM_LOG"; }
expect_installs() { # want, scenario
  local got; got="$(seed_installs)"
  [ "$got" = "$1" ] || { fails=$((fails + 1)); echo "FAIL: npmseed $2: $got npm ci runs, want $1"; }
}
seed_locks 1.0.0 1.0.0; expect_installs 4 "on first run"
expect_installs 0 "with nothing changed"
seed_locks 1.0.1 1.0.0; expect_installs 0 "after a release bumped only the package version"
seed_locks 1.0.1 2.0.0; expect_installs 4 "after a dependency changed"
rm "$seed/frontend/node_modules/.package-lock.json"; expect_installs 4 "with an incomplete tree"
rm -rf "$seed/packages/product-ui/node_modules"; expect_installs 4 "with a missing tree"
seed_locks 1.0.1 3.0.0; NPM_FAIL=1 expect_installs 1 "when the install fails"
seed_locks 1.0.1 2.0.0; expect_installs 4 "back on the last good lockfile after a failed install"

# B680: an installed loop that is not main's (an old copy kept calling the refusing daemon) raises one
# alert per main version, and none while the installed loop.sh and notify.sh match main.
(
  inst="$tmp/inst" WORK="$tmp/stalework" ALERTS="$tmp/stale-alerts.tsv"
  mkdir -p "$inst" "$WORK/scripts/local-ci" "$WORK/scripts/lib"
  cp "$here/loop.sh" "$WORK/scripts/local-ci/loop.sh"; cp "$here/../lib/notify.sh" "$WORK/scripts/lib/notify.sh"
  cp "$here/loop.sh" "$inst/loop.sh"; cp "$here/../lib/notify.sh" "$inst/notify.sh"; LOOP_SELF="$inst/loop.sh"
  stale_count() { cat "$ALERTS" 2>/dev/null | grep -c 'differs from main'; }
  warn_stale_install aaaaaaaaaaaa >/dev/null; [ "$(stale_count)" = 0 ] || echo "FAIL: alerted while current"
  echo '# old' >>"$inst/loop.sh"
  warn_stale_install bbbbbbbbbbbb >/dev/null; warn_stale_install cccccccccccc >/dev/null
  [ "$(stale_count)" = 1 ] || echo "FAIL: stale loop.sh: $(stale_count) alerts, want 1"
  grep -q "($inst/loop.sh) differs from main bbbbbbbbbb: run scripts/local-ci/install" "$ALERTS" || echo "FAIL: stale alert text: $(cat "$ALERTS")"
  echo '# newer main' >>"$WORK/scripts/local-ci/loop.sh"
  warn_stale_install dddddddddddd >/dev/null; [ "$(stale_count)" = 2 ] || echo "FAIL: a newer main did not alert again"
  cp "$WORK/scripts/local-ci/loop.sh" "$inst/loop.sh"; warn_stale_install eeeeeeeeeeee >/dev/null
  [ "$(stale_count)" = 2 ] || echo "FAIL: alerted after reinstall"
  echo '# old' >>"$inst/notify.sh"
  warn_stale_install ffffffffffff >/dev/null; [ "$(stale_count)" = 3 ] || echo "FAIL: a stale notify.sh did not alert"
) >"$tmp/stale.out" 2>&1
grep -q FAIL "$tmp/stale.out" && { fails=$((fails + 1)); cat "$tmp/stale.out"; }

# The live loop's scratch dir must be exactly as this test found it (not created, not recreated).
[ "$(stat -f %i "$LIVE_SCRATCH" 2>/dev/null)" = "$live_before" ] || { fails=$((fails + 1)); echo "FAIL: the test touched the live scratch dir $LIVE_SCRATCH"; }

[ "$fails" = 0 ] && echo "local-ci loop tests: ok"
exit "$fails"
