#!/usr/bin/env bash
# Sourced by scripts/release-local (never run directly): release locks, detached runs, install-failure notification, exit/interrupt undo, per-commit build lock.
# take_over_stale_lock <lock> <claim> <lock-stale-fn> <claim-stale-fn> <busy-msg> <label>: called after mkdir of
# <lock> failed. Dies with <busy-msg> unless the lock is stale. Only the run whose mkdir of <claim> succeeds may
# replace a stale lock, and it re-checks staleness under that claim, so a lock another run just re-created is never
# removed. A claim left by a killed run is itself stale and is moved aside atomically (only one mv of the same
# source can succeed), so it cannot block later runs forever. On return the caller holds a fresh <lock>.
take_over_stale_lock() {
  local l="$1" tk="$2" lock_stale="$3" claim_stale="$4" busy="$5" label="$6"
  "${lock_stale}" "${l}" || die "${busy}"
  if ! mkdir "${tk}" 2>/dev/null; then
    "${claim_stale}" "${tk}" || die "${label} takeover already in progress (${tk}); retry"
    mv "${tk}" "${tk}.dead.$$" 2>/dev/null && rm -rf "${tk}.dead.$$"
    mkdir "${tk}" 2>/dev/null || die "${label} takeover already in progress (${tk}); retry"
  fi
  if "${lock_stale}" "${l}"; then
    warn "stale ${label}; taking it over"
    rm -rf "${l}"
    mkdir "${l}" 2>/dev/null || { rmdir "${tk}"; die "${label} was taken by another run during takeover"; }
  else
    rmdir "${tk}"
    die "${busy}"
  fi
  rmdir "${tk}"
}
install_claim_stale() { [[ -n "$(find "$1" -maxdepth 0 -mmin +1 2>/dev/null)" ]]; }

# An install lock records "<pid> <start time>". It is stale when that process is gone or the pid was reused
# (start time differs). A lock with no owner file is a run killed before writing it (or an older release's
# bare lock): stale once it is over a minute old, so a run that just created it is not robbed.
install_lock_stale() {
  local l="$1" pid start
  if [[ ! -s "${l}/owner" ]]; then
    install_claim_stale "${l}"
    return
  fi
  read -r pid start <"${l}/owner"
  [[ "${pid}" =~ ^[0-9]+$ ]] || return 0
  kill -0 "${pid}" 2>/dev/null || return 0
  [[ "$(LC_ALL=C TZ=UTC ps -o lstart= -p "${pid}" 2>/dev/null | tr -s ' ')" != "${start}"* ]]
}

lock_releases() {
  mkdir -p "${releases_dir}"
  local l="${releases_dir}/.lock"
  if ! mkdir "${l}" 2>/dev/null; then
    take_over_stale_lock "${l}" "${l}.takeover" install_lock_stale install_claim_stale \
      "another release-local run holds ${l} ($(cat "${l}/owner" 2>/dev/null || echo "owner unknown"); remove it if no run is active)" "install lock"
  fi
  lock_dir="${l}"
  printf '%s %s\n' "$$" "$(LC_ALL=C TZ=UTC ps -o lstart= -p "$$" 2>/dev/null | tr -s ' ')" >"${l}/owner"
  trap on_exit EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  trap 'exit 129' HUP
  # With the lock held, a .partial point can only be left from a killed run.
  rm -rf "${releases_dir}"/*.partial
}

# write_result <ok|failed> <message>: the outcome for whoever asked for this install or rollback, in
# <--log>.result next to the log they redirect output to: line 1 "ok" or "failed", line 2 the message.
# It replaces --notify (B631): the daemon refuses an owner CLI run from here, so nothing is sent; the
# requesting session polls the file with its own CLI. Written whole (temp file, then rename), so a poller
# never reads half of it. Without --log nothing is written; a failure to write is logged, never fatal.
result_path() { [[ -n "${opt_log:-}" ]] && printf '%s.result' "${opt_log}"; }
clear_result() { local f; f="$(result_path)" && rm -f "${f}"; return 0; }
write_result() {
  local f tmp
  f="$(result_path)" || return 0
  [[ -n "${TALLPINE_RELEASE_TEST_RESULT_DELAY:-}" ]] && sleep "${TALLPINE_RELEASE_TEST_RESULT_DELAY}"
  tmp="${f}.tmp.$$"
  if printf '%s\n%s\n' "$1" "$2" 2>/dev/null >"${tmp}" && mv -f "${tmp}" "${f}"; then
    say "Result written to ${f}"
  else
    rm -f "${tmp}" 2>/dev/null
    warn "could not write the result to ${f} (the ${notify_what:-install} is unaffected)"
  fi
  return 0
}
sha10() { local c="${new_commit:-unknown}"; printf '%s' "${c:0:10}"; }
notify_log_path() { printf '%s' "${opt_log:-not recorded}"; }
notify_failure() {
  local reason="${last_error:-${notify_what:-install} failed}"
  reason="${reason%%$'\n'*}"
  if [[ "${notify_what:-install}" == rollback ]]; then
    write_result failed "[release] rollback FAILED: ${reason}; log $(notify_log_path)"
  else
    write_result failed "[release] install of $(sha10) FAILED: ${reason}; log $(notify_log_path)"
  fi
}

staged_cleanup=""
lock_dir=""
# What install/rollback has changed so far; on_exit undoes it on any failure.
phase=""
cur_point=""
cur_discard=""
restore_stage=0
on_exit() {
  local rc=$?
  trap - EXIT
  # Ignored, not reset: a second Ctrl-C or a closed terminal must not stop
  # the undo halfway (children inherit the ignore too).
  trap '' INT TERM HUP
  if [[ "${rc}" -ne 0 && -n "${phase}" ]]; then
    warn "stopped during '${phase}'; undoing it (Ctrl-C is ignored until it finishes)"
    case "${phase}" in
    install-stopped) undo_install_stopped ;;
    install-swapping) undo_install_swapping ;;
    install-migrating) undo_install_migrating ;;
    rollback-migrated) warn "the state was moved back to ${state_dir} but its data was not restored; restore it from ${point}/data by hand" ;;
    rollback-stopped) undo_rollback_stopped ;;
    rollback-restoring) undo_rollback ;;
    esac
  fi
  [[ -n "${staged_cleanup}" ]] && rm -rf "${staged_cleanup}"
  [[ "${rc}" -ne 0 && "${notify_armed:-0}" -eq 1 ]] && { notify_armed=0; notify_failure; }
  [[ -n "${lock_dir}" ]] && rm -rf "${lock_dir}" 2>/dev/null
  exit "${rc}"
}

# follow_range <file> <from> <upto>: prints bytes from+1..upto only. The caller advances its offset to
# upto, so reading to EOF instead would reprint whatever the run appended after the size was taken.
follow_range() {
  [[ -n "${TALLPINE_RELEASE_TEST_FOLLOW_DELAY:-}" ]] && sleep "${TALLPINE_RELEASE_TEST_FOLLOW_DELAY}"
  # head reads the file itself and tail drains the pipe, so no writer gets SIGPIPE under pipefail.
  head -c "$3" "$1" | tail -c "+$(($2 + 1))"
}

# detach_run: re-runs this install/rollback as a child of launchd in its own session, with the agent
# session's identity dropped, and follows its output. The session teardown (sessionreap) stops a
# session's processes by TALLPINE_SESSION_ID, by executables under its temp dir and by DESCENT from
# those, then SIGKILLs the rest; `setsid` alone keeps the parent link, so an install started from an
# orchestrator session was killed as soon as the app it was replacing quit that session (B327).
# Only for non-interactive --yes runs; a person at a terminal keeps Ctrl-C. Called after the checks
# that need the caller's identity (guard_live_target), before anything is locked or changed.
detach_run() {
  if [[ "${TALLPINE_RELEASE_DETACHED:-}" == 1 ]]; then
    # Test-only: record the session marker this detached run still carries (the reaper's first selector).
    [[ -n "${TALLPINE_RELEASE_TEST_ENVFILE:-}" ]] && printf '%s' "${TALLPINE_SESSION_ID:-}" >"${TALLPINE_RELEASE_TEST_ENVFILE}"
    return 0
  fi
  [[ "${TALLPINE_RELEASE_NO_DETACH:-}" == 1 ]] && return 0
  # Without a tty, or inside an agent session whose shell has a PTY on stdin (SIGKILL follows the
  # SIGTERM shield, so only leaving the session's process tree helps). A user's terminal tab is not
  # an agent session: TALLPINE_SHELL_TERMINAL marks it and keeps Ctrl-C.
  [[ "${opt_yes}" -eq 1 ]] || return 0
  [[ ! -t 0 || ( -n "${TALLPINE_SESSION_ID:-}" && -z "${TALLPINE_SHELL_TERMINAL:-}" ) ]] || return 0
  local dir rc="" outn=0 errn=0 n pid="" waits=0
  dir="$(mktemp -d "${TMPDIR:-/tmp}/release-local-detached.XXXXXX")" || return 0
  : >"${dir}/out"; : >"${dir}/err"
  env -u TALLPINE_SESSION_ID -u TALLPINE_SESSION_TOKEN -u TALLPINE_SESSION_TOKEN_RUN_FILE -u TALLPINE_SESSION_AUTH -u TALLPINE_SESSION_AUTH_RUN_FILE \
    -u TALLPINE_PROJECT_ID -u VO_SESSION_ID TALLPINE_RELEASE_DETACHED=1 \
    python3 -I -c 'import os,sys
if os.fork(): os._exit(0)
os.setsid()
if os.fork(): os._exit(0)
d=sys.argv[1]
os.dup2(os.open("/dev/null",os.O_RDONLY),0)
os.dup2(os.open(d+"/out",os.O_WRONLY|os.O_APPEND),1)
os.dup2(os.open(d+"/err",os.O_WRONLY|os.O_APPEND),2)
os.execv("/bin/bash",["bash","-c","echo $$ >\"$0/pid\"; \"$@\"; echo $? >\"$0/rc.tmp\"; mv \"$0/rc.tmp\" \"$0/rc\"",d]+sys.argv[2:])' \
    "${dir}" "${release_self}" "${release_args[@]}" || return 0
  # Follow the detached run; if this process is killed instead, the run carries on.
  notify_armed=0; phase=""; trap - EXIT INT TERM HUP
  while :; do
    n="$(wc -c <"${dir}/out")"; [[ "${n}" -gt "${outn}" ]] && { follow_range "${dir}/out" "${outn}" "${n}"; outn="${n}"; }
    n="$(wc -c <"${dir}/err")"; [[ "${n}" -gt "${errn}" ]] && { follow_range "${dir}/err" "${errn}" "${n}" >&2; errn="${n}"; }
    if [[ -f "${dir}/rc" ]]; then
      n="$(wc -c <"${dir}/out")"; [[ "${n}" -gt "${outn}" ]] && follow_range "${dir}/out" "${outn}" "${n}"
      n="$(wc -c <"${dir}/err")"; [[ "${n}" -gt "${errn}" ]] && follow_range "${dir}/err" "${errn}" "${n}" >&2
      rc="$(cat "${dir}/rc")"; break
    fi
    pid="$(cat "${dir}/pid" 2>/dev/null || true)"
    waits=$((waits + 1))
    if [[ -z "${pid}" && "${waits}" -gt 150 ]]; then rc=1; warn "the detached run did not start within 30s; see ${dir}"; break; fi
    # The run may have finished between the rc check above and this one: look again before giving up.
    if [[ -n "${pid}" ]] && ! pid_alive "${pid}"; then
      [[ -f "${dir}/rc" ]] && continue
      rc=1; warn "the detached run (pid ${pid}) ended without a result; see ${dir}"; break; fi
    sleep "${TALLPINE_RELEASE_POLL:-0.2}"
  done
  rm -rf "${dir}"
  exit "${rc}"
}

# Asks before an action that quits the app; --yes answers for scripts.
confirm() {
  printf '%s\n' "$1"
  [[ "${opt_yes}" -eq 1 ]] && return 0
  [[ -t 0 ]] || die "not a terminal; pass --yes to confirm; nothing was changed"
  local reply
  printf 'Continue? [y/N] '
  read -r reply || reply=""
  [[ "${reply}" == y || "${reply}" == Y || "${reply}" == yes ]] || die "cancelled; nothing was changed"
}

# Test-only: simulate Ctrl-C at a named step, and again during the undo.
interrupt_point() {
  if [[ "${TALLPINE_RELEASE_TEST_INTERRUPT:-}" == "$1" ]]; then kill -"${TALLPINE_RELEASE_TEST_SIGNAL:-INT}" $$; sleep 1; fi
  return 0
}

interrupt_undo() {
  [[ -n "${TALLPINE_RELEASE_TEST_INTERRUPT_UNDO:-}" ]] && kill -INT $$
  return 0
}

app_was_running=0
relaunch_if_quit() {
  as_prev _relaunch_if_quit
  return 0
}
# daemon_back: a daemon other than the one the install stopped is alive and answering.
# stopped_daemon_pid is set by install/rollback; unset, any live answering daemon counts as back.
daemon_back() {
  local pid
  pid="$(current_daemon_pid)"
  [[ -n "${pid}" && "${pid}" != "${stopped_daemon_pid:-}" ]] && pid_alive "${pid}" && probe "$(json_get "${run_file}" port)" /healthz >/dev/null
}
_relaunch_if_quit() {
  [[ "${app_was_running}" -eq 1 && -d "${app_path}" ]] || return 0
  # An undo that fires while the app is still shutting down (the SIGTERM we sent it) must wait for
  # that exit and then relaunch; checking once would leave the human with no app (B327).
  local waited=0 relaunch_wait="${TALLPINE_RELEASE_RELAUNCH_WAIT:-${stop_timeout}}"
  while [[ -n "$(app_pids)" ]]; do
    # Someone already relaunched the app (its daemon is back): waiting for an exit that will not come
    # held the undo, and its --notify, for stop_timeout (B585).
    if daemon_back; then
      say "${product_name} is already running again; not relaunching it"
      return 0
    fi
    if [[ "${waited}" -ge "$(poll_budget "${relaunch_wait}")" ]]; then
      warn "the app is still running after ${relaunch_wait}s; not relaunching it"
      return 0
    fi
    sleep "${TALLPINE_RELEASE_POLL:-0.5}"
    waited=$((waited + 1))
  done
  launch_app
  return 0
}


# Per-commit build lock, taken by cmd_build itself so loop.sh, manual and request-build builds exclude
# each other. A lock older than TALLPINE_RELEASE_BUILD_LOCK_MINUTES (45) belonged to a killed build.
build_lock_stale() { [[ -n "$(find "$1" -maxdepth 0 -mmin "+${TALLPINE_RELEASE_BUILD_LOCK_MINUTES:-45}" 2>/dev/null)" ]]; }
build_locked() { local l="${builds_dir}/.building-$1"; [[ -d "${l}" ]] && ! build_lock_stale "${l}"; }
build_lock_dir=""
acquire_build_lock() {
  local l="${builds_dir}/.building-$1" tk="${builds_dir}/.takeover-$1"
  mkdir -p "${builds_dir}"
  if ! mkdir "${l}" 2>/dev/null; then
    take_over_stale_lock "${l}" "${tk}" build_lock_stale build_lock_stale \
      "$(short "$1") is already being built (lock ${l}); remove it if no build is running" "build lock for $(short "$1")"
  fi
  build_lock_dir="${l}"
  trap 'rmdir "${build_lock_dir}" 2>/dev/null || true' EXIT
}
