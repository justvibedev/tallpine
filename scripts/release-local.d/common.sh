#!/usr/bin/env bash
# Sourced by scripts/release-local (never run directly): shared helpers: app/daemon discovery, stop, launch, health, live-target guard.
# ------------------------------------------------------------------ helpers

# While the first install after the rename replaces the legacy app, the app being
# replaced (and the daemon serving it) carries the legacy name, bundle id and
# service name. as_prev runs a command under that identity.
prev_is_legacy=0
as_prev() {
  if [[ "${prev_is_legacy}" -eq 1 ]]; then
    local product_name="${legacy_product_name}" bundle_id="${legacy_bundle_id}" service_name="${legacy_service_name}"
    local app_path="${app_dir%/}/${legacy_product_name}.app"
  fi
  "$@"
}
# The app a rollback point replaced: its file name (meta prev_app, else the current one).
point_app_name() {
  local n
  n="$(meta_get "$1" prev_app)"
  printf '%s\n' "${n:-${product_name}.app}"
}
# File name of the app being replaced right now.
prev_app_name() { [[ "${prev_is_legacy}" -eq 1 ]] && printf '%s.app\n' "${legacy_product_name}" || printf '%s.app\n' "${product_name}"; }

plist_get() { /usr/libexec/PlistBuddy -c "Print :$2" "$1/Contents/Info.plist" 2>/dev/null || true; }

json_get() { plutil -extract "$2" raw -o - "$1" 2>/dev/null || true; }

# The commit an app bundle was built from: the Info.plist stamp, else the
# bundled daemon's `tallpine --version`. Empty for builds that predate stamping.
app_commit() {
  local app="$1" commit
  commit="$(plist_get "${app}" VOBuildCommit)"
  if [[ -z "${commit}" && -x "${app}/Contents/Resources/daemon/${cli_name}" ]]; then
    commit="$("${app}/Contents/Resources/daemon/${cli_name}" --version 2>/dev/null |
      sed -n 's/.* commit \([0-9a-f]\{7,\}\).*/\1/p' | head -n 1 || true)"
  fi
  printf '%s\n' "${commit}"
}

app_dirty() { [[ "$(plist_get "$1" VOBuildDirty)" == "true" ]]; }

short() { printf '%s\n' "${1:0:12}"; }

# Latest local CI row for a commit: "<pass|fail>\t<summary>", or empty.
ci_row() {
  [[ -n "$1" && -f "${ci_tsv}" ]] || return 0
  awk -F'\t' -v sha="$1" '$2 == sha { row = $3 "\t" $4 } END { if (row != "") print row }' "${ci_tsv}"
}

require_ci_pass() {
  local sha="$1" row
  row="$(ci_row "${sha}")"
  if [[ "${row%%$'\t'*}" == "pass" ]]; then
    say "Local CI: pass (${row#*$'\t'})"
    return 0
  fi
  local why="no local CI row"
  [[ -n "${row}" ]] && why="latest local CI row is '${row%%$'\t'*}': ${row#*$'\t'}"
  [[ -f "${ci_tsv}" ]] || why="${ci_tsv} does not exist"
  if [[ "${opt_force}" -eq 1 ]]; then
    warn "$(short "${sha}"): ${why}; continuing because of --force"
  else
    die "$(short "${sha}"): ${why} in ${ci_tsv}; pass --force to override"
  fi
}

pid_alive() { [[ "$1" =~ ^[0-9]+$ ]] && kill -0 "$1" 2>/dev/null; }

# Poll count that spans <seconds> at the poll interval (TALLPINE_RELEASE_POLL, default 0.5s; tests shorten it).
poll_budget() { awk -v s="$1" -v p="${TALLPINE_RELEASE_POLL:-0.5}" 'BEGIN { printf "%d", s / p + 0.999 }'; }

wait_for_exit() {
  local pid="$1" budget="${2:-${stop_timeout}}" waited=0
  while pid_alive "${pid}"; do
    [[ "${waited}" -ge "$(poll_budget "${budget}")" ]] && return 1
    sleep "${TALLPINE_RELEASE_POLL:-0.5}"
    waited=$((waited + 1))
  done
}

# PIDs of the app's main process running from exactly ${app_path}. Matching
# the executable path, not the bundle id, means a test against a temp app dir
# can never reach the app the human is using.
app_pids() {
  local exe_name exe line pid cmd physical
  [[ -d "${app_path}" ]] || return 0
  exe_name="$(plist_get "${app_path}" CFBundleExecutable)"
  [[ -n "${exe_name}" ]] || return 0
  physical="$(cd "${app_path}" && pwd -P)"
  ps -axo pid=,command= | while IFS= read -r line; do
    line="${line#"${line%%[![:space:]]*}"}"
    pid="${line%% *}"
    cmd="${line#* }"
    for exe in "${app_path}/Contents/MacOS/${exe_name}" "${physical}/Contents/MacOS/${exe_name}"; do
      # "<exe> [args]", or "<interpreter> <exe>" for a script executable.
      if [[ "${cmd}" == "${exe}" || "${cmd}" == "${exe} "* || "${cmd}" == *" ${exe}" ]]; then
        printf '%s\n' "${pid}"
        break
      fi
    done
  done
}

# Other running copies: a process of an app bundle with ${bundle_id} that is
# not ${app_path} (a second bundle, a build-tree app). Prints "<pid> <bundle>".
other_app_copies() {
  local line pid cmd bundle physical="" checked=$'\n' ours=$'\n'
  [[ -d "${app_path}" ]] && physical="$(cd "${app_path}" && pwd -P)"
  ps -axo pid=,command= | while IFS= read -r line; do
    [[ "${line}" == *".app/Contents/MacOS/"* ]] || continue
    line="${line#"${line%%[![:space:]]*}"}"
    pid="${line%% *}"
    cmd="${line#* }"
    bundle="${cmd%%.app/Contents/MacOS/*}.app"
    # "<interpreter> <bundle exe>": drop leading words until a bundle remains.
    while [[ ! -d "${bundle}" && "${bundle}" == *" "* ]]; do bundle="${bundle#* }"; done
    [[ -d "${bundle}" ]] || continue
    if [[ "${checked}" != *$'\n'"${bundle}"$'\n'* ]]; then
      checked="${checked}${bundle}"$'\n'
      if [[ "$(plist_get "${bundle}" CFBundleIdentifier)" == "${bundle_id}" &&
        "${bundle}" != "${app_path}" && "$(cd "${bundle}" && pwd -P)" != "${physical}" ]]; then
        ours="${ours}${bundle}"$'\n'
      fi
    fi
    [[ "${ours}" == *$'\n'"${bundle}"$'\n'* ]] && printf '%s %s\n' "${pid}" "${bundle}"
  done
  # A non-matching last line leaves the loop false; that is not a failure.
  return 0
}

# Refuses, before anything is stopped, when another copy of the app runs: it
# would keep its daemon or start the old one against the swapped data, and
# `open` could activate it instead of launching the new bundle.
refuse_other_copies() {
  local copies dev
  copies="$(other_app_copies)"
  if [[ -n "${copies}" ]]; then
    printf 'release-local: another copy of %s is running:\n%s\n' "${product_name}" "${copies}" >&2
    die "quit it first; nothing was changed"
  fi
  dev="$(ps -axo pid=,command= | grep -F '/node_modules/electron/dist/Electron.app/Contents/MacOS/Electron' |
    grep -v -e ' --type=' -e 'grep' | awk '{print $1}' | tr '\n' ' ' || true)"
  [[ -z "${dev// /}" ]] ||
    warn "a development Electron app is running (pid ${dev% }); if it uses ${data_dir}, quit it first"
}

# PIDs that still have the database open.
db_holders() {
  local files=() name
  for name in ao.db ao.db-wal ao.db-shm; do
    [[ -e "${data_dir}/${name}" ]] && files+=("${data_dir}/${name}")
  done
  [[ ${#files[@]} -gt 0 ]] || return 0
  lsof -t -- "${files[@]}" 2>/dev/null | sort -u | tr '\n' ' ' || true
}

# The daemon serves its user-only socket and, by default, no loopback TCP
# (B176): curl goes through the socket running.json names when there is one.
daemon_curl() {
  local sock
  sock="$(json_get "${run_file}" socket 2>/dev/null || true)"
  if [[ -n "${sock}" && -S "${sock}" ]]; then
    curl --unix-socket "${sock}" "$@"
  else
    curl "$@"
  fi
}

probe() { daemon_curl -fsS --max-time 3 "http://127.0.0.1:$1$2" 2>/dev/null; }

# Stops the app running from ${app_path} and then the daemon that serves
# ${data_dir}, and waits until both have exited. Changes nothing on disk.
stop_app_and_daemon() {
  # Quitting the app can tear down the session this runs in. A SIGTERM or hangup in this window
  # must not abort the wait: the app is going away either way and the install has to finish or
  # be undone with the app relaunched. Ctrl-C (a person) still cancels.
  trap '' TERM HUP
  local rc=0
  _stop_app_and_daemon || rc=$?
  trap 'exit 143' TERM
  trap 'exit 129' HUP
  return "${rc}"
}
_stop_app_and_daemon() {
  local pids pid port body lock_pid holders
  pids="$(app_pids)"
  if [[ -n "${pids}" ]]; then
    app_was_running=1
    say "Quitting ${product_name} (pid $(printf '%s' "${pids}" | tr '\n' ' '))..."
    # The app treats SIGTERM like Cmd-Q, without the unsaved-draft dialog: it
    # stops its agents and its daemon.
    for pid in ${pids}; do kill -TERM "${pid}" 2>/dev/null || true; done
    interrupt_point quitting
    for pid in ${pids}; do
      wait_for_exit "${pid}" ||
        die "the app did not quit within ${stop_timeout}s (it may still be stopping agents or closing browser views); quit it from its menu and retry"
    done
  fi

  if [[ -f "${run_file}" ]]; then
    pid="$(json_get "${run_file}" pid)"
    port="$(json_get "${run_file}" port)"
    if pid_alive "${pid}"; then
      body="$(probe "${port}" /healthz || true)"
      if [[ "$(printf '%s' "${body}" | plutil -extract service raw -o - - 2>/dev/null)" != "${service_name}" ||
        "$(printf '%s' "${body}" | plutil -extract pid raw -o - - 2>/dev/null)" != "${pid}" ]]; then
        # The daemon the app just stopped may be alive but no longer answering.
        wait_for_exit "${pid}" 10 ||
          die "pid ${pid} from ${run_file} is alive but is not a responding ${service_name} on port ${port}; stop it with 'tallpine stop' and retry"
      else
        say "Stopping the daemon (pid ${pid}, port ${port})..."
        # A bare /shutdown: a daemon the app did not own (keep-alive or
        # `tallpine start`) leaves its agents running for the next daemon to adopt.
        daemon_curl -fsS --max-time 10 -X POST -H "X-VO-Token: $(cat "${token_file}" 2>/dev/null)" \
          "http://127.0.0.1:${port}/shutdown" >/dev/null ||
          die "the daemon refused /shutdown; stop it with 'tallpine stop' and retry"
        wait_for_exit "${pid}" || die "the daemon (pid ${pid}) did not exit within ${stop_timeout}s"
      fi
    fi
  fi

  # The data-dir lock names the process holding the database open.
  lock_pid="$(tr -dc '0-9' <"${data_dir}/daemon.lock" 2>/dev/null || true)"
  if pid_alive "${lock_pid}"; then
    wait_for_exit "${lock_pid}" || die "pid ${lock_pid} still holds ${data_dir}/daemon.lock; stop it and retry"
  fi
  # Before db_holders: a stray daemon on this data dir holds the database open and would abort the install there.
  sweep_stray_daemons
  holders="$(db_holders)"
  [[ -z "${holders// /}" ]] || die "the database in ${data_dir} is still open by pid ${holders% }; quit it and retry"
}

# drop_agent_session_env unsets the per-session variables of the agent session
# this script may run in (CLAUDE_*, CODEX_*, KIRO_*, DEEPSEEK_*, ...), so the
# app and its daemon never inherit that session's account home or tokens (a
# GH_TOKEN there would pin every project to that session's GitHub account).
# PATH, HOME, LANG and the rest stay.
drop_agent_session_env() {
  local name
  for name in $(compgen -e); do
    case "${name}" in
      CLAUDE_* | CLAUDECODE | ANTHROPIC_* | CODEX_* | KIRO_* | DEEPSEEK_* | OPENCODE_* | GEMINI_* | GH_TOKEN | GITHUB_TOKEN | GH_ENTERPRISE_TOKEN | GITHUB_ENTERPRISE_TOKEN | TALLPINE_GITHUB_TOKEN | TALLPINE_SESSION_* | TALLPINE_PROJECT_ID | TALLPINE_ISSUE_ID | TALLPINE_RUNTIME_LAUNCH_ID | TALLPINE_SUPERVISED_PROCESS | TALLPINE_PERMISSION_MODE | TALLPINE_BROWSER_CAPABILITY | TALLPINE_RUN_FILE | TALLPINE_HOST_SOCK_DIR | TALLPINE_ACP_RUNTIME_DIR) unset "${name}" ;;
    esac
  done
  # A session's scratch temp dir disappears with the session; the user's own
  # temp dir takes its place. A per-project Gradle cache must not become global.
  case "${TMPDIR:-}" in */data/tmp/*)
    user_tmp="$(getconf DARWIN_USER_TEMP_DIR 2>/dev/null || true)"
    if [[ -n "${user_tmp}" ]]; then export TMPDIR="${user_tmp}"; else unset TMPDIR; fi
    unset TMP TEMP ;;
  esac
  case "${GRADLE_USER_HOME:-}" in */data/caches/*) unset GRADLE_USER_HOME ;; esac
}

launch_app() {
  say "Launching ${app_path}..."
  # Restore default signals: the undo ignores them, and the app must not
  # inherit that.
  if [[ -n "${TALLPINE_RELEASE_LAUNCHER:-}" ]]; then
    local launcher="${TALLPINE_RELEASE_LAUNCHER}"
    (trap - INT TERM HUP; drop_agent_session_env; ${launcher} "${app_path}")
  else
    (trap - INT TERM HUP; drop_agent_session_env; open "${app_path}")
  fi
}

# Waits for a new daemon to answer /healthz and /readyz, then compares the
# commit it reports with the one expected (when both are known).
wait_healthy() {
  local expect="$1" old_pid="$2" deadline pid port body running_commit
  deadline=$(($(date +%s) + health_timeout))
  while :; do
    if [[ -f "${run_file}" ]]; then
      pid="$(json_get "${run_file}" pid)"
      port="$(json_get "${run_file}" port)"
      if [[ "${pid}" != "${old_pid}" ]] && pid_alive "${pid}" &&
        body="$(probe "${port}" /healthz)" && probe "${port}" /readyz >/dev/null &&
        [[ "$(printf '%s' "${body}" | plutil -extract service raw -o - - 2>/dev/null)" == "${service_name}" ]]; then
        break
      fi
    fi
    [[ "$(date +%s)" -lt "${deadline}" ]] || {
      printf 'release-local: the daemon did not become healthy within %ss\n' "${health_timeout}" >&2
      return 1
    }
    sleep "${TALLPINE_RELEASE_HEALTH_POLL:-${TALLPINE_RELEASE_POLL:-1}}"
  done
  healthy_pid="${pid}"
  running_commit="$(printf '%s' "${body}" | plutil -extract build.commit raw -o - - 2>/dev/null || true)"
  say "Daemon healthy: pid ${pid}, port ${port}, commit ${running_commit:-unknown}"
  if [[ -n "${expect}" && -n "${running_commit}" && "${running_commit}" != "${expect}" ]]; then
    printf 'release-local: the daemon runs commit %s, expected %s\n' "${running_commit}" "${expect}" >&2
    return 1
  fi
}

current_daemon_pid() { [[ -f "${run_file}" ]] && json_get "${run_file}" pid || true; }

# Refuse to stop the live daemon from inside one of its own agent sessions.
guard_live_target() {
  local live=0
  [[ "${data_dir}" == "${live_state_dir}/data" || "${run_file}" == "${live_state_dir}/running.json" ]] && live=1
  [[ "${app_path}" == "${HOME}/Applications/${product_name}.app" ]] && live=1
  if [[ "${live}" -eq 1 && -n "${TALLPINE_SESSION_ID:-}" && "${opt_allow_in_session}" -eq 0 ]]; then
    die "refusing to restart the live app from inside agent session ${TALLPINE_SESSION_ID}: it would stop the daemon hosting it. Run this from a plain terminal, or pass --allow-in-session"
  fi
}

