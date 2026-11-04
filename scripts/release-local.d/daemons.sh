#!/usr/bin/env bash
# Sourced by scripts/release-local (never run directly): finds `tallpine daemon` processes of the data dir being
# installed (B469). A daemon chdirs into its data dir at startup (stabilizeWorkingDirectory), so its cwd names
# the data dir it serves. Scratch daemons on other data dirs and anything under ~/.tallpine/local-ci are never selected.

# daemon_select <data_dir> [keep_pid]: reads "pid<TAB>cwd<TAB>command" lines on stdin and prints the pids of
# `<...>/${cli_name} daemon` processes whose cwd is <data_dir>, except keep_pid and anything under local-ci.
daemon_select() {
  local dir="$1" keep="${2:-}" pid cwd cmd
  while IFS=$'\t' read -r pid cwd cmd; do
    [[ -n "${pid}" && "${pid}" != "${keep}" ]] || continue
    if [[ -n "${TALLPINE_RELEASE_DAEMON_MATCH:-}" ]]; then
      # Test hook: an ERE for the fake daemon's command line, in place of `<...>/tallpine daemon`.
      [[ "${cmd}" =~ ${TALLPINE_RELEASE_DAEMON_MATCH} ]] || continue
    else
      # Match the tail: the executable path may contain spaces, so the command cannot be split at the first one.
      [[ "${cmd}" == */"${cli_name}"\ daemon || "${cmd}" == */"${cli_name}"\ daemon\ * ]] || continue
    fi
    [[ "${cwd}" == */.tallpine/local-ci || "${cwd}" == */.tallpine/local-ci/* ]] && continue
    # An empty <data_dir> selects every data dir (for reporting only).
    [[ -z "${dir}" || "${cwd%/}" == "${dir%/}" ]] || continue
    printf '%s\n' "${pid}"
  done
}

# daemon_table: "pid<TAB>cwd<TAB>command" for every process whose command looks like a daemon.
daemon_table() {
  local line pid cmd cwd
  ps -axo pid=,command= | grep -E " daemon|${TALLPINE_RELEASE_DAEMON_MATCH:-^\$}" | while IFS= read -r line; do
    line="${line#"${line%%[![:space:]]*}"}"
    pid="${line%% *}"
    cmd="${line#* }"
    cwd="$(lsof -a -p "${pid}" -d cwd -Fn 2>/dev/null | sed -n 's/^n//p' | head -1)"
    [[ -n "${cwd}" ]] && printf '%s\t%s\t%s\n' "${pid}" "${cwd}" "${cmd}"
  done
}

# data_dir_daemons [keep_pid]: pids of daemons serving ${data_dir} (physical path, so symlinks match).
data_dir_daemons() {
  local physical
  physical="$(cd "${data_dir}" 2>/dev/null && pwd -P)" || physical="${data_dir}"
  daemon_table | daemon_select "${physical}" "${1:-}"
}

# sweep_stray_daemons: after the stop step, SIGTERMs daemons still serving ${data_dir}. Each pid is logged.
sweep_stray_daemons() {
  local pid
  for pid in $(data_dir_daemons); do
    warn "daemon pid ${pid} still serves ${data_dir} after the stop step (it held neither ${run_file} nor daemon.lock); sending SIGTERM"
    kill -TERM "${pid}" 2>/dev/null || true
    wait_for_exit "${pid}" || die "stray daemon pid ${pid} for ${data_dir} did not exit within ${stop_timeout}s; stop it and retry"
  done
}

# report_other_daemons <pid>: warns about every other daemon of this user (not under local-ci) with its cwd, so a
# stray on a different data dir is visible. Reports only; scratch daemons are legitimate.
report_other_daemons() {
  local table pid cwd
  table="$(daemon_table || true)"
  for pid in $(printf '%s\n' "${table}" | daemon_select "" "$1"); do
    cwd="$(printf '%s\n' "${table}" | awk -F'\t' -v p="${pid}" '$1 == p {print $2}')"
    warn "another daemon is running: pid ${pid}, cwd ${cwd:-unknown} (not ${data_dir}; left alone)"
  done
}

# verify_single_daemon <pid>: exactly one daemon serves ${data_dir} and it is the one in ${run_file}. Never kills.
verify_single_daemon() {
  local pids n
  pids="$(data_dir_daemons | tr '\n' ' ')"
  n="$(printf '%s' "${pids}" | wc -w | tr -d ' ')"
  if [[ "${n}" != 1 || "${pids% }" != "$1" ]]; then
    printf 'release-local: expected exactly one daemon for %s (pid %s from %s); found %s: %s\n' \
      "${data_dir}" "$1" "${run_file}" "${n}" "${pids:-none}" >&2
    return 1
  fi
}
