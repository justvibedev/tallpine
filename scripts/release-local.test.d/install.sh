#!/usr/bin/env bash
# Sourced by scripts/release-local.test.sh (shares its state and helpers): install, rollback, signing, interrupt and retention checks.
# unchanged <label> <pid or ""> : B installed, db intact, no new point, running.
unchanged() {
  check "$1: B still installed with its data" '[[ "$(installed)" == "${B}" && "$(db)" == "${A} ${B}" ]]'
  check "$1: no new rollback point or leftovers" '[[ "$(points)" == "${points_before}" ]] && no_leftovers'
  expect_pid="$2"
  if [[ -n "${expect_pid}" ]]; then
    check "$1: B left running" '[[ "$(daemon_pid)" == "${expect_pid}" ]]'
  else
    check "$1: B relaunched" 'wait_up && kill -0 "$(daemon_pid)"'
  fi
}
daemon_pid() { plutil -extract pid raw -o - "${state}/running.json" 2>/dev/null || true; }
db() { tr '\n' ' ' <"${state}/data/ao.db" | sed 's/ $//'; }
installed() { /usr/libexec/PlistBuddy -c 'Print :VOBuildCommit' "${apps}/${product}.app/Contents/Info.plist"; }
wait_up() { local i; for i in $(seq 1 50); do [[ -f "${state}/running.json" ]] && return 0; sleep 0.2; done; return 1; }
wait_alive() { wait_up && kill -0 "$(daemon_pid)" 2>/dev/null; }

printf 'main\t%s\tpass\tall jobs green\nmain\t%s\tpass\tgreen\nmain\t%s\tfail\tred\n' "${B}" "${D}" "${C}" >"${TALLPINE_RELEASE_CI_TSV}"
printf 'main\t%s\tpass\tlater green\n' "${C}" >>"${TALLPINE_RELEASE_CI_TSV}"
for c in "${A}" "${B}" "${C}" "${D}"; do make_app "${T}/build-${c:0:1}" "${c}"; done
mkdir -p "${state}/data/worktrees/s1" "${state}/data/tmp" "${state}/data/logs" "${state}/data/prompts"
echo keep >"${state}/data/worktrees/s1/file"
echo scratch >"${state}/data/tmp/x"
echo log >"${state}/data/logs/daemon.log"
echo p1 >"${state}/data/prompts/p"
echo tok >"${state}/daemon.token"
echo '{"schemaVersion":2}' >"${state}/app-state.json"

# Release A is installed and running.
mkdir -p "${apps}" && cp -R "${T}/build-a/${product}.app" "${apps}/"
"${T}/launch" "${apps}/${product}.app"
wait_up || { echo "fake daemon did not start" >&2; exit 1; }
check "release A runs and migrated the db" '[[ "$(db)" == "${A}" ]]'

# Upgrade A -> B.
pid_a="$(daemon_pid)"
if rl install "${T}/build-b/${product}.app" >"${T}/out" 2>&1; then ok "install B succeeds"; else bad "install B succeeds"; cat "${T}/out"; fi
check "B is installed" '[[ "$(installed)" == "${B}" ]]'
check "B daemon runs (new pid)" '[[ -n "$(daemon_pid)" && "$(daemon_pid)" != "${pid_a}" ]]'
check "A daemon exited" '! kill -0 "${pid_a}" 2>/dev/null'
check "B migrated the db" '[[ "$(db)" == "${A} ${B}" ]]'
point="$(ls -1d "${state}/releases"/2* | tail -n 1)"
check "rollback point keeps app A" '[[ "$(/usr/libexec/PlistBuddy -c "Print :VOBuildCommit" "${point}/app/${product}.app/Contents/Info.plist")" == "${A}" ]]'
check "snapshot holds the pre-upgrade db" '[[ "$(tr "\n" " " <"${point}/data/ao.db")" == "${A} " ]]'
check "snapshot holds prompts and app-state.json" '[[ -f "${point}/data/prompts/p" && -f "${point}/app-state.json" ]]'
check "snapshot skips worktrees, tmp, logs, lock" '[[ ! -e "${point}/data/worktrees" && ! -e "${point}/data/tmp" && ! -e "${point}/data/logs" && ! -e "${point}/data/daemon.lock" ]]'
check "no second app left in the app dir" '[[ "$(ls "${apps}")" == "${product}.app" ]]'
check "staging dir and lock are gone" '[[ -z "$(ls -A "${state}/releases" | grep -v "^2")" ]]'

# A commit whose latest CI row is not pass is refused before anything stops.
pid_b="$(daemon_pid)"
printf 'main\t%s\tfail\tredder\n' "${C}" >>"${TALLPINE_RELEASE_CI_TSV}"
if rl install "${T}/build-c/${product}.app" >"${T}/out" 2>&1; then bad "install refuses a failing commit"; else ok "install refuses a failing commit"; fi
check "refusal names the CI row" 'grep -q "fail" "${T}/out"'
check "refusal left B running" '[[ "$(daemon_pid)" == "${pid_b}" && "$(installed)" == "${B}" ]]'
if rl install "${T}/build-a/${product}.app" >"${T}/out" 2>&1; then bad "install refuses a commit with no CI row"; else ok "install refuses a commit with no CI row"; fi

# A wrong bundle id is refused.
/usr/libexec/PlistBuddy -c 'Set :CFBundleIdentifier com.example.other' "${T}/build-c/${product}.app/Contents/Info.plist"
if rl --force install "${T}/build-c/${product}.app" >"${T}/out" 2>&1; then bad "install refuses another bundle id"; else ok "install refuses another bundle id"; fi
/usr/libexec/PlistBuddy -c "Set :CFBundleIdentifier ee.justdev.tallpine-release-test.other" "${T}/build-c/${product}.app/Contents/Info.plist"

# A failing snapshot aborts and relaunches the current release unchanged.
echo secret >"${state}/data/unreadable" && chmod 000 "${state}/data/unreadable"
points_before="$(ls -1d "${state}/releases"/2* | wc -l | tr -d ' ')"
if rl install "${T}/build-d/${product}.app" >"${T}/out" 2>&1; then bad "install aborts when the snapshot fails"; else ok "install aborts when the snapshot fails"; fi
check_relaunch "abort keeps B installed and relaunched" '[[ "$(installed)" == "${B}" ]] && wait_up && [[ "$(daemon_pid)" != "${pid_b}" ]]'
check "abort leaves no new rollback point" '[[ "$(ls -1d "${state}/releases"/2* | wc -l | tr -d " ")" == "${points_before}" && -z "$(ls -A "${state}/releases" | grep -v "^2")" ]]'
chmod 600 "${state}/data/unreadable" && rm -f "${state}/data/unreadable"
wait_alive

# B585: a write to ao.db while the snapshot is taken (a relaunched app's daemon) aborts with a message naming
# the cause, and the undo relaunches the current release.
points_before="$(ls -1d "${state}/releases"/2* | wc -l | tr -d ' ')"
if TALLPINE_RELEASE_TEST_DURING_SNAPSHOT='sleep 1; touch "${data_dir}/ao.db"' rl install "${T}/build-d/${product}.app" >"${T}/out" 2>&1; then bad "install aborts when ao.db is written during the snapshot"; else ok "install aborts when ao.db is written during the snapshot"; fi
check "the abort names the database change" 'grep -q "changed while it was being snapshotted" "${T}/out"'
check_relaunch "ao.db write abort keeps B installed and relaunched" '[[ "$(installed)" == "${B}" ]] && wait_up && kill -0 "$(daemon_pid)"'
check "ao.db write abort leaves no new rollback point" '[[ "$(ls -1d "${state}/releases"/2* | wc -l | tr -d " ")" == "${points_before}" ]]'
wait_alive

# B611: a process holding ao.db at the snapshot is named with its pid, ppid, start time and command, with what to do.
if TALLPINE_RELEASE_TEST_DURING_SNAPSHOT='bash -c "sleep 31.611 <${data_dir}/ao.db; :" >/dev/null 2>&1 & sleep 1' rl install "${T}/build-d/${product}.app" >"${T}/out" 2>&1; then bad "install aborts when a process holds ao.db during the snapshot"; else ok "install aborts when a process holds ao.db during the snapshot"; fi
check "the abort names the holder's pid, ppid, start and command" 'grep -Eq "pid [0-9]+: ppid [0-9]+, started .+, command: sleep 31" "${T}/out"'
check "the holder's parent gets its own line with ppid and start" 'p="$(sed -nE "s/.*pid [0-9]+: ppid ([0-9]+), started .+, command: sleep 31.*/\1/p" "${T}/out" | head -1)"; [[ -n "${p}" ]] && grep -Eq "pid ${p}: ppid [0-9]+, started .+, command: " "${T}/out"'
check "the abort says what to do" 'grep -q "do not reopen it until the install finishes" "${T}/out"'
pkill -f "sleep 31.611" 2>/dev/null || true
wait_up || true
wait_alive

# A failure to move the current app aside leaves no data-only point behind.
chmod 555 "${apps}"
if rl install "${T}/build-d/${product}.app" >"${T}/out" 2>&1; then bad "install aborts when the app can not move"; else ok "install aborts when the app can not move"; fi
chmod 755 "${apps}"
check "app-move abort keeps B installed and running" '[[ "$(installed)" == "${B}" ]] && wait_up'
check "app-move abort leaves no new rollback point" '[[ "$(ls -1d "${state}/releases"/2* | wc -l | tr -d " ")" == "${points_before}" && -z "$(ls -A "${state}/releases" | grep -v "^2")" ]]'
wait_alive

# A signature that does not verify refuses the install; ad hoc only warns.
pid_b="$(daemon_pid)"
echo bad >"${T}/build-d/${product}.app/Contents/sigmode"
if rl install "${T}/build-d/${product}.app" >"${T}/out" 2>&1; then bad "install refuses an invalid signature"; else ok "install refuses an invalid signature"; fi
check "invalid signature named in the error" 'grep -q "invalid code signature" "${T}/out"'
unchanged "invalid-signature install" "${pid_b}"
echo adhoc >"${T}/build-d/${product}.app/Contents/sigmode"
rl_ask install "${T}/build-d/${product}.app" >"${T}/out" 2>&1 || true
check "ad hoc signature warns that grants will not persist" 'grep -q "signed ad hoc" "${T}/out"'
rm -f "${T}/build-d/${product}.app/Contents/sigmode"

# A signed build without the hardened runtime is refused; one with it passes.
echo norunt >"${T}/build-d/${product}.app/Contents/sigmode"
if "${release}" hardened-check "${T}/build-d/${product}.app" >"${T}/out" 2>&1; then bad "build refuses a missing hardened runtime"; else ok "build refuses a missing hardened runtime"; fi
check "refusal names the hardened runtime" 'grep -q "without the hardened runtime" "${T}/out"'
rm -f "${T}/build-d/${product}.app/Contents/sigmode"
check "hardened runtime present passes" '"${release}" hardened-check "${T}/build-d/${product}.app" >"${T}/out" 2>&1'

# Every bundled helper must carry the release Team ID and the hardened runtime.
printf '%s\n' '#!/bin/bash
f="${@: -1}"; m="$(cat "${f}")"
if [[ "${m}" == other ]]; then echo "TeamIdentifier=OTHERTEAM1" >&2; elif [[ "${m}" == adhoc ]]; then echo "TeamIdentifier=not set" >&2; else echo "TeamIdentifier=TESTTEAM01" >&2; fi
if [[ "${m}" == norunt ]]; then echo "CodeDirectory v=20500 flags=0x0(none) hashes=1" >&2; else echo "CodeDirectory v=20500 flags=0x10000(runtime) hashes=1" >&2; fi' >"${T}/codesign-helpers"
chmod +x "${T}/codesign-helpers"
H="${T}/helpers.app/Contents/Resources"
for rel in daemon/tallpine agent-browser/agent-browser wake-helper/tallpine-wake cloudflared/cloudflared tmux/bin/tmux acp-runtime/node/bin/node; do mkdir -p "$(dirname "${H}/${rel}")"; echo ok >"${H}/${rel}"; done
hc() { HOME="${T}/nohome" TALLPINE_RELEASE_TEAM_ID=TESTTEAM01 TALLPINE_RELEASE_CODESIGN="${T}/codesign-helpers" "${release}" helpers-check "${T}/helpers.app" >"${T}/out" 2>&1; }
check "helpers signed by the release team with runtime pass" 'hc'
if HOME="${T}/nohome" env -u TALLPINE_SIGN_IDENTITY -u APPLE_SIGNING_IDENTITY -u TALLPINE_RELEASE_TEAM_ID TALLPINE_RELEASE_CODESIGN="${T}/codesign-helpers" "${release}" helpers-check "${T}/helpers.app" >"${T}/out" 2>&1; then bad "unknown release team id fails closed"; else ok "unknown release team id fails closed"; fi
check "unknown release team id says so" 'grep -q "release Team ID unknown" "${T}/out"'
check "team id falls back to the identity suffix" 'HOME="${T}/nohome" env -u TALLPINE_RELEASE_TEAM_ID TALLPINE_SIGN_IDENTITY="Developer ID Application: Test (TESTTEAM01)" TALLPINE_RELEASE_CODESIGN="${T}/codesign-helpers" "${release}" helpers-check "${T}/helpers.app"'
for bad_mode in other adhoc norunt; do
  echo "${bad_mode}" >"${H}/cloudflared/cloudflared"
  if hc; then bad "helper guard refuses ${bad_mode} helper"; else ok "helper guard refuses ${bad_mode} helper"; fi
  check "refusal names the ${bad_mode} helper" 'grep -q "cloudflared/cloudflared" "${T}/out"'
done
echo ok >"${H}/cloudflared/cloudflared"
rm "${H}/tmux/bin/tmux"
if hc; then bad "helper guard refuses a missing helper"; else ok "helper guard refuses a missing helper"; fi

# The identity comes only from the environment; none refuses unless --allow-unsigned.
if HOME="${T}/nohome" env -u TALLPINE_SIGN_IDENTITY -u APPLE_SIGNING_IDENTITY "${release}" signing-setup >"${T}/out" 2>&1; then bad "no identity configured: refused"; else ok "no identity configured: refused"; fi
if HOME="${T}/nohome" env -u TALLPINE_SIGN_IDENTITY -u APPLE_SIGNING_IDENTITY "${release}" --state-dir "${T}/empty" --no-fetch build >"${T}/out" 2>&1; then bad "build with no identity is refused"; else ok "build with no identity is refused"; fi
check "build refuses before fetching, checking CI or installing" 'grep -q "refusing to build a release without signing" "${T}/out" && ! grep -q "Building \|Installing dependencies" "${T}/out"'
check "refusal names --allow-unsigned" 'grep -q "refusing to build a release without signing" "${T}/out" && grep -q -- "--allow-unsigned" "${T}/out"'
HOME="${T}/nohome" env -u TALLPINE_SIGN_IDENTITY -u APPLE_SIGNING_IDENTITY "${release}" --allow-unsigned signing-setup >"${T}/out" 2>&1 || true
check "--allow-unsigned: ad hoc with a loud warning" 'grep -q "^identity=$" "${T}/out" && grep -q "UNSIGNED BUILD" "${T}/out" && grep -q "signed ad hoc" "${T}/out"'
HOME="${T}/nohome" TALLPINE_SIGN_IDENTITY="Developer ID Application: Test Co (TEAMID)" "${release}" signing-setup >"${T}/out" 2>&1 || true
check "identity from TALLPINE_SIGN_IDENTITY is used" 'grep -q "^identity=Developer ID Application: Test Co (TEAMID)$" "${T}/out" && ! grep -q "UNSIGNED BUILD" "${T}/out"'
mkdir -p "${T}/relhome/.tallpine"; echo 'export TALLPINE_SIGN_IDENTITY="From File (TEAMID)"' >"${T}/relhome/.tallpine/release.env"
HOME="${T}/relhome" env -u TALLPINE_SIGN_IDENTITY "${release}" signing-setup >"${T}/out" 2>&1 || true
check "identity from ~/.tallpine/release.env is used" 'grep -q "^identity=From File (TEAMID)$" "${T}/out"'
# A plain (non-exported) assignment in release.env must still reach child processes.
echo 'TALLPINE_RELEASE_GITHUB_REPO=example-owner/example-repo' >>"${T}/relhome/.tallpine/release.env"
HOME="${T}/relhome" env -u TALLPINE_SIGN_IDENTITY -u TALLPINE_RELEASE_GITHUB_REPO "${release}" signing-setup >"${T}/out" 2>&1 || true
check "TALLPINE_RELEASE_GITHUB_REPO from release.env is exported to the build env" 'grep -q "^release_repo=example-owner/example-repo$" "${T}/out"'

# Notary profile: release.env is exported, unset skips.
HOME="${T}/nohome" env -u TALLPINE_NOTARY_PROFILE TALLPINE_SIGN_IDENTITY=Id "${release}" notary-setup >"${T}/out" 2>&1 || true
check "no --notarize: skipped with one line" 'grep -q "^profile=$" "${T}/out" && [[ "$(grep -c "Notarization skipped" "${T}/out")" -eq 1 ]]'
HOME="${T}/nohome" TALLPINE_SIGN_IDENTITY=Id TALLPINE_NOTARY_PROFILE=p "${release}" notary-setup >"${T}/out" 2>&1 || true
check "profile set but no --notarize: not exported" 'grep -q "^profile=$" "${T}/out"'
if HOME="${T}/nohome" env -u TALLPINE_NOTARY_PROFILE TALLPINE_SIGN_IDENTITY=Id "${release}" --notarize notary-setup >"${T}/out" 2>&1; then bad "--notarize without a profile fails"; else ok "--notarize without a profile fails"; fi
HOME="${T}/nohome" TALLPINE_SIGN_IDENTITY=Id TALLPINE_NOTARY_PROFILE=new-p "${release}" --notarize notary-setup >"${T}/out" 2>&1 || true
check "TALLPINE_NOTARY_PROFILE is exported to the build" 'grep -q "^profile=new-p$" "${T}/out"'
mkdir -p "${T}/nhome/.tallpine"; echo 'TALLPINE_NOTARY_PROFILE=file-p' >"${T}/nhome/.tallpine/release.env"
HOME="${T}/nhome" env -u TALLPINE_NOTARY_PROFILE TALLPINE_SIGN_IDENTITY=Id "${release}" --notarize notary-setup >"${T}/out" 2>&1 || true
check "a plain release.env assignment is exported to the build" 'grep -q "^profile=file-p$" "${T}/out"'
HOME="${T}/nohome" env -u TALLPINE_SIGN_IDENTITY -u APPLE_SIGNING_IDENTITY TALLPINE_NOTARY_PROFILE=p "${release}" --notarize --allow-unsigned notary-setup >"${T}/out" 2>&1 || true
check "--allow-unsigned with a profile set: not exported, skip line" 'grep -q "^profile=$" "${T}/out" && grep -q "Notarization skipped: unsigned build" "${T}/out"'
# Fake xcrun/spctl drive notary-check and the notarized guard.
mkdir -p "${T}/fakebin"
cat >"${T}/fakebin/xcrun" <<'FAKE'
#!/usr/bin/env bash
case "$1 $2" in
"notarytool history") [[ "${FAKE_NOTARY_FAIL:-0}" == 1 ]] && { echo "bad creds SECRET" >&2; exit 1; }; printf '  id: a\n  id: b\n' ;;
"stapler validate") [[ "${FAKE_STAPLE_FAIL:-0}" == 1 ]] && exit 1; exit 0 ;;
esac
FAKE
cat >"${T}/fakebin/spctl" <<'FAKE'
#!/usr/bin/env bash
echo "${FAKE_SPCTL:-accepted source=Notarized Developer ID}"
FAKE
chmod +x "${T}/fakebin/xcrun" "${T}/fakebin/spctl"
cat >"${T}/fakebin/ioreg" <<'FAKE'
#!/usr/bin/env bash
printf '<dict><key>CGSSessionScreenIsLocked</key>\n<%s/></dict>\n' "${FAKE_LOCKED:-false}"
FAKE
chmod +x "${T}/fakebin/ioreg"
nenv=(env TALLPINE_RELEASE_IOREG="${T}/fakebin/ioreg" TALLPINE_NOTARY_PROFILE=p TALLPINE_RELEASE_TEAM_ID=TESTTEAM01 TALLPINE_RELEASE_XCRUN="${T}/fakebin/xcrun" TALLPINE_RELEASE_SPCTL="${T}/fakebin/spctl")
check "notary-check reports ok and the count" '"${nenv[@]}" "${release}" notary-check >"${T}/out" 2>&1 && grep -q "ok.*(2 submissions" "${T}/out"'
if "${nenv[@]}" FAKE_NOTARY_FAIL=1 "${release}" notary-check >"${T}/out" 2>&1; then bad "notary-check fails on bad credentials"; else ok "notary-check fails on bad credentials"; fi
if "${nenv[@]}" FAKE_LOCKED=true "${release}" notary-check >"${T}/out" 2>&1; then bad "notary-check fails when locked"; else ok "notary-check fails when locked"; fi
check "locked Mac says unlock, not store-credentials" 'grep -q "Mac is locked" "${T}/out" && ! grep -q store-credentials "${T}/out"'
check "unlocked Mac still checks the profile" '"${nenv[@]}" "${release}" notary-check >"${T}/out" 2>&1'
check "notary-check does not echo tool output" '! grep -q SECRET "${T}/out"'
if HOME="${T}/nohome" env -u TALLPINE_NOTARY_PROFILE "${release}" notary-check >"${T}/out" 2>&1; then bad "notary-check without a profile fails"; else ok "notary-check without a profile fails"; fi
check "notarized artifacts pass the guard" '"${nenv[@]}" "${release}" notarized-check "${T}/build-d/${product}.app" "${T}/x.dmg" >"${T}/out" 2>&1'
if "${nenv[@]}" FAKE_STAPLE_FAIL=1 "${release}" notarized-check "${T}/build-d/${product}.app" >"${T}/out" 2>&1; then bad "unstapled app fails the guard"; else ok "unstapled app fails the guard"; fi
check "unstapled refusal says so" 'grep -q "not stapled" "${T}/out"'
if "${nenv[@]}" FAKE_SPCTL="accepted source=Developer ID" "${release}" notarized-check "${T}/build-d/${product}.app" >"${T}/out" 2>&1; then bad "non-notarized Gatekeeper source fails the guard"; else ok "non-notarized Gatekeeper source fails the guard"; fi

# Without --yes and without a terminal, install asks and refuses.
pid_b="$(daemon_pid)"
if rl_ask install "${T}/build-d/${product}.app" >"${T}/out" 2>&1; then bad "install asks before quitting"; else ok "install asks before quitting"; fi
check "the notice says agents stop and drafts are discarded" 'grep -q "every running agent" "${T}/out" && grep -q "drafts are discarded" "${T}/out"'
unchanged "unconfirmed install" "${pid_b}"

# A dirty build is refused without --force.
cp -R "${T}/build-d" "${T}/build-e"
/usr/libexec/PlistBuddy -c 'Set :VOBuildDirty true' "${T}/build-e/${product}.app/Contents/Info.plist"
if rl install "${T}/build-e/${product}.app" >"${T}/out" 2>&1; then bad "install refuses a dirty build"; else ok "install refuses a dirty build"; fi
check "refusal names the dirty tree" 'grep -q "dirty tree" "${T}/out"'
unchanged "dirty refusal" "${pid_b}"

# Another running copy of the app (same bundle id, other path) is refused.
mkdir -p "${T}/other/${product}.app/Contents/MacOS"
cp "${T}/build-d/${product}.app/Contents/Info.plist" "${T}/other/${product}.app/Contents/"
printf '#!/bin/bash\nsleep 60\n' >"${T}/other/${product}.app/Contents/MacOS/fake"
chmod +x "${T}/other/${product}.app/Contents/MacOS/fake"
"${T}/other/${product}.app/Contents/MacOS/fake" &
other_pid=$!
sleep 0.5
if rl install "${T}/build-d/${product}.app" >"${T}/out" 2>&1; then bad "install refuses another running copy"; else ok "install refuses another running copy"; fi
check "refusal names the other copy" 'grep -q "${T}/other/${product}.app" "${T}/out"'
unchanged "other copy" "${pid_b}"
kill "${other_pid}" 2>/dev/null; wait "${other_pid}" 2>/dev/null || true

# A copy running in a different sandbox (another concurrent test run) is not ours to refuse on.
foreign="$(mktemp -d "${TMPDIR:-/tmp}/tallpine-release-test.XXXXXX")"
mkdir -p "${foreign}/apps/${product}.app/Contents/MacOS"
cp "${T}/build-d/${product}.app/Contents/Info.plist" "${foreign}/apps/${product}.app/Contents/"
/usr/libexec/PlistBuddy -c "Set :CFBundleIdentifier ee.justdev.tallpine-release-test.other" "${foreign}/apps/${product}.app/Contents/Info.plist"
printf '#!/bin/bash\nsleep 60\n' >"${foreign}/apps/${product}.app/Contents/MacOS/fake"
chmod +x "${foreign}/apps/${product}.app/Contents/MacOS/fake"
"${foreign}/apps/${product}.app/Contents/MacOS/fake" &
foreign_pid=$!
sleep 0.5
rl_ask install "${T}/build-d/${product}.app" >"${T}/out" 2>&1 || true
check "a copy running in another sandbox does not block this one" '! grep -q "another copy" "${T}/out"'
kill "${foreign_pid}" 2>/dev/null; wait "${foreign_pid}" 2>/dev/null || true; rm -rf "${foreign}"

# A process still holding the database open after the stop aborts the install.
(exec 3<"${state}/data/ao.db"; exec sleep 60) &
holder_pid=$!
sleep 0.5
if rl install "${T}/build-d/${product}.app" >"${T}/out" 2>&1; then bad "install refuses while the db is open"; else ok "install refuses while the db is open"; fi
check "refusal names the holder" 'grep -q "still open by pid ${holder_pid}" "${T}/out"'
unchanged "db held open" ""
kill "${holder_pid}" 2>/dev/null; wait "${holder_pid}" 2>/dev/null || true
wait_alive

# A non-matching app process as the last `ps` line must not end the run
# silently: a stub ps appends one after the real list.
mkdir -p "${T}/stubbin" "${T}/notours/Other.app/Contents/MacOS"
printf '<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.example.notours</string></dict></plist>\n' >"${T}/notours/Other.app/Contents/Info.plist"
cat >"${T}/stubbin/ps" <<SH
#!/bin/bash
/bin/ps "\$@"
[[ "\$*" == *command=* ]] && echo "99999 ${T}/notours/Other.app/Contents/MacOS/other"
exit 0
SH
chmod +x "${T}/stubbin/ps"
if PATH="${T}/stubbin:${PATH}" rl_ask install "${T}/build-d/${product}.app" >"${T}/out" 2>&1; then bad "a trailing foreign app process is ignored"; else
  check "a trailing foreign app process is ignored (run reaches the confirmation)" 'grep -q "pass --yes" "${T}/out"'
fi
unchanged "trailing foreign app process" "$(daemon_pid)"

# Ctrl-C mid-install is undone: after the snapshot, and after the app moved.
for step in snapshot app-moved; do
  if TALLPINE_RELEASE_TEST_INTERRUPT="${step}" rl install "${T}/build-d/${product}.app" >"${T}/out" 2>&1; then
    bad "install interrupted at ${step} fails"
  else
    ok "install interrupted at ${step} fails"
  fi
  unchanged "install interrupted at ${step}" ""
  wait_alive
done

# A stale .partial point (from a killed run) is never a rollback point.
mkdir -p "${state}/releases/29990102T000000Z-stale.partial/data" "${state}/releases/29990102T000000Z-stale.partial/app/${product}.app"

# A data-only point (a first install onto an empty app dir) is skipped.
mkdir -p "${state}/releases/29990101T000000Z-unknown/data"

# status
if rl status >"${T}/out" 2>&1; then ok "status succeeds"; else bad "status succeeds"; cat "${T}/out"; fi
check "status shows installed commit, daemon and point" 'grep -q "Installed: ${B}" "${T}/out" && grep -q "commit ${B}" "${T}/out" && grep -q "app ${A}" "${T}/out"'
check "status shows the CI row" 'grep -q "CI (installed):   pass" "${T}/out"'
check "status does not list the .partial point" '! grep -q "stale" "${T}/out"'

# Rollback asks first, and checks the point belongs to this data dir.
pid_b="$(daemon_pid)"
points_before="$(points)"
if rl_ask rollback >"${T}/out" 2>&1; then bad "rollback asks before replacing data"; else ok "rollback asks before replacing data"; fi
check "the rollback notice names the point and what moves aside" 'grep -q "taken .*h ago" "${T}/out" && grep -q "Data written since then" "${T}/out"'
unchanged "unconfirmed rollback" "${pid_b}"
newest="$(ls -1d "${state}/releases"/2*Z-aaaaaaaaaaaa | tail -n 1)"
cp "${newest}/meta" "${T}/meta.bak"
sed -i '' "s|^data_dir=.*|data_dir=/elsewhere/data|" "${newest}/meta"
if rl rollback >"${T}/out" 2>&1; then bad "rollback refuses another data dir"; else ok "rollback refuses another data dir"; fi
check "refusal names the recorded data dir" 'grep -q "/elsewhere/data" "${T}/out"'
cp "${T}/meta.bak" "${newest}/meta"
unchanged "data-dir mismatch" "${pid_b}"

# A second Ctrl-C during the undo is ignored; the undo finishes.
for cmd in rollback install; do
  step=data-restored; [[ "${cmd}" == install ]] && step=app-moved
  if TALLPINE_RELEASE_TEST_INTERRUPT="${step}" TALLPINE_RELEASE_TEST_INTERRUPT_UNDO=1 rl ${cmd} "${T}/build-d/${product}.app" >"${T}/out" 2>&1; then
    bad "${cmd} with a second Ctrl-C during the undo fails"
  else
    ok "${cmd} with a second Ctrl-C during the undo fails"
  fi
  check "${cmd}: the undo ran to the end" 'grep -q "Ctrl-C is ignored until it finishes" "${T}/out" && grep -q "Launching" "${T}/out"'
  unchanged "${cmd} with a second Ctrl-C during the undo" ""
  check "${cmd} double Ctrl-C: data back in place, nothing split" '[[ -f "${state}/data/prompts/p" && -f "${state}/app-state.json" && -z "$(ls -A "${state}/releases/rolled-back" 2>/dev/null)" ]]'
  wait_alive
done

# SIGHUP (terminal closed) mid-rollback is undone like Ctrl-C.
if TALLPINE_RELEASE_TEST_INTERRUPT=data-restored TALLPINE_RELEASE_TEST_SIGNAL=HUP rl rollback >"${T}/out" 2>&1; then bad "rollback hung up midway fails"; else ok "rollback hung up midway fails"; fi
check "SIGHUP triggers the undo" 'grep -q "undoing it" "${T}/out"'
unchanged "rollback hung up midway" ""
wait_alive

# Without a running app, a failed install leaves it closed.
kill -TERM "$(ps -axo pid=,command= | awk -v exe="${apps}/${product}.app/Contents/MacOS/fake" 'index($0, exe) { print $1; exit }')"
for _ in $(seq 1 50); do [[ ! -f "${state}/running.json" ]] && break; sleep 0.1; done
if TALLPINE_RELEASE_TEST_INTERRUPT=snapshot rl install "${T}/build-d/${product}.app" >"${T}/out" 2>&1; then bad "install of a closed app interrupted fails"; else ok "install of a closed app interrupted fails"; fi
check "an app that was not running is not relaunched by the undo" '! grep -q "Launching" "${T}/out" && [[ ! -f "${state}/running.json" ]]'
"${T}/launch" "${apps}/${product}.app"
wait_alive

# Ctrl-C mid-rollback is undone at every step.
for step in data-moved data-restored app-moved; do
  if TALLPINE_RELEASE_TEST_INTERRUPT="${step}" rl rollback >"${T}/out" 2>&1; then
    bad "rollback interrupted at ${step} fails"
  else
    ok "rollback interrupted at ${step} fails"
  fi
  unchanged "rollback interrupted at ${step}" ""
  check "rollback interrupted at ${step}: post-upgrade data back in place" '[[ -f "${state}/data/prompts/p" && -f "${state}/app-state.json" && -z "$(ls -A "${state}/releases/rolled-back" 2>/dev/null)" ]]'
  wait_alive
done

# Roll back B -> A.
echo after-upgrade >"${state}/data/prompts/new"
if rl rollback >"${T}/out" 2>&1; then ok "rollback succeeds"; else bad "rollback succeeds"; cat "${T}/out"; fi
check "A is installed again" '[[ "$(installed)" == "${A}" ]]'
check "rollback names the skipped data-only point" 'grep -q "skipping newer data-only point(s) 29990101T000000Z-unknown" "${T}/out"'
check "db restored from the snapshot" '[[ "$(db)" == "${A}" ]]'
check "post-upgrade data moved aside, not deleted" '[[ ! -e "${state}/data/prompts/new" && -n "$(ls "${state}/releases/rolled-back"/*/data/prompts/new)" ]]'
check "B app kept with the rolled-back release" '[[ -n "$(ls -d "${state}/releases/rolled-back"/*/app/"${product}.app")" ]]'
check "worktrees untouched" '[[ "$(cat "${state}/data/worktrees/s1/file")" == keep ]]'
check "the stale .partial point was removed" '[[ ! -e "${state}/releases/29990102T000000Z-stale.partial" ]]'
check "the used point was consumed, the data-only one skipped" '[[ "$(ls -1d "${state}/releases"/2* 2>/dev/null)" == "${state}/releases/29990101T000000Z-unknown" ]]'
rm -rf "${state}/releases/29990101T000000Z-unknown"
if rl rollback >"${T}/out" 2>&1; then bad "rollback with no point fails"; else ok "rollback with no point fails"; fi
check "A still running after the failed rollback" '[[ -n "$(daemon_pid)" ]] && kill -0 "$(daemon_pid)"'

# Retention: with three older points seeded, one more upgrade keeps the newest three.
for n in 1 2 3; do mkdir -p "${state}/releases/2020010${n}T000000Z-seeded/data"; done
rl install "${T}/build-d/${product}.app" >"${T}/out" 2>&1 || { bad "install D for retention"; cat "${T}/out"; }
check "keeps the newest 3 rollback points" '[[ "$(ls -1d "${state}/releases"/2* | wc -l | tr -d " ")" == 3 ]]'
check "retention drops the oldest seeded point and keeps the newer ones" '[[ ! -e "${state}/releases/20200101T000000Z-seeded" && -d "${state}/releases/20200102T000000Z-seeded" && -d "${state}/releases/20200103T000000Z-seeded" ]]'
check "last install is D" '[[ "$(installed)" == "${D}" ]]'

