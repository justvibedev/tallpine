#!/usr/bin/env bash
# Sourced by scripts/release-local (never run directly): install, rollback, status, migration, snapshots and rollback points.
move_entries() {
  # move_entries <from> <to> [skip-excluded]: moves every entry of <from>.
  local from="$1" to="$2" skip="${3:-}" entry
  [[ -d "${from}" ]] || return 0
  mkdir -p "${to}" || return 1
  for entry in "${from}"/* "${from}"/.[!.]*; do
    [[ -e "${entry}" || -L "${entry}" ]] || continue
    [[ -n "${skip}" ]] && is_excluded "$(basename "${entry}")" && continue
    mv "${entry}" "${to}/" || return 1
  done
}

# Moves every data entry a snapshot would cover into <discard>/data, then
# clones <point>/data back into the data dir.
# Rollback swaps data in steps that on_exit can undo in reverse:
#   1 move the current entries (and app-state.json) into <discard>/data
#   2 move the clones from <discard>/incoming into the data dir
#   3 move the current app into <discard>/app
#   4 clone the point's app into place
stage_incoming() {
  local point="$1" discard="$2" entry
  mkdir -p "${discard}/incoming" || return 1
  for entry in "${point}/data"/* "${point}/data"/.[!.]*; do
    [[ -e "${entry}" || -L "${entry}" ]] || continue
    cp -c -R -p "${entry}" "${discard}/incoming/" || return 1
  done
  if [[ -f "${point}/app-state.json" ]]; then cp -c -p "${point}/app-state.json" "${discard}/incoming-app-state.json" || return 1; fi
}

undo_install_stopped() {
  rm -rf "${cur_point}.partial"
  [[ -n "${cur_point}" && -d "${cur_point}" && ! -d "${cur_point}/app/$(prev_app_name)" ]] && rm -rf "${cur_point}"
  relaunch_if_quit
}

undo_install_swapping() {
  local old="${cur_point}/app/$(prev_app_name)" dest
  dest="${app_dir%/}/$(prev_app_name)"
  if [[ -d "${old}" ]]; then
    # The new app may already be in place; it is still in the build tree.
    [[ -e "${app_path}" ]] && mv "${app_path}" "${staged_cleanup}/new.app"
    interrupt_undo
    # mv onto an existing directory would nest the old app inside it.
    if [[ -e "${dest}" ]] || ! mv "${old}" "${dest}"; then
      warn "could not move ${old} back to ${dest}; restore it by hand"
      return 0
    fi
  fi
  rm -rf "${cur_point}"
  relaunch_if_quit
}

undo_rollback_stopped() {
  [[ -n "${cur_discard}" ]] && rm -rf "${cur_discard}"
  relaunch_if_quit
}

undo_rollback() {
  local ok=1 d="${cur_discard}"
  [[ "${restore_stage}" -ge 4 ]] && rm -rf "${restored_dest:-${app_path}}"
  if [[ "${restore_stage}" -ge 3 && -d "${d}/app/${product_name}.app" ]]; then
    # mv onto an existing directory would nest the app inside it.
    if [[ -e "${app_path}" ]] || ! mv "${d}/app/${product_name}.app" "${app_path}"; then ok=0; fi
  fi
  interrupt_undo
  if [[ "${restore_stage}" -ge 2 ]]; then
    move_entries "${data_dir}" "${d}/incoming" skip-excluded || ok=0
    rm -f "${state_dir}/app-state.json"
  fi
  interrupt_undo
  move_entries "${d}/data" "${data_dir}" || ok=0
  if [[ -f "${d}/app-state.json" ]]; then mv "${d}/app-state.json" "${state_dir}/app-state.json" || ok=0; fi
  if [[ "${ok}" -eq 1 ]]; then
    rm -rf "${d}"
    relaunch_if_quit
  else
    warn "could not fully undo the rollback; the replaced app and data are in ${d}, restore them by hand"
  fi
}

# Rollback points, oldest first. Each is <stamp>-<commit>/ holding app/ (the
# app that was installed before) and data/ (its data, snapshotted while it was
# stopped).
list_points() {
  local dir
  [[ -d "${releases_dir}" ]] || return 0
  for dir in "${releases_dir}"/2*; do
    [[ "${dir}" != *.partial && -d "${dir}/data" ]] && printf '%s\n' "${dir}"
  done
  return 0
}

prune_dir_entries() {
  # $1: newline-separated paths, oldest first; keeps the newest ${keep}.
  local paths="$1" count remove path
  count="$(printf '%s' "${paths}" | grep -c . || true)"
  [[ "${count}" -gt "${keep}" ]] || return 0
  remove=$((count - keep))
  printf '%s\n' "${paths}" | head -n "${remove}" | while IFS= read -r path; do
    [[ -n "${path}" ]] || continue
    say "Pruning ${path}"
    rm -rf "${path}"
  done
}

prune_releases() {
  prune_dir_entries "$(list_points)"
  [[ -d "${rolled_back_dir}" ]] && prune_dir_entries "$(ls -1d "${rolled_back_dir}"/2* 2>/dev/null || true)"
  return 0
}

# A path that does not exist yet: <base>, else <base>-2, <base>-3, ...
unique_path() {
  local base="$1" n=2 path="$1"
  while [[ -e "${path}" || -e "${path}.partial" ]]; do path="${base}-${n}"; n=$((n + 1)); done
  printf '%s\n' "${path}"
}

meta_get() { [[ -f "$1/meta" ]] && sed -n "s/^$2=//p" "$1/meta" | head -n 1; return 0; }

# ------------------------------------------------------------------ install

# ----------------------------------------------------------------- migration

# repoint_state <from> <to>: after the state tree moved, rewrites every path
# variable that pointed into it (the caller's locals too: bash scopes them dynamically).
repoint_state() {
  local from="$1" to="$2" v
  for v in state_dir data_dir run_file token_file releases_dir rolled_back_dir builds_dir lock_dir \
    staged_cleanup point discard prev_app cur_point cur_discard; do
    [[ -n "${!v:-}" ]] || continue
    printf -v "${v}" '%s' "${!v/#${from}/${to}}"
  done
}

# The new app's own CLI performs the migration and its reversal.
new_cli() { printf '%s\n' "${app_path}/Contents/Resources/daemon/${cli_name}"; }

# Kills the legacy tmux server (its socket name is fixed): the migrator refuses
# while it answers, and every agent already stopped with the app.
kill_legacy_tmux() { tmux -L "${legacy_tmux_socket}" kill-server 2>/dev/null || true; }

# Step 0-8 of the migration, run after the snapshot with the app, daemon and
# tmux stopped and the new app in place; the migrator itself checks all that.
run_migration() {
  local cli
  cli="$(new_cli)"
  [[ -x "${cli}" ]] || die "the new app has no ${cli_name} CLI at ${cli}; cannot migrate ${legacy_state_dir}"
  say "Migrating ${legacy_state_dir} to ${live_state_dir} (rollback point ${point})..."
  phase=install-migrating
  "${cli}" migrate-from-vo || die "the migration failed; the old app and data are restored"
  repoint_state "${legacy_state_dir}" "${live_state_dir}"
  sed -i '' "s|^data_dir=.*|data_dir=${data_dir}|" "${point}/meta"
  phase=""
}

# Reverses run_migration for a rollback point taken with it: the new tree goes
# back to the legacy home (worktrees repaired to their old paths); the caller
# then restores the snapshot's data over it.
rollback_migration() {
  local from="$1" cli
  cli="$(new_cli)"
  [[ -x "${cli}" ]] || die "no ${cli_name} CLI at ${cli} to move ${live_state_dir} back to ${from}"
  say "Moving ${live_state_dir} back to ${from}..."
  "${cli}" migrate-from-vo --rollback || die "could not move ${live_state_dir} back to ${from}; nothing else was changed"
  repoint_state "${live_state_dir}" "${from}"
  phase=rollback-migrated
}

undo_install_migrating() {
  local cli f
  cli="$(new_cli)"
  if [[ -e "${live_state_dir}" ]]; then
    "${cli}" migrate-from-vo --rollback ||
      { warn "could not move ${live_state_dir} back to ${legacy_state_dir}; its snapshot is ${cur_point}/data"; return 0; }
  fi
  # The DB rewrite may have run: put the snapshot's DB files back (WAL files of a
  # newer run must not outlive their database).
  rm -f "${data_dir}/ao.db-wal" "${data_dir}/ao.db-shm"
  for f in "${cur_point}"/data/ao.db "${cur_point}"/data/ao.db-wal; do
    [[ -f "${f}" ]] && cp -c -p "${f}" "${data_dir}/"
  done
  undo_install_swapping
}

cmd_install() {
  local src="${1:-}" src_physical prev_commit stamp point staged old_pid
  # Armed first: any failure from here on (even before anything stops) tells --notify sessions.
  notify_armed=1
  new_commit=""
  trap on_exit EXIT
  [[ -n "${src}" ]] || die "usage: release-local install <path to ${product_name}.app>"
  guard_live_target
  detach_run
  [[ -f "${src}/Contents/Info.plist" ]] || die "${src} is not an app bundle"
  src_physical="$(cd "${src}" && pwd -P)"
  [[ "$(plist_get "${src}" CFBundleIdentifier)" == "${bundle_id}" ]] ||
    die "${src} has bundle id '$(plist_get "${src}" CFBundleIdentifier)', expected ${bundle_id}"
  if [[ -d "${app_path}" && "$(cd "${app_path}" && pwd -P)" == "${src_physical}" ]]; then
    die "${src} is the installed app"
  fi
  new_commit="$(app_commit "${src}")"
  if [[ -z "${new_commit}" ]]; then
    [[ "${opt_force}" -eq 1 ]] || die "${src} carries no build commit; build it with release-local build, or pass --force"
    warn "installing an app without a build commit"
  else
    if app_dirty "${src}"; then
      [[ "${opt_force}" -eq 1 ]] || die "${src} was built from a dirty tree, which its commit's CI row does not cover; pass --force to install it anyway"
      warn "${src} was built from a dirty tree"
    fi
    require_ci_pass "${new_commit}"
  fi
  local sig=0
  verify_signature "${src}" || sig=$?
  if [[ "${sig}" -eq 1 ]]; then
    [[ "${opt_force}" -eq 1 ]] || die "${src} has an invalid code signature (codesign --verify --deep --strict failed); rebuild it, or pass --force"
    warn "${src} has an invalid code signature"
  elif [[ "${sig}" -eq 2 ]]; then
    warn "${src} is signed ad hoc: macOS will ask again for Documents and other privacy access after this install"
  fi
  [[ "${migrate_pending}" -eq 1 ]] && prev_is_legacy=1
  local prev_path
  prev_path="${app_dir%/}/$(prev_app_name)"
  as_prev refuse_other_copies
  refuse_other_copies
  lock_releases
  preflight_clone

  prev_commit=""
  [[ -d "${prev_path}" ]] && prev_commit="$(app_commit "${prev_path}")"
  confirm "Installing $(short "${new_commit:-unknown}") over $(short "${prev_commit:-nothing}") at ${app_path}.
This quits ${product_name}: every running agent (orchestrator and workers) stops and resumes on its next message, and unsent chat drafts are discarded."

  # Stage a copy before stopping anything, so a failed copy changes nothing.
  stamp="$(date -u +%Y%m%dT%H%M%SZ)"
  staged="${releases_dir}/.incoming-${stamp}"
  staged_cleanup="${staged}"
  rm -rf "${staged}"
  mkdir -p "${staged}"
  say "Staging ${src}..."
  ditto "${src}" "${staged}/${product_name}.app" || die "could not copy ${src}; nothing was changed"

  point="$(unique_path "${releases_dir}/${stamp}-$(short "${prev_commit:-unknown}")")"
  cur_point="${point}"
  old_pid="$(current_daemon_pid)"; stopped_daemon_pid="${old_pid}"

  # From here on, any failure or Ctrl-C is undone by on_exit.
  phase=install-stopped
  as_prev stop_app_and_daemon
  [[ "${migrate_pending}" -eq 1 ]] && kill_legacy_tmux
  say "Snapshotting ${data_dir} into ${point}..."
  snapshot_data "${point}.partial" || die "the data snapshot failed; nothing was changed"
  interrupt_point snapshot
  {
    printf 'created=%s\n' "${stamp}"
    printf 'commit=%s\n' "${prev_commit}"
    printf 'replaced_by=%s\n' "${new_commit}"
    printf 'data_dir=%s\n' "${data_dir}"
    printf 'app_path=%s\n' "${app_path}"
    printf 'prev_app=%s\n' "$(prev_app_name)"
    [[ "${migrate_pending}" -eq 1 ]] && printf 'migrated_from=%s\n' "${legacy_state_dir}"
    true
  } >"${point}.partial/meta"
  mkdir -p "${point}.partial/app" "${app_dir}"
  mv "${point}.partial" "${point}"

  if [[ -d "${prev_path}" ]]; then
    phase=install-swapping
    mv "${prev_path}" "${point}/app/" || die "could not move the current app aside"
    interrupt_point app-moved
  else
    warn "no app was installed at ${prev_path}; this rollback point holds data only"
  fi
  [[ ! -e "${prev_path}" && ! -e "${app_path}" ]] || die "${prev_path} or ${app_path} still exists after moving it aside"
  mv "${staged}/${product_name}.app" "${app_path}" || die "could not move the new app into place"
  phase=""

  [[ "${migrate_pending}" -eq 1 ]] && run_migration
  launch_app
  if ! wait_healthy "${new_commit}" "${old_pid}"; then
    die "the new release did not come up healthy; roll back with: scripts/release-local rollback$(path_flags)"
  fi
  report_other_daemons "${healthy_pid}"
  verify_single_daemon "${healthy_pid}" || die "the daemon check failed (see above); investigate before the next install"
  prune_releases
  say "Installed $(short "${new_commit:-unknown}") at ${app_path}; rollback point ${point}"
  notify_armed=0
  write_result ok "[release] installed $(sha10); daemon healthy (pid ${healthy_pid}); rollback point ${point}; log $(notify_log_path)"
}

# Re-print the path overrides in hints so they target the same install.
path_flags() {
  [[ -n "${opt_state_dir}" ]] && printf ' --state-dir %q' "${state_dir}"
  [[ -n "${opt_data_dir}" ]] && printf ' --data-dir %q' "${data_dir}"
  [[ -n "${opt_app_dir}" ]] && printf ' --app-dir %q' "${app_dir}"
  return 0
}

# ----------------------------------------------------------------- rollback

cmd_rollback() {
  local migrated_from point candidate prev_app prev_dest target_commit current_commit stamp discard old_pid recorded created age skipped
  # Armed first, like install: any failure tells --notify sessions.
  notify_armed=1 notify_what=rollback
  trap on_exit EXIT
  guard_live_target
  detach_run
  # The newest point that holds an app; a data-only point (from a first
  # install onto an empty app dir) can not be rolled back to.
  point=""
  skipped=""
  while IFS= read -r candidate; do
    [[ -n "${candidate}" ]] || continue
    if [[ -d "${candidate}/app/$(point_app_name "${candidate}")" ]]; then
      point="${candidate}"
      skipped=""
    else
      skipped="${skipped} $(basename "${candidate}")"
    fi
  done < <(list_points) # not a heredoc: see require_hardened_runtime (B23)
  [[ -n "${point}" ]] || die "no rollback point with an app under ${releases_dir}"
  [[ -z "${skipped}" ]] ||
    warn "skipping newer data-only point(s)${skipped}: they hold no app; rolling back to the older $(basename "${point}") and its data"
  prev_app="${point}/app/$(point_app_name "${point}")"
  prev_dest="${app_dir%/}/$(point_app_name "${point}")"
  [[ "$(point_app_name "${point}")" == "${legacy_product_name}.app" ]] && prev_is_legacy=1
  recorded="$(meta_get "${point}" data_dir)"
  [[ -z "${recorded}" || "${recorded}" == "${data_dir}" ]] ||
    die "${point} was taken from ${recorded}, not ${data_dir}; pass --data-dir ${recorded}, or restore it by hand"
  recorded="$(meta_get "${point}" app_path)"
  [[ -z "${recorded}" || "${recorded}" == "${app_path}" ]] ||
    die "${point} replaced ${recorded}, not ${app_path}; pass --app-dir $(dirname "${recorded}"), or restore it by hand"
  target_commit="$(meta_get "${point}" commit)"
  [[ -n "${target_commit}" ]] || target_commit="$(app_commit "${prev_app}")"
  refuse_other_copies
  lock_releases
  preflight_clone

  current_commit=""
  [[ -d "${app_path}" ]] && current_commit="$(app_commit "${app_path}")"
  created="$(meta_get "${point}" created)"
  age=""
  if [[ -n "${created}" ]] && created="$(date -j -u -f %Y%m%dT%H%M%SZ "${created}" +%s 2>/dev/null)"; then
    age=", taken $((($(date +%s) - created) / 3600))h ago"
  fi
  stamp="$(date -u +%Y%m%dT%H%M%SZ)"
  discard="$(unique_path "${rolled_back_dir}/${stamp}-$(short "${current_commit:-unknown}")")"
  confirm "Rolling back from $(short "${current_commit:-unknown}") to $(short "${target_commit:-unknown}") using $(basename "${point}") (created $(meta_get "${point}" created)${age}).
Data written since then (sessions, chats, reports) leaves the app and is kept in ${discard}; worktrees stay.
This quits ${product_name}: every running agent (orchestrator and workers) stops and resumes on its next message, and unsent chat drafts are discarded."
  old_pid="$(current_daemon_pid)"; stopped_daemon_pid="${old_pid}"

  # From here on, any failure or Ctrl-C is undone by on_exit.
  cur_discard="${discard}"
  phase=rollback-stopped
  stop_app_and_daemon
  migrated_from="$(meta_get "${point}" migrated_from)"
  if [[ -n "${migrated_from}" && "${state_dir}" == "${live_state_dir}" ]]; then
    kill_legacy_tmux
    rollback_migration "${migrated_from}"
  fi
  say "Restoring data; the replaced data is kept in ${discard}"
  stage_incoming "${point}" "${discard}" || die "could not clone ${point}/data; nothing was changed"
  phase=rollback-restoring
  restore_stage=1
  if [[ -f "${state_dir}/app-state.json" ]]; then
    mkdir -p "${discard}" && mv "${state_dir}/app-state.json" "${discard}/app-state.json" || die "could not move app-state.json aside"
  fi
  move_entries "${data_dir}" "${discard}/data" skip-excluded || die "could not move the current data aside"
  interrupt_point data-moved
  restore_stage=2
  move_entries "${discard}/incoming" "${data_dir}" || die "could not move the restored data into place"
  if [[ -f "${discard}/incoming-app-state.json" ]]; then
    mv "${discard}/incoming-app-state.json" "${state_dir}/app-state.json" || die "could not restore app-state.json"
  fi
  interrupt_point data-restored
  restore_stage=3
  mkdir -p "${discard}/app"
  if [[ -d "${app_path}" ]]; then
    mv "${app_path}" "${discard}/app/" || die "could not move ${app_path} aside"
  fi
  interrupt_point app-moved
  restore_stage=4
  # Clone, not move: the rollback point stays intact until the old app is up.
  [[ ! -e "${app_path}" && ! -e "${prev_dest}" ]] || die "${app_path} or ${prev_dest} still exists after moving it aside"
  restored_dest="${prev_dest}"
  cp -c -R -p "${prev_app}" "${prev_dest}" || die "could not restore ${prev_app} to ${prev_dest}"
  phase=""
  rmdir "${discard}/incoming" 2>/dev/null || true
  {
    printf 'created=%s\n' "${stamp}"
    printf 'commit=%s\n' "${current_commit}"
    printf 'restored_point=%s\n' "$(basename "${point}")"
  } >"${discard}/meta"

  as_prev launch_app
  if ! as_prev wait_healthy "${target_commit}" "${old_pid}"; then
    die "the restored release did not come up healthy; the rollback point is still at ${point}, and the replaced release is in ${discard}"
  fi
  # The point is now the running state; keep it with the rolled-back release.
  mv "${point}" "${discard}/restored-point"
  prune_releases
  say "Rolled back to $(short "${target_commit:-unknown}"). The replaced app and data are in ${discard}"
  notify_armed=0
  write_result ok "[release] rolled back to $(short "${target_commit:-unknown}"); log $(notify_log_path)"
}

# ------------------------------------------------------------------- status

cmd_status() {
  local installed running_body running_commit pid port main_sha row point commit
  say "App:       ${app_path}"
  if [[ -d "${app_path}" ]]; then
    installed="$(app_commit "${app_path}")"
    say "Installed: ${installed:-unknown (built before build stamping)}$(app_dirty "${app_path}" && printf ' (dirty)')"
    say "Version:   $(plist_get "${app_path}" CFBundleShortVersionString), built $(plist_get "${app_path}" VOBuildDate)"
  else
    installed=""
    say "Installed: none"
  fi

  pid="$(current_daemon_pid)"
  if pid_alive "${pid}"; then
    port="$(json_get "${run_file}" port)"
    running_body="$(probe "${port}" /healthz || true)"
    running_commit="$(printf '%s' "${running_body}" | plutil -extract build.commit raw -o - - 2>/dev/null || true)"
    say "Daemon:    pid ${pid}, port ${port}, commit ${running_commit:-unknown}"
  else
    say "Daemon:    not running"
  fi

  if [[ "${opt_fetch}" -eq 1 ]]; then git -C "${repo_root}" fetch --quiet origin 2>/dev/null || warn "fetch failed"; fi
  main_sha="$(git -C "${repo_root}" rev-parse --verify --quiet origin/main || true)"
  say "origin/main: ${main_sha:-unknown}"
  if [[ -n "${installed}" && -n "${main_sha}" ]] && git -C "${repo_root}" cat-file -e "${installed}^{commit}" 2>/dev/null; then
    say "Behind:    $(git -C "${repo_root}" rev-list --count "${installed}..${main_sha}") commits"
  fi
  row="$(ci_row "${installed}")"
  say "CI (installed):   ${row:-no row}"
  row="$(ci_row "${main_sha}")"
  say "CI (origin/main): ${row:-no row}"

  say "Rollback points (newest last):"
  local any=0
  while IFS= read -r point; do
    [[ -n "${point}" ]] || continue
    any=1
    commit="$(meta_get "${point}" commit)"
    if [[ -d "${point}/app/$(point_app_name "${point}")" ]]; then
      say "  $(basename "${point}")  app ${commit:-unknown}"
    else
      say "  $(basename "${point}")  data only"
    fi
  done < <(list_points)
  [[ "${any}" -eq 1 ]] || say "  none"
}

