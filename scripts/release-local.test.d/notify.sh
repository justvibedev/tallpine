#!/usr/bin/env bash
# Sourced by scripts/release-local.test.sh (shares its state and helpers): --notify flag parsing and install notifications.
# --notify: flag parsing and message text with a fake CLI, in its own state and app dir.
fake_cli() { # <app> <outfile> <rc>
  mkdir -p "$1/Contents/Resources/daemon"
  printf '#!/bin/bash\nprintf "%%s|" "$@" >>"%s"; printf "\\nENV:%%s:%%s\\n" "${TALLPINE_SESSION_ID:-}" "${TALLPINE_RUN_FILE:-}" >>"%s"; exit %s\n' "$2" "$2" "$3" >"$1/Contents/Resources/daemon/tallpine"
  chmod +x "$1/Contents/Resources/daemon/tallpine"
}
"${release}" --notify >/dev/null 2>&1 && bad "--notify without a value is refused" || ok "--notify without a value is refused"
export TALLPINE_RELEASE_NOTIFY_INTERVAL=0 TALLPINE_RELEASE_NOTIFY_TIMEOUT=2
nrc="${T}/notify.out"; : >"${nrc}"
fake_cli "${T}/nb-c/${product}.app" "${nrc}" 0
fake_cli "${T}/nb-d/${product}.app" "${nrc}" 0
TALLPINE_SESSION_ID=orch-x nrl install "${T}/nb-c/${product}.app" --notify orch-1 --notify=orch-2 --log "${T}/the.log" >"${T}/out" 2>&1 \
  && ok "install C with --notify succeeds" || { bad "install C with --notify succeeds"; cat "${T}/out"; }
check "one success message per session" '[[ "$(grep -c "^send|--session|" "${nrc}")" -eq 2 ]] && grep -q "^send|--session|orch-1|--message|\[release\] installed ${C:0:10}; daemon healthy (pid [0-9]*); rollback point ${nst}/releases/2[^;]*; log ${T}/the.log|" "${nrc}" && grep -q "|orch-2|" "${nrc}"'
check "send runs without session env, with this install's run file" 'grep -q "^ENV::${nst}/running.json$" "${nrc}"'
# Failure: the new daemon never becomes healthy (a health timeout does not roll back); the message is still sent here because the fake CLI always succeeds.
: >"${nrc}"
oldht="${TALLPINE_RELEASE_HEALTH_TIMEOUT}"; export TALLPINE_RELEASE_HEALTH_TIMEOUT=2
printf '#!/bin/bash\nexit 0\n' >"${T}/nb-d/${product}.app/Contents/MacOS/fake"
if nrl install "${T}/nb-d/${product}.app" --notify orch-1 --log "${T}/the.log" >"${T}/out" 2>&1; then bad "install D with a dead daemon fails"; else ok "install D with a dead daemon fails"; fi
export TALLPINE_RELEASE_HEALTH_TIMEOUT="${oldht}"
check "failure message names the sha, a one-line reason and the log path" '[[ "$(grep -c "^send|" "${nrc}")" -eq 1 ]] && grep -q "^send|--session|orch-1|--message|\[release\] install of ${D:0:10} FAILED: the new release did not come up healthy.*; log ${T}/the.log|" "${nrc}"'
check "failure message carries no log contents" '! grep -q "Daemon healthy" "${nrc}"'
unset TALLPINE_RELEASE_NOTIFY_INTERVAL TALLPINE_RELEASE_NOTIFY_TIMEOUT

# An early refusal (no such app) still notifies.
: >"${nrc}"
nrl install "${T}/nonexistent.app" --notify orch-1 --log "${T}/the.log" >"${T}/out" 2>&1 && bad "early refusal fails" || ok "early refusal fails"
check "early refusal sends a FAILED message" 'grep -q "^send|--session|orch-1|--message|\[release\] install of unknown FAILED: .*not an app bundle.*; log ${T}/the.log|" "${nrc}"'
# Give-up path: a CLI that always fails never changes the install result, and the window is shared.
fake_cli "${napps}/${product}.app" "${nrc}" 1; : >"${nrc}"
TALLPINE_RELEASE_NOTIFY_TIMEOUT=5 TALLPINE_RELEASE_NOTIFY_INTERVAL=0 nrl install "${T}/nonexistent.app" --notify orch-1 --notify orch-2 >"${T}/out" 2>&1 && rc=0 || rc=$?
check "failing send: install exits 1" '[[ ${rc} -eq 1 ]] || { echo "rc=${rc}"; cat "${T}/out"; false; }'
check "failing send: install keeps its own error" 'grep -q "not an app bundle" "${T}/out" || { cat "${T}/out"; false; }'
check "failing send: warns once per session" '[[ "$(grep -c "could not notify session" "${T}/out")" -eq 2 ]] || { cat "${T}/out"; false; }'
# The window is proven by attempts, not the clock: the first session retries until the shared deadline passes, so
# the second gets exactly its one always-made attempt. A window per session would give it more.
check "failing send: retried, and both sessions share one window" '[[ "$(grep -c "^send|--session|orch-1|" "${nrc}")" -ge 2 && "$(grep -c "^send|--session|orch-2|" "${nrc}")" -eq 1 ]] || { cat "${nrc}"; false; }'
pkill -f "${nst}/" 2>/dev/null || true

# B357: the detached run's output is followed by byte count; output appended between the size read and the
# copy must not be printed twice. A slow failing CLI spaces the two warnings 0.3s apart and the follower
# pauses 0.8s between sizing and copying, so the second warning always lands inside that window.
printf '#!/bin/bash\nsleep 0.3; exit 1\n' >"${napps}/${product}.app/Contents/Resources/daemon/tallpine"
TALLPINE_RELEASE_TEST_FOLLOW_DELAY=0.8 TALLPINE_RELEASE_NOTIFY_TIMEOUT=1 TALLPINE_RELEASE_NOTIFY_INTERVAL=0 nrl install "${T}/nonexistent.app" --notify orch-1 --notify orch-2 >"${T}/out" 2>&1 && rc=0 || rc=$?
check "detached output is not duplicated when it grows while being followed" '[[ ${rc} -eq 1 ]] && [[ "$(grep -c "could not notify session" "${T}/out")" -eq 2 ]] && [[ "$(grep -c "not an app bundle" "${T}/out")" -eq 1 ]] || { cat "${T}/out"; false; }'
