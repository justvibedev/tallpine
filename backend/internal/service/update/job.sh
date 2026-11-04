#!/bin/bash
# Detached "Update & restart" job, started by the daemon in its own session so
# it survives the daemon and the app quitting. Written to <state>/update/ and
# run with bash; configuration comes from the environment:
#   UPDATE_SHA            full commit to install (required)
#   UPDATE_STATE_DIR      state root (default ~/.tallpine); logs, lock and result.json
#                         live in <state>/update
#   UPDATE_RELEASE_LOCAL  scripts/release-local of the repo checkout (build)
#   UPDATE_APP_PATH       the installed app (default ~/Applications/<name>.app)
#   UPDATE_PORT           daemon port to probe (default 3101)
#   UPDATE_SOCKET         the daemon's user-only socket; probes go there when it
#                         exists, since the daemon has no loopback TCP by default
#   UPDATE_DELAY          seconds to wait first, so the UI sees the accept
#   UPDATE_HEALTH_WAIT    seconds to wait for the daemon to answer (default 180)
#   UPDATE_POLL           seconds between health probes (default 5)
#   UPDATE_PRODUCT        app product name (default Tallpine)
#   UPDATE_OPEN_CMD       command that launches the app (default open)
#   UPDATE_COPY_CMD / UPDATE_VERIFY_CMD  test-only: replace ditto / the bundle check
#   UPDATE_APP_PGREP      pattern of the running app process
set -u

SHA="${UPDATE_SHA:?UPDATE_SHA is required}"
STATE="${UPDATE_STATE_DIR:-$HOME/.tallpine}"
DIR="$STATE/update"
RELEASE_LOCAL="${UPDATE_RELEASE_LOCAL:?UPDATE_RELEASE_LOCAL is required}"
PRODUCT="${UPDATE_PRODUCT:-Tallpine}"
LIVE="${UPDATE_APP_PATH:-$HOME/Applications/$PRODUCT.app}"
PORT="${UPDATE_PORT:-3101}"
SOCKET="${UPDATE_SOCKET:-}"
HEALTH_WAIT="${UPDATE_HEALTH_WAIT:-180}"
POLL="${UPDATE_POLL:-5}"
OPEN_CMD="${UPDATE_OPEN_CMD:-open}"
PGREP_PAT="${UPDATE_APP_PGREP:-$PRODUCT.app/Contents/MacOS}"
BUILDS="$STATE/release-builds/$SHA"
RESULT="$DIR/result.json"
STARTED="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
# The agent session's identity must not reach release-local or the app.
CLEAN=(env -u TALLPINE_SESSION_ID -u TALLPINE_DATA_DIR -u TALLPINE_RUN_FILE -u TALLPINE_PORT -u TALLPINE_TMUX_SOCKET_NAME -u TALLPINE_SESSION_TOKEN -u TALLPINE_SESSION_TOKEN_RUN_FILE -u TALLPINE_SESSION_AUTH -u TALLPINE_SESSION_AUTH_RUN_FILE -u TALLPINE_PROJECT_ID)

mkdir -p "$DIR"
log() { printf '[%s] %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"; }

# Atomic: a reader never sees a half-written result.
write_result() { # state message
  local msg="${2//\\/\\\\}"; msg="${msg//\"/\\\"}"
  printf '{"state":"%s","commit":"%s","startedAt":"%s","finishedAt":"%s","message":"%s"}\n' \
    "$1" "$SHA" "$STARTED" "$([[ "$1" == running ]] || date -u +%Y-%m-%dT%H:%M:%SZ)" "$msg" >"$RESULT.tmp" &&
    mv "$RESULT.tmp" "$RESULT"
}

# One update at a time. mkdir is atomic; a lock whose pid is gone is stale.
if ! mkdir "$DIR/lock" 2>/dev/null; then
  holder="$(cat "$DIR/lock/pid" 2>/dev/null || true)"
  if [[ -n "$holder" ]] && kill -0 "$holder" 2>/dev/null; then
    echo "update-job: another update is running (pid $holder)" >&2
    exit 3
  fi
  rm -rf "$DIR/lock"
  mkdir "$DIR/lock" 2>/dev/null || { echo "update-job: could not take the lock" >&2; exit 3; }
fi
echo $$ >"$DIR/lock/pid"
trap 'rm -rf "$DIR/lock"' EXIT

exec >>"$DIR/update.log" 2>&1
log "update to $SHA started (pid $$)"
write_result running "starting"

# Over the daemon's socket when it is there (B176), else loopback TCP: the
# restarted daemon recreates the socket, and an older one may only have TCP.
daemon_curl() {
  if [[ -n "$SOCKET" && -S "$SOCKET" ]]; then
    curl --unix-socket "$SOCKET" "$@"
  else
    curl "$@"
  fi
}
ready() { daemon_curl -fsS -m 5 "http://127.0.0.1:$PORT/readyz" >/dev/null 2>&1; }
running_commit() {
  daemon_curl -fsS -m 5 "http://127.0.0.1:$PORT/healthz" 2>/dev/null |
    sed -n 's/.*"commit"[[:space:]]*:[[:space:]]*"\([0-9a-f]*\)".*/\1/p' | head -n 1
}
wait_ready() {
  local i
  for ((i = 0; i < HEALTH_WAIT; i += POLL)); do ready && return 0; sleep "$POLL"; done
  ready
}
app_running() { pgrep -f "$PGREP_PAT" >/dev/null 2>&1; }
FALLBACK=""
# Verifies a staged bundle: an Info.plist and a valid signature.
verify_bundle() {
  if [[ -n "${UPDATE_VERIFY_CMD:-}" ]]; then "$UPDATE_VERIFY_CMD" "$1"; return; fi
  [[ -f "$1/Contents/Info.plist" ]] && codesign --verify --deep "$1" >/dev/null 2>&1
}
# Copies the bundle at $2 over $LIVE without ever leaving no bundle: stage next
# to the target (same volume), verify, keep the old one aside until the new is
# in place, and put it back if the swap fails.
try_bundle() { # label path
  local staged="$LIVE.new.$$" aside="$LIVE.old.$$"
  [[ -d "$2" ]] || return 1
  log "fallback: restoring $1 ($2)"
  mkdir -p "$(dirname "$LIVE")"
  rm -rf "$staged" "$aside"
  if ! "${UPDATE_COPY_CMD:-ditto}" "$2" "$staged" || ! verify_bundle "$staged"; then
    log "fallback: could not stage a valid copy of $2; $LIVE left as it was"
    rm -rf "$staged"
    return 1
  fi
  app_running && pkill -f "$PGREP_PAT" >/dev/null 2>&1
  if [[ -e "$LIVE" ]] && ! mv "$LIVE" "$aside"; then rm -rf "$staged"; return 1; fi
  if ! mv "$staged" "$LIVE"; then
    [[ -e "$aside" ]] && mv "$aside" "$LIVE"
    rm -rf "$staged"
    return 1
  fi
  rm -rf "$aside"
  "$OPEN_CMD" "$LIVE"
  if wait_ready; then FALLBACK="$1"; return 0; fi
  return 1
}
# Always leaves an app running: the installed one, else the newest rollback
# point, else the just-built bundle, else an earlier build.
ensure_app() {
  if app_running && ready; then return 0; fi
  if [[ -d "$LIVE" ]]; then
    app_running || { log "app not running: launching $LIVE"; "$OPEN_CMD" "$LIVE"; }
    wait_ready && return 0
    log "$LIVE did not come up healthy"
  else
    log "$LIVE is missing"
  fi
  local p
  while IFS= read -r p; do
    [[ -n "$p" ]] && try_bundle "rollback point" "$p" && return 0
  done < <(ls -1td "$STATE"/releases/*/app/"$PRODUCT.app" 2>/dev/null)
  [[ -n "${APP:-}" ]] && try_bundle "new build" "$APP" && return 0
  while IFS= read -r p; do
    [[ -n "$p" ]] && try_bundle "earlier build" "$p" && return 0
  done < <(ls -1td "$STATE"/release-builds/*/frontend/out/*/"$PRODUCT.app" 2>/dev/null)
  log "no app could be started"
  return 1
}
finish() { # state message
  local state="$1" msg="$2"
  if ! ensure_app; then
    state=failed; msg="$msg; no app could be started, restore ~/Applications by hand"
  elif [[ -n "$FALLBACK" ]]; then
    msg="$msg; relaunched from the $FALLBACK"
  fi
  log "$state: $msg"
  write_result "$state" "$msg"
  exit $([[ "$state" == succeeded ]] && echo 0 || echo 1)
}

sleep "${UPDATE_DELAY:-5}"

APP="$(find "$BUILDS/frontend/out" -maxdepth 2 -type d -name "$PRODUCT.app" 2>/dev/null | head -n 1)"
if [[ -z "$APP" ]]; then
  log "no build for $SHA: building"
  write_result running "building"
  if ! "${CLEAN[@]}" "$RELEASE_LOCAL" build "$SHA"; then
    finish failed "build failed; the installed app was not changed"
  fi
  APP="$(find "$BUILDS/frontend/out" -maxdepth 2 -type d -name "$PRODUCT.app" 2>/dev/null | head -n 1)"
  [[ -n "$APP" ]] || finish failed "build produced no app; the installed app was not changed"
fi

# After a rollback the old daemon must answer, on the commit it had before
# (when that is known), before the result is recorded.
rolled_back_ok() {
  local got
  wait_ready || return 1
  got="$(running_commit)"
  [[ -n "$got" && ( -z "$PREV" || "$got" == "$PREV" ) ]]
}
rollback() { # message-when-ok
  if "${CLEAN[@]}" "$INSTALLER" rollback --yes --allow-in-session; then
    if rolled_back_ok; then finish rolled_back "$1"; fi
    finish failed "rolled back but the daemon is not healthy; see update.log"
  fi
  finish failed "$2"
}

# Install with the new build's own release-local (the one that matches it).
INSTALLER="$BUILDS/scripts/release-local"
[[ -x "$INSTALLER" ]] || INSTALLER="$RELEASE_LOCAL"

PREV="$(running_commit)"
write_result running "installing"
log "install start"
if "${CLEAN[@]}" "$INSTALLER" install --yes --allow-in-session "$APP"; then
  :
else
  log "install exited $?"
  if wait_ready; then
    finish failed "install failed; the previous version is still running"
  fi
  log "daemon not ready after failed install: rolling back"
  write_result running "rolling back"
  rollback "install failed and the daemon was not healthy; rolled back" "install failed and the rollback failed too; see update.log"
fi

# Healthy means ready AND running the commit we installed.
write_result running "verifying"
healthy=0
if wait_ready; then
  got="$(running_commit)"
  if [[ -n "$got" && "$got" == "$SHA" ]]; then healthy=1; else log "daemon runs $got, expected $SHA"; fi
fi
if [[ "$healthy" -ne 1 ]]; then
  log "new version unhealthy: rolling back"
  write_result running "rolling back"
  rollback "the new version was not healthy; rolled back" "the new version was not healthy and the rollback failed; see update.log"
fi
finish succeeded "installed $SHA"
