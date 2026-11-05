#!/usr/bin/env bash
# Sourced by scripts/release-local.test.sh (shares its state and helpers): B327: detached install/rollback, TERM shield, relaunch wait, rollback notify.
# ---- B327: an install/rollback survives the teardown of the session that launched it, and every
# undo relaunches the restored app and reports the failure to --notify sessions.
b="${T}/b327"; bst="${b}/state"; bapps="${b}/apps"; mkdir -p "${bst}" "${bapps}"
# </dev/null: detach_run only detaches without a tty, so these exercise it however the suite is started.
brl() { "${release}" --state-dir "${bst}" --app-dir "${bapps}" --no-fetch --yes --force "$@" </dev/null; }
# slow_app <dir> <commit>: an app that needs 3s to quit after SIGTERM, with a fake CLI that records sends.
bnrc="${b}/notify.out"; : >"${bnrc}"
slow_app() {
  make_app "${b}/$1" "$2" "${bst}"
  perl -pi -e 's/exit 0. TERM/sleep 3; exit 0\x27 TERM/' "${b}/$1/${product}.app/Contents/MacOS/fake"
  fake_cli "${b}/$1/${product}.app" "${bnrc}" 0
}
bwait() { local i; for i in $(seq 1 100); do [[ -f "${bst}/running.json" ]] && kill -0 "$(plutil -extract pid raw -o - "${bst}/running.json" 2>/dev/null)" 2>/dev/null && return 0; sleep 0.2; done; return 1; }
binst() { /usr/libexec/PlistBuddy -c 'Print :VOBuildCommit' "${bapps}/${product}.app/Contents/Info.plist"; }
echo tok >"${bst}/daemon.token"
slow_app old "${A}"; slow_app new "${D}"; slow_app newer "${C}"
cp -R "${b}/old/${product}.app" "${bapps}/"; "${T}/launch" "${bapps}/${product}.app"; bwait || bad "b327: old app did not start"
# A launcher that records its ancestry, to prove the run is not a descendant of the caller.
cat >"${b}/launchlog" <<'SH'
#!/bin/bash
p=$PPID; chain=""
for i in 1 2 3 4 5 6 7 8; do chain="$chain $p"; p=$(ps -o ppid= -p $p | tr -d ' '); [[ -z $p || $p -le 1 ]] && break; done
echo "launch chain:$chain" >>"__B__/chain.out"
exec "__T__/launch" "$@"
SH
perl -pi -e "s#__B__#${b}#g; s#__T__#${T}#g" "${b}/launchlog"
chmod +x "${b}/launchlog"
export TALLPINE_RELEASE_LAUNCHER="${b}/launchlog"

# SIGTERM while the app is quitting (what the session teardown sends) must not abort the install.
TALLPINE_RELEASE_TEST_ENVFILE="${b}/envfile" TALLPINE_SESSION_ID=orch-b327 TALLPINE_RELEASE_TEST_INTERRUPT=quitting TALLPINE_RELEASE_TEST_SIGNAL=TERM \
  brl install "${b}/new/${product}.app" --allow-in-session --notify orch-b --log "${T}/b.log" >"${T}/out" 2>&1 && rc=0 || rc=$?
check "b327: install survives SIGTERM while the app quits" '[[ ${rc} -eq 0 && "$(binst)" == "${D}" ]]'
chain="$(cat "${b}/chain.out" 2>/dev/null || true)"
check "b327: the detached run is not a descendant of the caller" '[[ "${chain}" == *"launch chain:"* && " ${chain} " != *" $$ "* ]]'
check "b327: the detached run dropped the session marker" '[[ -f "${b}/envfile" && ! -s "${b}/envfile" ]]'
bwait || bad "b327: new app did not come up"

# The app is still quitting when the install gives up: the undo waits for the exit and relaunches it.
: >"${bnrc}"; : >"${b}/chain.out"; before="$(plutil -extract pid raw -o - "${bst}/running.json")"
TALLPINE_RELEASE_STOP_TIMEOUT=1 TALLPINE_RELEASE_RELAUNCH_WAIT=20 brl install "${b}/newer/${product}.app" --notify orch-b --log "${T}/b.log" >"${T}/out" 2>&1 && rc=0 || rc=$?
check "b327: slow quit fails the install" '[[ ${rc} -ne 0 ]] && grep -q "did not quit within 1s" "${T}/out"'
check "b327: the undo relaunched the app" 'grep -q "launch chain:" "${b}/chain.out" && bwait && [[ "$(plutil -extract pid raw -o - "${bst}/running.json")" != "${before}" ]] && [[ "$(binst)" == "${D}" ]]'
check "b327: the failed install was reported" 'grep -q "^send|--session|orch-b|--message|\[release\] install of ${C:0:10} FAILED: .*did not quit.*; log ${T}/b.log|" "${bnrc}"'

# B585: the app was already relaunched (a healthy new daemon) while the old app process is still exiting: the undo
# must not wait for that exit or launch a second app, and still reports the failure.
: >"${bnrc}"; : >"${b}/chain.out"; before="$(plutil -extract pid raw -o - "${bst}/running.json")"
(
  for i in $(seq 1 50); do [[ -f "${bst}/running.json" ]] || break; sleep 0.05; done
  python3 "${T}/fake_daemon.py" "${bst}" "${D}" "${service}" >/dev/null 2>&1 &
) &
TALLPINE_RELEASE_STOP_TIMEOUT=1 TALLPINE_RELEASE_RELAUNCH_WAIT=20 brl install "${b}/newer/${product}.app" --notify orch-b --log "${T}/b.log" >"${T}/out" 2>&1 && rc=0 || rc=$?
wait
check "b585: the undo sees the app is already running again" '[[ ${rc} -ne 0 ]] && grep -q "is already running again; not relaunching it" "${T}/out" && ! grep -q "Launching" "${T}/out"'
check "b585: the undo launched nothing and the install failure was reported" '[[ ! -s "${b}/chain.out" ]] && grep -q "^send|--session|orch-b|--message|\[release\] install of ${C:0:10} FAILED: .*did not quit" "${bnrc}"'
# Back to a normal app: stop the stand-in daemon, let the old app process finish exiting, start the app.
bwait && kill -TERM "$(plutil -extract pid raw -o - "${bst}/running.json")"; sleep 4
"${T}/launch" "${bapps}/${product}.app"; bwait || bad "b585: app did not come back"

# Same for rollback.
: >"${bnrc}"; : >"${b}/chain.out"; before="$(plutil -extract pid raw -o - "${bst}/running.json")"
TALLPINE_RELEASE_STOP_TIMEOUT=1 TALLPINE_RELEASE_RELAUNCH_WAIT=20 brl rollback --notify orch-b --log "${T}/b.log" >"${T}/out" 2>&1 && rc=0 || rc=$?
check "b327: slow quit fails the rollback" '[[ ${rc} -ne 0 ]] && grep -q "did not quit within 1s" "${T}/out"'
check "b327: the rollback undo relaunched the app" 'grep -q "launch chain:" "${b}/chain.out" && bwait && [[ "$(plutil -extract pid raw -o - "${bst}/running.json")" != "${before}" ]] && [[ "$(binst)" == "${D}" ]]'
check "b327: the failed rollback was reported" 'grep -q "^send|--session|orch-b|--message|\[release\] rollback FAILED: .*did not quit.*; log ${T}/b.log|" "${bnrc}"'
# An agent session whose shell has a PTY on stdin is detached too; a user terminal and a non-session run are not.
ptyrun() { python3 -c 'import pty,sys; pty.spawn(sys.argv[1:])' "$@" >/dev/null 2>&1 || true; }
rm -f "${b}/envfile"
TALLPINE_SESSION_ID=orch-pty TALLPINE_RELEASE_TEST_ENVFILE="${b}/envfile" ptyrun "${release}" --state-dir "${bst}" --app-dir "${bapps}" --no-fetch --yes --allow-in-session install "${T}/none.app"
check "b327: an agent session with a PTY on stdin is detached (marker dropped)" '[[ -f "${b}/envfile" && ! -s "${b}/envfile" ]]'
rm -f "${b}/envfile"
TALLPINE_SESSION_ID=orch-pty TALLPINE_SHELL_TERMINAL=1 TALLPINE_RELEASE_TEST_ENVFILE="${b}/envfile" ptyrun "${release}" --state-dir "${bst}" --app-dir "${bapps}" --no-fetch --yes --allow-in-session install "${T}/none.app"
check "b327: a user terminal in a session is not detached" '[[ ! -e "${b}/envfile" ]]'
(unset TALLPINE_SESSION_ID; TALLPINE_RELEASE_TEST_ENVFILE="${b}/envfile" ptyrun "${release}" --state-dir "${bst}" --app-dir "${bapps}" --no-fetch --yes install "${T}/none.app")
check "b327: a plain terminal run is not detached" '[[ ! -e "${b}/envfile" ]]'
unset TALLPINE_RELEASE_LAUNCHER; export TALLPINE_RELEASE_LAUNCHER="${T}/launch"
pkill -f "${b}/" 2>/dev/null || true
