#!/usr/bin/env bash
# Sourced by scripts/release-local.test.sh (shares its state and helpers): B469: which daemons the installer sweeps and verifies.
(
  # shellcheck source=/dev/null
  . "${script_dir}/release-local.d/daemons.sh"
  cli_name=tallpine; unset TALLPINE_RELEASE_DAEMON_MATCH
  dd=/h/.tallpine/data
  tab=$'\t'
  table="$(printf '%s\n' \
    "100${tab}${dd}${tab}/Applications/Tallpine.app/Contents/Resources/daemon/tallpine daemon" \
    "101${tab}${dd}${tab}/old/Tallpine.app/Contents/Resources/daemon/tallpine daemon --flag" \
    "102${tab}/tmp/scratch/data${tab}/Applications/Tallpine.app/Contents/Resources/daemon/tallpine daemon" \
    "103${tab}/h/.tallpine/local-ci/data${tab}/x/tallpine daemon" \
    "104${tab}/h/.tallpine/local-ci${tab}/x/tallpine daemon" \
    "105${tab}${dd}${tab}/x/tallpine start" \
    "106${tab}${dd}${tab}/x/other daemon" \
    "107${tab}${dd}${tab}/x/tallpine daemonize" \
    "109${tab}${dd}${tab}/Users/x/My Apps/Tallpine.app/Contents/Resources/daemon/tallpine daemon" \
    "108${tab}${dd}/sub${tab}/x/tallpine daemon")"
  got="$(printf '%s\n' "${table}" | daemon_select "${dd}" | tr '\n' ' ')"
  [[ "${got}" == "100 101 109 " ]] || { echo "select got '${got}'"; exit 1; }
  got="$(printf '%s\n' "${table}" | daemon_select "${dd}" 100 | tr '\n' ' ')"
  [[ "${got}" == "101 109 " ]] || { echo "keep got '${got}'"; exit 1; }
  # A data dir that itself lives under local-ci is never swept.
  got="$(printf '%s\n' "${table}" | daemon_select /h/.tallpine/local-ci/data | tr '\n' ' ')"
  [[ -z "${got}" ]] || { echo "local-ci got '${got}'"; exit 1; }
  # report scope: every other non-local-ci daemon regardless of data dir.
  got="$(printf '%s\n' "${table}" | daemon_select "" 100 | tr '\n' ' ')"
  [[ "${got}" == "101 102 109 108 " ]] || { echo "report got '${got}'"; exit 1; }
  # verify_single_daemon: one matching the run file passes; none, two, or a different pid fail without killing.
  data_dir=x run_file=r
  data_dir_daemons() { printf '%s' "${FAKE_PIDS}" | tr ' ' '\n' | sed '/^$/d'; }
  FAKE_PIDS="100" verify_single_daemon 100 2>/dev/null || { echo "single should pass"; exit 1; }
  FAKE_PIDS="" verify_single_daemon 100 2>/dev/null && { echo "none should fail"; exit 1; }
  FAKE_PIDS="100 101" verify_single_daemon 100 2>/dev/null && { echo "two should fail"; exit 1; }
  FAKE_PIDS="101" verify_single_daemon 100 2>/dev/null && { echo "mismatch should fail"; exit 1; }
  exit 0
) >"${T}/b469.out" 2>&1 && ok "b469: daemon sweep selects only this data dir, skips local-ci/scratch; verify needs exactly one" || { bad "b469: $(cat "${T}/b469.out")"; }

# B469 review: a stray holding ao.db open on the data dir must be swept by the stop step (before db_holders aborts).
(
  d="${T}/b469data"; mkdir -p "${d}"
  python3 -c 'import os,sys,time; os.chdir(sys.argv[1]); f=open("ao.db","a"); time.sleep(60)' "${d}" B469STRAY &
  sp=$!
  sleep 1
  # shellcheck source=/dev/null
  . "${script_dir}/release-local.d/common.sh"; . "${script_dir}/release-local.d/daemons.sh"
  export TALLPINE_RELEASE_DAEMON_MATCH=B469STRAY
  app_path=/nonexistent run_file="${d}/none.json" data_dir="${d}" stop_timeout=5 cli_name=tallpine
  say() { :; }; warn() { :; }; die() { echo "die: $*"; kill "${sp}" 2>/dev/null; exit 1; }
  _stop_app_and_daemon || { kill "${sp}" 2>/dev/null; exit 1; }
  kill -0 "${sp}" 2>/dev/null && { kill "${sp}"; echo "stray survived"; exit 1; }
  exit 0
) >"${T}/b469b.out" 2>&1 && ok "b469: a stray holding ao.db is swept before the db_holders check" || bad "b469 sweep: $(cat "${T}/b469b.out")"
