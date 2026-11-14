#!/usr/bin/env bash
# Sourced by scripts/release-local (never run directly): the APFS clone preflight and the data snapshot taken while stopped.
# Fails early, before anything is stopped, when APFS cloning does not work
# between the data dir and the releases dir.
preflight_clone() {
  local probe_src="${releases_dir}/.clone-probe-src" probe_dst="${releases_dir}/.clone-probe"
  if [[ -f "${data_dir}/ao.db" ]]; then probe_src="${data_dir}/ao.db"; else printf 'probe\n' >"${probe_src}"; fi
  if ! cp -c "${probe_src}" "${probe_dst}" 2>/dev/null; then
    rm -f "${probe_dst}" "${releases_dir}/.clone-probe-src"
    die "cp -c (APFS clone) from ${data_dir} to ${releases_dir} failed; both must be on the same APFS volume; nothing was changed"
  fi
  rm -f "${probe_dst}" "${releases_dir}/.clone-probe-src"
}

is_excluded() {
  [[ "${snapshot_excludes}" == *" $1 "* || "$1" == *.log ]]
}

# db_stamp: inode, size and mtime of the database files a snapshot depends on (not -shm, which a mere reader
# touches). The same stamp before and after the clone proves nothing wrote the files in between.
db_stamp() {
  local name
  for name in ao.db ao.db-wal; do
    [[ -e "${data_dir}/${name}" ]] && stat -f '%N %i %z %Fm' "${data_dir}/${name}" 2>/dev/null
  done
  return 0
}

# Clones the stopped data dir (minus the excludes) and app-state.json into
# <dest>/data and <dest>/app-state.json; on_exit removes a partial one.
# Returns 2 when the database changed or was held open during the clone (a relaunched app), 1 on any other failure.
# The clone is instant, so the check that it is consistent must be too (B585): it used to cmp the multi-GB
# ao.db against its clone, which left a ~10s window for a relaunched Tallpine to write the database.
snapshot_data() {
  local dest="$1" entry name before after holders
  mkdir -p "${dest}/data" || return 1
  if [[ -d "${data_dir}" ]]; then
    before="$(db_stamp)"
    for entry in "${data_dir}"/* "${data_dir}"/.[!.]*; do
      [[ -e "${entry}" || -L "${entry}" ]] || continue
      name="$(basename "${entry}")"
      is_excluded "${name}" && continue
      cp -c -R -p "${entry}" "${dest}/data/" || return 1
    done
    [[ -n "${TALLPINE_RELEASE_TEST_DURING_SNAPSHOT:-}" ]] && eval "${TALLPINE_RELEASE_TEST_DURING_SNAPSHOT}"
    after="$(db_stamp)"
    holders="$(db_holders)"
    if [[ "${before}" != "${after}" || -n "${holders// /}" ]]; then
      printf 'the database in %s changed while it was being snapshotted%s; something (a relaunched %s or a stray process) is using it. Do not open %s during an install\n' \
        "${data_dir}" "${holders:+ (open by pid ${holders% })}" "${product_name}" "${product_name}" >&2
      describe_holders "${holders:-$(app_pids | tr '\n' ' ')}" >&2
      return 2
    fi
  fi
  if [[ -f "${state_dir}/app-state.json" ]]; then
    cp -c -p "${state_dir}/app-state.json" "${dest}/app-state.json" || return 1
  fi
}

# B695: macOS starts the app again whenever someone clicks its Dock icon or picks it in Spotlight, even while an
# install has it stopped (launchd is then its parent; nothing in the app relaunches itself). A snapshot that a
# relaunch spoiled is discarded, the app and its daemon are quit again and the snapshot is retaken, a few times.
snapshot_with_retry() {
  local dest="$1" attempts="${TALLPINE_RELEASE_SNAPSHOT_ATTEMPTS:-3}" attempt=1 rc
  while :; do
    say "Snapshotting ${data_dir} into ${dest%.partial}..."
    rc=0
    snapshot_data "${dest}" || rc=$?
    [[ "${rc}" -eq 0 ]] && return 0
    rm -rf "${dest}"
    if [[ "${rc}" -ne 2 || "${attempt}" -ge "${attempts}" ]]; then
      [[ "${rc}" -eq 2 ]] && printf '%s kept being started again (%s attempts). Quit it, do not reopen it until the install finishes (no Dock, Spotlight or Finder launch), and retry; if it keeps coming back by itself, check System Settings > General > Login Items and "Close windows when quitting an application".\n' "${product_name}" "${attempts}" >&2
      return 1
    fi
    attempt=$((attempt + 1))
    say "${product_name} was started again during the install (a Dock click, Spotlight, a login item...); quitting it and retaking the snapshot (attempt ${attempt} of ${attempts})"
    as_prev stop_app_and_daemon
  done
}
