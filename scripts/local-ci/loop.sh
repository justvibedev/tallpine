#!/bin/bash
# Main-only local CI loop: re-runs the local gate whenever origin/main moves.
# This file lives in the repo; scripts/local-ci/install copies it to ~/.tallpine/local-ci/loop.sh,
# which a service manager runs. The loop only schedules: the gate itself is `scripts/verify --all`.
#   loop.sh           loop forever: re-run whenever origin/main moves (poll every POLL s, default 120; a cheap git ls-remote decides whether to fetch)
#   loop.sh --once    evaluate current origin/main once, even if already recorded; exits 1 if origin is unreachable
# Appends "main\t<sha>\t<pass|fail|superseded>\t<summary>" to $OUT/local-ci.tsv; full log in
# $OUT/local-ci-main-<sha8>.log; itemised failures (and [flaky-retried] items) in
# $OUT/local-ci-main-<sha8>-failures.txt.  Start with ~/.tallpine/local-ci/start.sh (detached, pid file).
# GitHub access: $H/github-user (one login) pins the gh account for this repo; without it the loop uses the
# ambient login and falls back to any other signed-in gh account that can reach the repo. While origin is
# unreachable, $H/fetch-failing holds the failure and the orchestrator is told (see note_fetch_failure).
H="${TALLPINE_LOCAL_CI_DIR:-$HOME/.tallpine/local-ci}"
OUT="${OUT:-$H/results}"
REPO_URL="${REPO_URL:-https://github.com/justvibedev/tallpine-dev.git}"
PROJECT_ID="${LOCAL_CI_PROJECT_ID:-agent-orchestrator}"
WORK="$H/work/main"; POLL="${POLL:-120}"; TSV="$OUT/local-ci.tsv"
export PATH="$HOME/.nvm/versions/node/v24.18.0/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"
mkdir -p "$OUT" "$H/work" "$H/gocache" "$H/gomod" "$H/tmp"

# Keep the loop's Go caches apart from the workers'; nice 15 keeps the machine usable.
# LOOP_CORE_BUDGET (default 12 of 18 cores) is the loop's total; scripts/verify splits it across the
# concurrent steps (verify.d/par.sh adapt_caps). Workers keep their own caps (B350 shared budget).
BUDGET="${LOOP_CORE_BUDGET:-12}"
case "$BUDGET" in ""|*[!0-9]*) BUDGET=12 ;; esac # non-numeric: default
[ "$BUDGET" -ge 4 ] || BUDGET=4 # keep every -p / GOMAXPROCS at least 1
export GOCACHE="$H/gocache" GOMODCACHE="$H/gomod" GOFLAGS="-buildvcs=false -p=$((BUDGET / 4))" VERIFY_NICE=15
export VERIFY_CORE_BUDGET="$BUDGET" GOMAXPROCS=$((BUDGET / 4))

# lock_fingerprint prints the given lockfiles without each one's own package
# version: a release bumps that, and it changes nothing npm would install.
lock_fingerprint() {
  node -e '
    const fs = require("fs");
    for (const file of process.argv.slice(1)) {
      let raw;
      try { raw = fs.readFileSync(file, "utf8"); } catch { console.log(file, "absent"); continue; }
      try {
        const lock = JSON.parse(raw);
        delete lock.version;
        if (lock.packages && lock.packages[""]) delete lock.packages[""].version;
        console.log(file, JSON.stringify(lock));
      } catch { console.log(file, "unparsed", raw); }
    }' "$@"
}

# npm writes node_modules/.package-lock.json last, so a tree without it never
# finished installing.
npm_tree_complete() { [ -f "$1/node_modules/.package-lock.json" ]; }

npmseed() {
  local h; h="$( (node -v; lock_fingerprint package-lock.json frontend/package-lock.json frontend/src/docs/package-lock.json packages/product-ui/package-lock.json packages/shared/package-lock.json) | shasum | cut -d' ' -f1)"
  if [ "$h" != "$(cat "$H/npm.hash" 2>/dev/null)" ] || ! npm_tree_complete . || ! npm_tree_complete frontend || ! npm_tree_complete frontend/src/docs || ! npm_tree_complete packages/product-ui; then
    rm -f "$H/npm.hash" # an interrupted install must not match a later run's stamp
    npm ci --ignore-scripts --no-audit --no-fund && (cd frontend && npm ci --ignore-scripts --no-audit --no-fund) \
      && (cd frontend/src/docs && npm ci --ignore-scripts --no-audit --no-fund) \
      && (cd packages/product-ui && npm ci --ignore-scripts --no-audit --no-fund) \
      && (cd frontend && npm rebuild better-sqlite3) && echo "$h" >"$H/npm.hash"
  fi
}

mobile_seed() { # packages/mobile: npm ci (with patch-package postinstall) when the lock changes
  local h; h="$((node -v; cat packages/mobile/package-lock.json packages/mobile/patches/* 2>/dev/null) | shasum | cut -d' ' -f1)"
  if [ "$h" != "$(cat "$H/mobile.npm.hash" 2>/dev/null)" ] || [ ! -d packages/mobile/node_modules ]; then
    (cd packages/mobile && npm ci --no-audit --no-fund) && echo "$h" >"$H/mobile.npm.hash"
  fi
}

# Failed jobs and flaky retries come from verify's own summary ("FAIL: a b" and "  flaky    x" lines).
failed_jobs() { sed -n 's/^FAIL: //p' "$1" | tail -1; }
flaky_items() { sed -n 's/^  flaky    //p' "$1"; }
flaky_detail() { sed -n 's/^  flaky-detail | //p' "$1"; } # first-run assertion message + stack of each retried failure
job_output() { # job_output <log> <job>: that job's section of the verify output
  awk -v n="$2" '$0 == "=== " n {f=1; next} index($0, "--- " n " ") == 1 {f=0} f' "$1"
}

last_green() { # last_green <sha>: newest passing main commit that is an ancestor of <sha>, else empty
  local g; for g in $(grep '^main	' "$TSV" 2>/dev/null | awk -F'\t' '$3=="pass"{print $2}' | tail -5 | sed '1!G;h;$!d'); do
    git -C "$WORK" merge-base --is-ancestor "$g" "$1" 2>/dev/null && { echo "$g"; return; }
  done
}
# B355: a build request made at a main commit this run does not include supersedes the in-flight run.
# True when the request marker names a commit that origin/main contains, origin/main has moved past <sha>,
# and <sha> itself lacks it. Never true for a run that already includes the requested commit.
superseded_by_request() {
  local sha="$1" m="$HOME/.tallpine/release-builds/request-build" need head
  [ -f "$m" ] || return 1
  need="$(head -n1 "$m" 2>/dev/null | tr -d '[:space:]')"; [ -n "$need" ] || return 1
  # Local check first: a run that already includes the request never needs a GitHub fetch.
  if git -C "$WORK" cat-file -e "$need^{commit}" 2>/dev/null && git -C "$WORK" merge-base --is-ancestor "$need" "$sha" 2>/dev/null; then return 1; fi
  ci_git -C "$WORK" fetch -q origin main 2>/dev/null
  git -C "$WORK" cat-file -e "$need^{commit}" 2>/dev/null || return 1
  git -C "$WORK" merge-base --is-ancestor "$need" "$sha" 2>/dev/null && return 1
  head="$(git -C "$WORK" rev-parse origin/main 2>/dev/null)"
  [ -n "$head" ] && [ "$head" != "$sha" ] && git -C "$WORK" merge-base --is-ancestor "$need" "$head" 2>/dev/null
}
# run_verify_supervised <sha> <vlog> <rcfile> <verify args...>: runs scripts/verify in its own session and
# polls for supersession every SUPERSEDE_CHECK s (default 30). Returns 3 if it killed the run (process group).
# The supervised run is in its own session, so the loop must reap it itself when the service manager stops it.
SUP_PGID=""
kill_supervised() { [ -n "$SUP_PGID" ] || return 0; kill -TERM -- "-$SUP_PGID" 2>/dev/null; sleep 2; kill -KILL -- "-$SUP_PGID" 2>/dev/null; SUP_PGID=""; }
install_kill_trap() { trap 'kill_supervised; exit 143' TERM INT HUP; trap kill_supervised EXIT; }
# snapshot_tree <pgid> <file>: B361: the supervised run's processes (its process group, plus this loop) as
# `ps -o pid,ppid,pgid,stat,command`; overwritten each time, so after a silent death the last one shows what
# was alive and who the parent was.
# snapshot_unreadable <ps output>: true when a live (non-zombie) process shows "(name)" instead of its command.
snapshot_unreadable() { printf '%s\n' "$1" | awk 'NR > 1 && $4 !~ /Z/ && $5 ~ /^\(/ { bad = 1 } END { exit !bad }'; }
snapshot_tree() {
  local out try
  for try in 1 2 3; do
    out="$(${PS_CMD:-ps} -axo pid,ppid,pgid,stat,command | awk -v g="$1" -v me="$$" 'NR == 1 || $3 == g || $1 == me')"
    # macOS ps prints "(name)" instead of the command line while a process is mid-fork/exec or under load;
    # such a snapshot names nothing useful, so retry rather than let it overwrite a good one.
    snapshot_unreadable "$out" || break
    sleep 0.2
  done
  # Still unreadable: keep an earlier readable snapshot instead of replacing it with this one.
  if snapshot_unreadable "$out" && [ -s "$2" ]; then return 0; fi
  # An empty group means the run just ended: keep the previous snapshot, which still shows it alive.
  [ "$(printf '%s\n' "$out" | wc -l)" -gt 2 ] || return 0
  { date '+# %H:%M:%S'; printf '%s\n' "$out"; } >"$2.tmp" && mv -f "$2.tmp" "$2"
}
run_verify_supervised() {
  local sha="$1" vlog="$2" rcf="$3" pid n=0; shift 3
  rm -f "$rcf" "${rcf%.rc}.ps" # a stale snapshot from the previous run must never be reported
  # B602: the job's TMPDIR is /tmp; a run killed mid-test would strand its go-build and t.TempDir
  # trees there. Keep them under a directory this run owns and wipes (a kill is swept next run).
  local scratch="$H/tmp/verify-scratch"
  rm -rf "$scratch"; mkdir -p "$scratch"
  TMPDIR="$scratch" python3 -I -c 'import os,sys
os.setsid()
os.execv("/bin/bash",["bash","-c",sys.argv[1],"bash"]+sys.argv[2:])' \
    'vlog="$1"; rcf="$2"; shift 2; scripts/verify "$@" 2>&1 | tee "$vlog"; echo "${PIPESTATUS[0]}" >"$rcf"' "$vlog" "$rcf" "$@" &
  pid=$!; SUP_PGID="$pid"
  while kill -0 "$pid" 2>/dev/null; do
    sleep 1; n=$((n+1))
    if [ $((n % ${SNAPSHOT_EVERY:-60})) -eq 0 ]; then snapshot_tree "$pid" "${rcf%.rc}.ps"; fi
    if [ $((n % ${SUPERSEDE_CHECK:-30})) -eq 0 ] && superseded_by_request "$sha"; then
      echo "[$(date)] superseded: build requested for a newer main; stopping run on ${sha:0:8}"
      kill_supervised; wait "$pid" 2>/dev/null; rm -rf "$scratch"; return 3
    fi
  done
  wait "$pid" 2>/dev/null; SUP_PGID=""; rm -rf "$scratch"; return 0
}
# describe_verify_rc <rcfile>: "exit N", "killed by SIG<name> (exit N)" for N>128, or "no exit status
# recorded (the supervising shell died before verify reported)". Goes into the log of a verify-crashed run.
describe_verify_rc() {
  local n; n="$(cat "$1" 2>/dev/null)"
  case "$n" in
    '') echo "no exit status recorded (the supervising shell died before verify reported)" ;;
    *[!0-9]*) echo "unreadable exit status '$n'" ;;
    *) if [ "$n" -gt 128 ]; then echo "killed by SIG$(kill -l "$((n-128))" 2>/dev/null || echo "$((n-128))") (exit $n)"; else echo "exit $n"; fi ;;
  esac
}
run_suite() {
  SUPERSEDED_NOW=0 # only a run that ends superseded may set it (early returns leave it 0)
  local sha="$1" s8="${1:0:8}"; local log="$OUT/local-ci-main-$s8.log" items="$OUT/local-ci-main-$s8-failures.txt" vlog="$H/tmp/verify.out"
  if [ "$(git -C "$WORK" rev-parse HEAD)" != "$sha" ]; then printf 'main\t%s\tfail\tcheckout mismatch\n' "$sha" >>"$TSV"; return 0; fi
  local rc=0 setup="" superseded=0
  rm -f "$vlog" # never report a previous commit's flaky items
  # B350: the loop run is cached + concurrent + skips steps unaffected since the last green commit.
  # One full uncached serial run per day (and any run with no green base) keeps the old guarantees.
  local green vargs nightly="$H/full-uncached.stamp" stamp=0
  green="$(last_green "$sha")"; vargs=(--loop --since "$green")
  if [ -z "$green" ] || [ ! -f "$nightly" ] || [ $(( $(date +%s) - $(stat -f %m "$nightly") )) -ge 86400 ]; then vargs=(); stamp=1; fi
  {
    echo "main head $sha"; cd "$WORK" || exit 1
    echo "=== [$(date +%H:%M:%S)] npm-seed"
    npmseed && mobile_seed || { echo "--- npm-seed FAILED"; setup=npm-seed; }
    if [ -z "$setup" ]; then
      echo "=== [$(date +%H:%M:%S)] scripts/verify --all"
      run_verify_supervised "$sha" "$vlog" "$H/tmp/verify.rc" --all "${vargs[@]}"
      if [ $? -eq 3 ]; then superseded=1; else rc="$(cat "$H/tmp/verify.rc" 2>/dev/null || echo 1)"; echo "=== [$(date +%H:%M:%S)] verify finished: $(describe_verify_rc "$H/tmp/verify.rc")"; [ "$rc" != 0 ] && [ -f "$H/tmp/verify.ps" ] && { echo "=== last process snapshot of the verify tree:"; cat "$H/tmp/verify.ps"; }; fi
    fi
  } >"$log" 2>&1
  if [ "$superseded" = 1 ]; then SUPERSEDED_NOW=1; printf 'main\t%s\tsuperseded\tstopped: build requested for a newer main\n' "$sha" >>"$TSV"; return 0; fi
  # The nightly stamp moves only after a full run actually executed (not on a setup failure).
  [ "$stamp" = 1 ] && [ -z "$setup" ] && touch "$nightly"
  local fails flaky; fails="$setup"; flaky="$(flaky_items "$vlog" 2>/dev/null)"
  [ -n "$setup" ] || { fails="$(failed_jobs "$vlog")"; [ "$rc" -ne 0 ] && [ -z "$fails" ] && fails="verify-crashed"; }
  {
    for j in $fails; do
      echo "## $j"
      case "$j" in
        go-test) job_output "$vlog" go-test | grep -E '^(--- FAIL|FAIL|panic)' | sort -u | head -40 ;;
        go-lint) job_output "$vlog" go-lint | grep -vE '^[[:space:]]|^\^|^exit status|^[0-9]+ issues:' | head -40 ;;
        npm-seed|verify-crashed|verify-killed-by-*) tail -30 "$log" ;;
        *) job_output "$vlog" "$j" | grep -iE 'error|fail|not ok|need gofmt|TS[0-9]+|leak|stale|NEW |GREW ' | head -30 ;;
      esac
    done
    [ -n "$flaky" ] && { echo "## [flaky-retried] (failed first run, passed on retry)"; echo "$flaky"; flaky_detail "$vlog" 2>/dev/null; }
  } >"$items"
  local note=""; [ -n "$flaky" ] && note=" [flaky-retried: $(echo "$flaky" | grep -c .)]"
  note="$note$(sed -n 's/^verify-note: / [/p' "$vlog" 2>/dev/null | tail -1 | sed 's/$/]/')"
  if [ -z "$fails" ]; then printf 'main\t%s\tpass\tall jobs green%s\n' "$sha" "$note" >>"$TSV"
  else printf 'main\t%s\tfail\tmain debt (details in local-ci-main-%s-failures.txt): %s%s\n' "$sha" "$s8" "$(echo $fails | tr ' ' ',')" "$note" >>"$TSV"; fi
}

# Build on green: 'scripts/release-local request-build' leaves a marker; the first passing main commit
# consumes it and builds detached in its own session (never installs; the orchestrator installs).
build_on_green() {
  local sha="$1" m="$HOME/.tallpine/release-builds/request-build"
  [ -f "$m" ] || return 0
  grep -q "^main	$sha	pass	" "$TSV" || return 0
  # B341: the marker holds main's head at request time; a commit older than it must not fulfil the request.
  local need; need="$(head -n1 "$m" 2>/dev/null | tr -d '[:space:]')"
  if [ -n "$need" ] && ! git -C "$WORK" merge-base --is-ancestor "$need" "$sha" 2>/dev/null; then
    echo "[$(date)] build requested at ${need:0:8}; ${sha:0:8} does not include it, keeping the request"; return 0
  fi
  rm -f "$m"
  echo "[$(date)] build requested: building $sha detached (log $OUT/build-${sha:0:8}.log)"
  # The detached child builds, then sends ONE line to the orchestrator (B215). It runs bash -c with
  # the send_to_orchestrator/tallpine_cli definitions copied in via declare -f.
  local defs child
  load_notify_lib
  defs="$(declare -f tallpine_cli send_to_orchestrator load_notify_lib tp_notify_orchestrator); H='$H'; PROJECT_ID='$PROJECT_ID'"
  child='"$3" build "$2" --no-fetch </dev/null; rc=$?
if [ "$rc" -eq 0 ]; then
  app="$(grep "^Built: " "$1" | tail -1 | cut -c8-)"; msg="[local-ci] build ${2:0:10} OK: ${app:-unknown}. Log: $1"
else msg="[local-ci] build ${2:0:10} FAILED (rc $rc). Log: $1"; fi
send_to_orchestrator "$msg"'
  env -i HOME="$HOME" PATH="$PATH" USER="$USER" LOGNAME="${LOGNAME:-$USER}" TMPDIR="${TMPDIR:-/tmp}" \
    python3 -I -c 'import os,sys
os.setsid()
log=os.open(sys.argv[1],os.O_WRONLY|os.O_CREAT|os.O_TRUNC,0o644)
os.dup2(log,1); os.dup2(log,2)
os.execv("/bin/bash",["bash","-c",sys.argv[2],"bash",sys.argv[3],sys.argv[4],sys.argv[5]])' \
    "$OUT/build-${sha:0:8}.log" "$defs"$'\n'"$child" "$OUT/build-${sha:0:8}.log" "$sha" "$WORK/scripts/release-local" </dev/null >/dev/null 2>&1 &
}

# Weekly report-only security scan (B40). Never affects the main pass/fail row.
security_weekly() {
  local stamp="$H/security-scan.stamp" sha="$1"
  [ -x "$WORK/scripts/security-scan" ] || return 0
  [ -f "$stamp" ] && [ $(( $(date +%s) - $(stat -f %m "$stamp") )) -lt 604800 ] && return 0
  local d="$OUT/security/$(date +%Y-%m-%d)-${sha:0:8}" app="$HOME/Applications/Tallpine.app"
  mkdir -p "$OUT/security"; echo "[$(date)] security scan $sha -> $d"
  local args=("$d"); [ -d "$app" ] && args+=(--app "$app")
  ( cd "$WORK" && nice -n 15 scripts/security-scan "${args[@]}" ) >"$d.log" 2>&1
  printf '%s\t%s\t%s\n' "$(date +%F)" "$sha" "$(tr '\n' ';' <"$d/summary.txt" 2>/dev/null)" >>"$OUT/security-scan.tsv"
  touch "$stamp"
}

# Daily housekeeping: worker scratch dirs in /private/tmp older than 2 days that no
# running process references, and Go build-cache entries unused for 2 days.
housekeeping_daily() {
  local stamp="$H/housekeeping.stamp"
  [ -f "$stamp" ] && [ $(( $(date +%s) - $(stat -f %m "$stamp") )) -lt 86400 ] && return 0
  local inuse; inuse="$(ps -axo command= | grep -oE '/(private/)?tmp/[^/ ]+' | sed 's#^/private##;s#^/tmp/##' | sort -u)"
  local n=0 d
  while IFS= read -r d; do
    d="${d#/private/tmp/}"
    case "$d" in vo-briefs|vo-stray-app-*|claude-*|com.apple*|tmux-*|powerlog|"") continue ;; esac
    printf '%s\n' "$inuse" | grep -qxF "$d" && continue
    chmod -R u+w "/private/tmp/$d" 2>/dev/null; rm -rf "/private/tmp/$d" 2>/dev/null && n=$((n+1))
  done < <(find /private/tmp -maxdepth 1 -mindepth 1 -type d -user "$(id -un)" -mmin +2880 2>/dev/null)
  # B492: never delete from the shared Go build cache here; Go trims it itself and a concurrent build lost entries.
  echo "[$(date)] housekeeping: removed $n stale /private/tmp dirs; free $(df -h / | awk 'NR==2{print $4}')"
  touch "$stamp"
}

# Push main results to ONE orchestrator (the main-health owner): the live session named in
# $H/notify-target (one line), else the oldest live orchestrator of the project; picked by
# `tallpine orchestrator notify`. Fails: always. Pass: only when the previous main row was a fail (all-clear).
tallpine_cli() {
  local c="$HOME/Applications/Tallpine.app/Contents/Resources/daemon/tallpine"
  [ -x "$c" ] && { echo "$c"; return; }
  command -v tallpine
}
notify_orchestrators() {
  local sha="$1" row prev msg
  row="$(grep "^main	$sha	" "$TSV" | tail -1)"; [ -n "$row" ] || return 0
  prev="$(grep '^main	' "$TSV" | grep -v "^main	$sha	" | grep -v '	superseded	' | tail -1 | cut -f3)"
  case "$(echo "$row" | cut -f3)" in
    fail) msg="[local-ci] main ${sha:0:10} FAILED: $(echo "$row" | cut -f4). Log: $OUT/local-ci-main-${sha:0:8}.log" ;;
    pass) [ "$prev" = fail ] || return 0; msg="[local-ci] main ${sha:0:10} is green again (all jobs passed)." ;;
    *) return 0 ;;
  esac
  send_to_orchestrator "$msg"
}
# Shared by notify_orchestrators and the detached build child (B215).
# tp_notify_orchestrator lives in scripts/lib/notify.sh, shared with release-local. This file runs from an
# installed copy: the lib sits next to it, else in this repo tree or the main checkout.
load_notify_lib() {
  type tp_notify_orchestrator >/dev/null 2>&1 && return 0
  local f here; here="$(dirname "${BASH_SOURCE[0]}")"
  # scripts/local-ci/install copies the lib next to the installed loop.sh, so the alert for an unreachable
  # origin does not depend on the checkout. A file without the function (an older checkout) does not count.
  for f in "$here/notify.sh" "$here/../lib/notify.sh" "$WORK/scripts/lib/notify.sh"; do
    # shellcheck disable=SC1090
    [ -f "$f" ] && . "$f" 2>/dev/null
    type tp_notify_orchestrator >/dev/null 2>&1 && return 0
  done
  return 1
}
send_to_orchestrator() {
  local msg="$1" cli target
  load_notify_lib || { echo "[$(date)] notify: scripts/lib/notify.sh not found, skipping"; return 0; }
  cli="$(tallpine_cli)"; [ -n "$cli" ] || { echo "[$(date)] notify: no CLI found"; return 0; }
  target="$(head -1 "$H/notify-target" 2>/dev/null | tr -d '[:space:]')"
  echo "[$(date)] $(tp_notify_orchestrator "$cli" "$PROJECT_ID" "$target" "$msg")"
}

# B590: the repo is private and the machine-wide gh login can be any account, so which account reaches it
# is decided here, per repo: $H/github-user if set, else the ambient login, else any other gh account that can.
export GIT_TERMINAL_PROMPT=0
CI_GH_LOGIN=""; CI_OUT=""; FETCH_ERR="$H/.fetch.err"; FETCH_STATE="$H/fetch-failing"
pinned_gh_login() { head -1 "$H/github-user" 2>/dev/null | tr -cd 'A-Za-z0-9-'; }
gh_logins() { gh auth status --hostname github.com 2>&1 | sed -nE 's/.*Logged in to github\.com (account|as) ([A-Za-z0-9-]+).*/\2/p' | awk '!seen[$0]++'; }
# ci_git <git args...>: git, as CI_GH_LOGIN when one is chosen. The token stays out of argv: git asks gh for it.
ci_git() {
  [ -n "$CI_GH_LOGIN" ] || { git "$@"; return; }
  git -c credential.helper= -c "credential.helper=!f() { [ \"\$1\" = get ] && { echo username=x-access-token; echo \"password=\$(gh auth token --hostname github.com --user $CI_GH_LOGIN)\"; }; }; f" "$@"
}
# ci_try <git args...>: stdout in CI_OUT, the last stderr in FETCH_ERR. Tries the current account, then others.
ci_try() {
  local pinned login
  pinned="$(pinned_gh_login)"; [ -z "$pinned" ] || CI_GH_LOGIN="$pinned"
  CI_OUT="$(ci_git "$@" 2>"$FETCH_ERR")" && return 0
  [ -z "$pinned" ] || return 1
  for login in $(gh_logins); do
    [ "$login" != "$CI_GH_LOGIN" ] || continue
    CI_OUT="$(CI_GH_LOGIN="$login" ci_git "$@" 2>"$FETCH_ERR")" || continue
    echo "[$(date)] origin is reachable as gh account $login; using it for this repo"
    CI_GH_LOGIN="$login"; return 0
  done
  return 1
}
# Fetch failures must not read as "nothing new on main" (B590: 1283 silent failures, main untested for days).
# $FETCH_STATE: "<consecutive failures> <first epoch> <last notified epoch>", then the error on line 2.
note_fetch_failure() {
  local now err count=0 first notified=0 mins
  now="$(date +%s)"; first="$now"
  err="$(tail -c 300 "$FETCH_ERR" 2>/dev/null | sed -E 's#(https?://)[^/@ ]+@#\1#g' | tr -s '\n\t' ' ')"
  err="${err:-origin returned no main branch}"
  [ ! -f "$FETCH_STATE" ] || read -r count first notified <"$FETCH_STATE"
  count=$((count + 1))
  echo "[$(date)] cannot reach origin (failure $count): $err"
  if [ "$count" -ge "${FETCH_FAIL_NOTIFY_AFTER:-3}" ] && [ $((now - notified)) -ge "${FETCH_RENOTIFY_EVERY:-21600}" ]; then
    mins=$(((now - first) / 60)); notified="$now"
    send_to_orchestrator "[local-ci] cannot reach origin for ${mins}m ($count failed checks): $err. Main is not being tested and build requests are waiting. Check gh auth status, or pin an account that can reach the repo in $H/github-user."
  fi
  printf '%s %s %s\n%s\n' "$count" "$first" "$notified" "$err" >"$FETCH_STATE"
}
note_fetch_ok() {
  local count first notified=0
  [ -f "$FETCH_STATE" ] || return 0
  read -r count first notified <"$FETCH_STATE"
  rm -f "$FETCH_STATE"
  echo "[$(date)] origin reachable again after $count failed checks"
  [ "${notified:-0}" = 0 ] || send_to_orchestrator "[local-ci] origin is reachable again after $count failed checks; main is being tested."
}
# The tick for an unreachable origin: record it, keep the maintenance going, never evaluate a stale origin/main.
fetch_failed_tick() {
  note_fetch_failure
  [ "$1" != "--once" ] || exit 1
  [ -z "${sha:-}" ] || security_weekly "$sha"
  housekeeping_daily; sleep "$POLL"
}

[ "${LOOP_SOURCE_ONLY:-0}" = 1 ] && return 0 2>/dev/null # tests source the functions only
install_kill_trap
while true; do
  date +%s >"$H/heartbeat"  # B207: epoch of the last loop tick; stale (> ~POLL*3) means the loop is dead
  # Cheap check first: fetch (and maybe run CI) only when the remote main moved from what we last saw.
  if [ ! -d "$WORK/.git" ] && ! ci_try clone -q "$REPO_URL" "$WORK"; then fetch_failed_tick "$1"; continue; fi
  remote=""; ! ci_try -C "$WORK" ls-remote origin refs/heads/main || remote="$(printf '%s' "$CI_OUT" | cut -f1)"
  if [ -z "$remote" ]; then fetch_failed_tick "$1"; continue; fi
  if [ "$1" != "--once" ] && [ "$remote" = "$sha" ]; then
    note_fetch_ok; security_weekly "$sha"; housekeeping_daily; sleep "$POLL"; continue
  fi
  if ! ci_try -C "$WORK" fetch -q origin main; then fetch_failed_tick "$1"; continue; fi
  note_fetch_ok
  sha="$(git -C "$WORK" rev-parse origin/main)"
  if [ "$1" = "--once" ] || ! grep -q "^main	$sha	" "$TSV" 2>/dev/null; then
    git -C "$WORK" checkout -q --force --detach "$sha" && git -C "$WORK" clean -qfdx -e node_modules
    gc_kb=$(du -sk "$H/gocache" 2>/dev/null | cut -f1); [ "${gc_kb:-0}" -gt 20971520 ] && { echo "[$(date)] gocache over 20G, clearing"; rm -rf "$H/gocache"; }
    echo "[$(date)] evaluating main $sha"; run_suite "$sha"; echo "[$(date)] done $sha: $(tail -1 "$TSV")"; notify_orchestrators "$sha"; build_on_green "$sha"
  fi
  [ "$SUPERSEDED_NOW" = 1 ] && [ "$1" != "--once" ] && { SUPERSEDED_NOW=0; sha=""; continue; } # start the newer main now, no POLL sleep
  [ "$1" = "--once" ] || security_weekly "$sha"
  [ "$1" = "--once" ] || housekeeping_daily
  [ "$1" = "--once" ] && break
  sleep "$POLL"
done
