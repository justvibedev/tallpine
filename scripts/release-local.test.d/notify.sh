#!/usr/bin/env bash
# Sourced by scripts/release-local.test.sh (shares its state and helpers): the install result file that
# replaced --notify (B631), and stopping a daemon the app does not own with SIGTERM, in their own state and app dir.
# fake_cli records any CLI call, to prove nothing is sent to the daemon any more.
fake_cli() { # <app> <outfile> <rc>
  mkdir -p "$1/Contents/Resources/daemon"
  printf '#!/bin/bash\nprintf "%%s|" "$@" >>"%s"; printf "\\n" >>"%s"; exit %s\n' "$2" "$2" "$3" >"$1/Contents/Resources/daemon/tallpine"
  chmod +x "$1/Contents/Resources/daemon/tallpine"
}
result="${T}/the.log.result"
"${release}" --notify >/dev/null 2>&1 && bad "--notify without a value is refused" || ok "--notify without a value is refused"
nrl install "${T}/nonexistent.app" --notify orch-1 >"${T}/out" 2>&1 && bad "--notify without --log is refused" || ok "--notify without --log is refused"
check "--notify without --log says to poll the result file" 'grep -q -- "--notify was removed.*--log <path> and poll <path>.result" "${T}/out" || { cat "${T}/out"; false; }'
nrc="${T}/notify.out"; : >"${nrc}"
fake_cli "${T}/nb-c/${product}.app" "${nrc}" 0
fake_cli "${T}/nb-d/${product}.app" "${nrc}" 0

# B631: a daemon the app does not own (the app is gone, its daemon kept running) is stopped with SIGTERM,
# never POST /shutdown, which a signed daemon refuses (the fake records any POST in shutdown-refused).
pkill -9 -f "${napps}/${product}.app/Contents/MacOS/fake" 2>/dev/null || true
sleep 0.3
check "the daemon outlives its app" 'kill -0 "$(plutil -extract pid raw -o - "${nst}/running.json")" 2>/dev/null'
rm -f "${nst}/shutdown-refused"
printf 'stale\n' >"${result}"
TALLPINE_SESSION_ID=orch-x nrl install "${T}/nb-c/${product}.app" --notify orch-1 --log "${T}/the.log" >"${T}/out" 2>&1 \
  && ok "install C with --log succeeds" || { bad "install C with --log succeeds"; cat "${T}/out"; }
check "the bare daemon was stopped by SIGTERM, not /shutdown" 'grep -q "Stopping the daemon (pid" "${T}/out" && [[ ! -e "${nst}/shutdown-refused" ]] || { cat "${T}/out"; false; }'
check "--notify with --log is ignored with a warning" 'grep -q "warning: --notify is ignored (B631): poll ${result}" "${T}/out"'
check "success result: ok, then the message" '[[ "$(sed -n 1p "${result}")" == ok ]] && sed -n 2p "${result}" | grep -q "^\[release\] installed ${C:0:10}; daemon healthy (pid [0-9]*); rollback point ${nst}/releases/2[^;]*; log ${T}/the.log$"'
check "the result file has exactly two lines and no temp file is left" '[[ "$(wc -l <"${result}")" -eq 2 ]] && [[ -z "$(ls "${T}"/the.log.result.tmp.* 2>/dev/null)" ]]'
check "nothing is sent to the daemon" '[[ ! -s "${nrc}" ]] || { cat "${nrc}"; false; }'

# Failure: the new daemon never becomes healthy (a health timeout does not roll back).
oldht="${TALLPINE_RELEASE_HEALTH_TIMEOUT}"; export TALLPINE_RELEASE_HEALTH_TIMEOUT=2
printf '#!/bin/bash\nexit 0\n' >"${T}/nb-d/${product}.app/Contents/MacOS/fake"
if nrl install "${T}/nb-d/${product}.app" --log "${T}/the.log" >"${T}/out" 2>&1; then bad "install D with a dead daemon fails"; else ok "install D with a dead daemon fails"; fi
export TALLPINE_RELEASE_HEALTH_TIMEOUT="${oldht}"
check "failure result names the sha, a one-line reason and the log path" '[[ "$(sed -n 1p "${result}")" == failed ]] && sed -n 2p "${result}" | grep -q "^\[release\] install of ${D:0:10} FAILED: the new release did not come up healthy.*; log ${T}/the.log$"'
check "failure result carries no log contents" '! grep -q "Daemon healthy" "${result}"'

# An early refusal (no such app) still writes a result, replacing a stale one.
printf 'stale\n' >"${result}"
nrl install "${T}/nonexistent.app" --log "${T}/the.log" >"${T}/out" 2>&1 && bad "early refusal fails" || ok "early refusal fails"
check "early refusal writes a failed result" '[[ "$(sed -n 1p "${result}")" == failed ]] && sed -n 2p "${result}" | grep -q "^\[release\] install of unknown FAILED: .*not an app bundle.*; log ${T}/the.log$"'
# Without --log nothing is written.
rm -f "${result}"
nrl install "${T}/nonexistent.app" >"${T}/out" 2>&1 && bad "refusal without --log fails" || ok "refusal without --log fails"
check "no result file without --log" '[[ ! -e "${result}" ]] && ! grep -q "Result written" "${T}/out"'
# An unwritable result never changes the install result.
nrl install "${T}/nonexistent.app" --log "${T}/no-such-dir/x.log" >"${T}/out" 2>&1 && rc=0 || rc=$?
check "unwritable result: install keeps its own error and exit" '[[ ${rc} -eq 1 ]] && grep -q "not an app bundle" "${T}/out" && grep -q "could not write the result" "${T}/out" || { cat "${T}/out"; false; }'

# B263: the stale-checkout guard refuses before cmd_install runs; that refusal must write a result too.
sc="${T}/stale-checkout"; mkdir -p "${sc}/scripts" "${sc}/backend/internal/brand"
cp -R "${script_dir}/release-local" "${script_dir}/release-local.d" "${script_dir}/lib" "${script_dir}/release-local-publish.sh" "${sc}/scripts/"
cp "${script_dir}/../backend/internal/brand/brand.go" "${sc}/backend/internal/brand/"
git -C "${sc}" init -q && git -C "${sc}" add -A && git -C "${sc}" -c user.name=t -c user.email=t@t commit -qm base
git -C "${sc}" update-ref refs/heads/main HEAD
echo "# newer" >>"${sc}/scripts/release-local"
git -C "${sc}" -c user.name=t -c user.email=t@t commit -qam newer
git -C "${sc}" update-ref refs/remotes/origin/main HEAD && git -C "${sc}" reset -q --hard HEAD~1
rm -f "${result}"
env -u TALLPINE_RELEASE_SKIP_STALE_CHECK "${sc}/scripts/release-local" --state-dir "${nst}" --app-dir "${napps}" --no-fetch --yes --force \
  install "${T}/nonexistent.app" --log "${T}/the.log" >"${T}/out" 2>&1 && bad "stale checkout refuses install" || ok "stale checkout refuses install"
check "stale-checkout refusal writes a failed result with the reason" 'sed -n 2p "${result}" | grep -q "^\[release\] install of unknown FAILED: this checkout .*is behind it.*; log ${T}/the.log$" || { cat "${result}" "${T}/out"; false; }'
pkill -f "${nst}/" 2>/dev/null || true

# B357: the detached run's output is followed by byte count; output appended between the size read and the
# copy must not be printed twice. The result line lands 0.3s after the refusal and the follower pauses 0.8s
# between sizing and copying, so the second line always lands inside that window.
TALLPINE_RELEASE_TEST_FOLLOW_DELAY=0.8 TALLPINE_RELEASE_TEST_RESULT_DELAY=0.3 nrl install "${T}/nonexistent.app" --log "${T}/the.log" </dev/null >"${T}/out" 2>&1 && rc=0 || rc=$?
check "detached output is not duplicated when it grows while being followed" '[[ ${rc} -eq 1 ]] && [[ "$(grep -c "Result written" "${T}/out")" -eq 1 ]] && [[ "$(grep -c "not an app bundle" "${T}/out")" -eq 1 ]] || { cat "${T}/out"; false; }'
