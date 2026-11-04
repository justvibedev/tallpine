#!/usr/bin/env bash
# Sourced by scripts/release-local.test.sh (shares its state and helpers): first install after the rename: legacy-home migration.
# ------------------------------------------------- migration (first install after the rename)
# While the migration is pending, every subcommand (build included) uses the legacy home,
# even when the app already created a DB-less ~/.tallpine; nothing is created in the new home.
ph="${T}/pendhome"; mkdir -p "${ph}/${lhome}/data" "${ph}/.tallpine/electron"; : >"${ph}/${lhome}/data/ao.db"
HOME="${ph}" "${release}" paths >"${T}/paths" 2>&1
check "pending migration: build paths use the legacy home" \
  'grep -q "^builds_dir=${ph}/${lhome}/release-builds\$" "${T}/paths" && grep -q "^build_cache=${ph}/${lhome}/build-cache\$" "${T}/paths"'
check "pending migration: no build dirs created in the new home" '[[ "$(ls -A "${ph}/.tallpine")" == electron ]]'
: >"${ph}/.tallpine/data.tmp"; mkdir -p "${ph}/.tallpine/data"; : >"${ph}/.tallpine/data/ao.db"
HOME="${ph}" "${release}" paths >"${T}/paths" 2>&1
check "migrated: paths use the new home" 'grep -q "^state_dir=${ph}/.tallpine\$" "${T}/paths"'

# A scratch HOME only; the new app's CLI is a stub that renames the tree like the real
# migrator (the real one is covered by the Go tests and a separate rehearsal).
# Stop the main section's app first: release-local refuses a second running copy.
pkill -f "${apps}/${product}.app" 2>/dev/null || true
for _ in $(seq 1 50); do pgrep -f "${apps}/${product}.app" >/dev/null || break; sleep 0.1; done
mh="${T}/mighome"
mapps="${mh}/apps"
mrl() { HOME="${mh}" "${release}" --app-dir "${mapps}" --no-fetch --yes "$@"; }
mdb() { tr '\n' ' ' <"$1/data/ao.db" | sed 's/ $//'; }
mkdir -p "${mh}/${lhome}/data/worktrees/w1" "${mapps}"
echo "${A}" >"${mh}/${lhome}/data/ao.db"; echo keep >"${mh}/${lhome}/data/worktrees/w1/file"
echo '{"schemaVersion":2}' >"${mh}/${lhome}/app-state.json"; echo tok >"${mh}/${lhome}/daemon.token"
printf 'main\t%s\tpass\tgreen\n' "${A}" >>"${TALLPINE_RELEASE_CI_TSV}"
make_app "${T}/mig-a" "${A}" "${mh}/${lhome}" "${lproduct}" "${lservice}"
/usr/libexec/PlistBuddy -c "Set :CFBundleIdentifier ee.justdev.tallpine-release-test-legacy.$$" "${T}/mig-a/${lproduct}.app/Contents/Info.plist"
export TALLPINE_RELEASE_LEGACY_BUNDLE_ID=ee.justdev.tallpine-release-test-legacy.$$
make_app "${T}/mig-b" "${B}" "${mh}/.tallpine"
mkdir -p "${T}/mig-b/${product}.app/Contents/Resources/daemon"
cat >"${T}/mig-b/${product}.app/Contents/Resources/daemon/tallpine" <<SH
#!/bin/bash
h="${mh}"
[[ -e "\$h/fail-migrate" && "\$2" != --rollback ]] && exit 1
if [[ "\$2" == --rollback ]]; then
  rm "\$h/${lhome}/README" && rmdir "\$h/${lhome}" && mv "\$h/.tallpine" "\$h/${lhome}"; echo rollback >>"\$h/cli.log"; exit
fi
mv "\$h/${lhome}" "\$h/.tallpine" && mkdir "\$h/${lhome}" && echo note >"\$h/${lhome}/README"; echo migrate >>"\$h/cli.log"
SH
chmod +x "${T}/mig-b/${product}.app/Contents/Resources/daemon/tallpine"
cp -R "${T}/mig-a/${lproduct}.app" "${mapps}/"
"${T}/launch" "${mapps}/${lproduct}.app"
for i in $(seq 1 50); do [[ -f "${mh}/${lhome}/running.json" ]] && break; sleep 0.2; done
check "migration scratch: old app runs in the legacy home" '[[ "$(mdb "${mh}/${lhome}")" == "${A}" ]]'

touch "${mh}/fail-migrate"
if mrl install "${T}/mig-b/${product}.app" >"${T}/out" 2>&1; then bad "failed migration fails the install"; else ok "failed migration fails the install"; fi
check "failed migration: legacy data and old app restored, no new home" \
  '[[ "$(mdb "${mh}/${lhome}")" == "${A}" && ! -e "${mh}/.tallpine" && "$(/usr/libexec/PlistBuddy -c "Print :VOBuildCommit" "${mapps}/${lproduct}.app/Contents/Info.plist")" == "${A}" && ! -e "${mapps}/${product}.app" && "$(cat "${mh}/${lhome}/data/worktrees/w1/file")" == keep ]]'
rm -f "${mh}/fail-migrate"

pkill -f "${T}/fake_daemon.py ${mh}" 2>/dev/null || true
for _ in $(seq 1 50); do pgrep -f "${T}/fake_daemon.py ${mh}" >/dev/null || break; sleep 0.1; done
if mrl install "${T}/mig-b/${product}.app" >"${T}/out" 2>&1; then ok "install B migrates"; else bad "install B migrates"; cat "${T}/out"; fi
mpoint0="$(ls -1d "${mh}/.tallpine/releases"/2* | tail -n 1)"
check "migrated: the new CLI ran once" '[[ "$(cat "${mh}/cli.log")" == migrate ]]'
check "migrated: B runs in the new home on the snapshot-compatible db" '[[ "$(mdb "${mh}/.tallpine")" == "${A} ${B}" && -f "${mh}/.tallpine/running.json" ]]'
check "migrated: the legacy app moved into the rollback point, the new app is installed" \
  '[[ -d "${mpoint0}/app/${lproduct}.app" && ! -e "${mapps}/${lproduct}.app" && -d "${mapps}/${product}.app" ]]'
check "migrated: legacy home holds only the README" '[[ "$(ls -A "${mh}/${lhome}")" == README ]]'
check "migrated: worktrees came along" '[[ "$(cat "${mh}/.tallpine/data/worktrees/w1/file")" == keep ]]'
mpoint="$(ls -1d "${mh}/.tallpine/releases"/2* | tail -n 1)"
check "migrated: rollback point moved with the tree and records the migration" \
  'grep -q "^migrated_from=${mh}/${lhome}\$" "${mpoint}/meta" && grep -q "^data_dir=${mh}/.tallpine/data\$" "${mpoint}/meta"'

if mrl rollback >"${T}/out" 2>&1; then ok "rollback of a migrated install"; else bad "rollback of a migrated install"; cat "${T}/out"; fi
check "rollback: tree moved back, new home gone" '[[ ! -e "${mh}/.tallpine" && -d "${mh}/${lhome}/data" && ! -e "${mh}/${lhome}/README" ]]'
check "rollback: snapshot db and worktrees restored, old app back" \
  '[[ "$(mdb "${mh}/${lhome}")" == "${A}" && "$(cat "${mh}/${lhome}/data/worktrees/w1/file")" == keep && "$(/usr/libexec/PlistBuddy -c "Print :VOBuildCommit" "${mapps}/${lproduct}.app/Contents/Info.plist")" == "${A}" && ! -e "${mapps}/${product}.app" ]]'
check "rollback: the CLI reversed the rename" '[[ "$(tail -n 1 "${mh}/cli.log")" == rollback ]]'
pkill -f "${T}/fake_daemon.py ${mh}" 2>/dev/null || true

# The live-app guard refuses inside an agent session before reading anything.
# An empty state dir: even a broken guard finds no rollback point to act on.
if TALLPINE_SESSION_ID=x-1 "${release}" --app-dir "${HOME}/Applications" --state-dir "${T}/empty" rollback >"${T}/out" 2>&1; then
  bad "guard refuses the live app path inside a session"
else
  check "guard refuses the live app path inside a session" 'grep -q "refusing" "${T}/out"'
fi

