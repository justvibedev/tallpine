#!/usr/bin/env bash
# Tests for scripts/release-local install, rollback and status against a temp
# state dir, a temp app dir and a fake app whose "daemon" is a small Python
# HTTP server. Never touches ~/Applications, ~/.tallpine or the running daemon.
#
#   scripts/release-local.test.sh
set -euo pipefail

# A job started in the background (a CI loop, `cmd &` in a script) inherits SIGINT,
# SIGQUIT and SIGHUP as ignored, and bash cannot trap a signal that was ignored
# on entry. The interrupt tests make release-local signal itself, so under an
# ignored SIGINT its trap never fires and ~40 checks fail (B7). Restore the
# default dispositions once (Python itself ignores SIGPIPE and SIGXFSZ and execvp
# keeps that, so those are reset too), for this script and everything it launches.
if [[ -z "${TALLPINE_RELEASE_TEST_SIGNALS_RESET:-}" ]]; then
  export TALLPINE_RELEASE_TEST_SIGNALS_RESET=1
  exec python3 -c 'import os, signal, sys
for s in (signal.SIGINT, signal.SIGQUIT, signal.SIGHUP, signal.SIGPIPE, signal.SIGXFSZ):
    signal.signal(s, signal.SIG_DFL)
os.execvp(sys.argv[1], sys.argv[1:])' bash "$0" "$@"
fi

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
release="${script_dir}/release-local"
product="$(sed -n 's/^[[:space:]]*ProductName[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' "${script_dir}/../backend/internal/brand/brand.go")"
lhome="$(sed -n 's/^[[:space:]]*LegacyHomeDirName[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' "${script_dir}/../backend/internal/brand/brand.go")"
lproduct="$(sed -n 's/^[[:space:]]*LegacyProductName[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' "${script_dir}/../backend/internal/brand/brand.go")"
lservice="$(sed -n 's/^[[:space:]]*LegacyDaemonServiceName[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' "${script_dir}/../backend/internal/brand/brand.go")"
service="$(sed -n 's/^[[:space:]]*DaemonServiceName[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' "${script_dir}/../backend/internal/brand/brand.go")"
command -v python3 >/dev/null || { echo "python3 is required for the fake daemon" >&2; exit 1; }

# B187: the staleness guard must not depend on this checkout lagging origin/main.
export TALLPINE_RELEASE_SKIP_STALE_CHECK=1
# Alerts recorded by the detached build must not reach the real desktop.
export TALLPINE_ALERT_DESKTOP_NOTIFY=""
T="$(mktemp -d "${TMPDIR:-/tmp}/tallpine-release-test.XXXXXX")"
T="$(cd "${T}" && pwd -P)"
state="${T}/state"
apps="${T}/apps"
fail=0
cleanup() {
  if [[ -f "${state}/running.json" ]]; then
    kill "$(plutil -extract pid raw -o - "${state}/running.json" 2>/dev/null)" 2>/dev/null || true
  fi
  pkill -f "${T}/" 2>/dev/null || true
  rm -rf "${T}"
}
trap cleanup EXIT

ok() { printf 'ok   %s\n' "$1"; }
bad() { printf 'FAIL %s\n' "$1"; fail=1; }
# B463: the Claude security-mode fence denies ps on purpose (argv leaks); the install undo needs it to see the
# app quit before relaunching, so a relaunch assertion is skipped only when ps is actually denied there.
ps_denied() { [[ -n "${CLAUDE_CODE_TMPDIR:-}" ]] && ! ps -o etime= -p $$ >/dev/null 2>&1; }
check_relaunch() { if ps_denied; then printf 'skip %s (ps is denied by the Claude fence)\n' "$1"; else check "$@"; fi; }
check() { if eval "$2"; then ok "$1"; else bad "$1"; printf '     assertion: %s\n' "$2"; fi; }
check "SIGINT is not ignored by the test run" \
  'python3 -c "import signal,sys; sys.exit(signal.getsignal(signal.SIGINT) is not signal.default_int_handler)"'
check "SIGPIPE has its default disposition in the test run" \
  '[[ -z "$( { yes | head -1 >/dev/null; } 2>&1 )" ]]'

A=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
B=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
C=cccccccccccccccccccccccccccccccccccccccc
D=dddddddddddddddddddddddddddddddddddddddd

# Copied, not a heredoc: a body this large deadlocks bash when macOS shrinks pipes
# to 512 bytes under memory pressure (B23).
cp "$(dirname "${BASH_SOURCE[0]}")/testdata/release-local-fake-daemon.py" "${T}/fake_daemon.py"
export TALLPINE_RELEASE_DAEMON_MATCH="${T}/fake_daemon\\.py"

# make_app <dir> <commit>: a fake bundle with the test bundle id. Its
# executable is the "app" process; it runs the fake daemon as a child and
# stops it on SIGTERM, like the Electron main process stops its daemon.
make_app() {
  local app="$1/${4:-${product}}.app" commit="$2" st="${3:-${state}}" svc="${5:-${service}}"
  mkdir -p "${app}/Contents/MacOS"
  printf '<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>%s</string>
<key>CFBundleExecutable</key><string>fake</string>
<key>CFBundleShortVersionString</key><string>0.0.1</string>
<key>VOBuildCommit</key><string>%s</string>
<key>VOBuildDate</key><string>2026-10-26T00:00:00Z</string>
<key>VOBuildDirty</key><string>false</string>
</dict></plist>\n' "${TALLPINE_RELEASE_BUNDLE_ID}" "${commit}" >"${app}/Contents/Info.plist"
  cat >"${app}/Contents/MacOS/fake" <<SH
#!/bin/bash
python3 "${T}/fake_daemon.py" "${st}" "${commit}" "${svc}" &
child=\$!
trap 'kill -TERM "\$child" 2>/dev/null; wait "\$child"; exit 0' TERM
wait "\$child"
SH
  chmod +x "${app}/Contents/MacOS/fake"
}

# The app must not inherit the agent session's account home or tokens.
eval "$(sed -n '/^drop_agent_session_env() {/,/^}/p' "${release}" "${script_dir}"/release-local.d/*.sh)"
got="$(TMPDIR=/x/data/tmp/s1 GRADLE_USER_HOME=/x/data/caches/g TALLPINE_SUPERVISED_PROCESS=1 CLAUDE_CONFIG_DIR=/m CLAUDE_SECURESTORAGE_CONFIG_DIR=/m CODEX_HOME=/c KIRO_X=1 DEEPSEEK_Y=1 GH_TOKEN=t GITHUB_TOKEN=t TALLPINE_GITHUB_TOKEN=t LANG=C.UTF-8 \
  bash -c "$(declare -f drop_agent_session_env); drop_agent_session_env; env" | grep -E '^(GH_TOKEN|GITHUB_TOKEN|TALLPINE_GITHUB_TOKEN|CLAUDE_|CODEX_|KIRO_|DEEPSEEK_|LANG=|HOME=|PATH=|GRADLE_USER_HOME=|TALLPINE_SUPERVISED_PROCESS=|TMPDIR=.*/data/tmp)' | cut -d= -f1 | sort | tr '\n' ' ')"
check "launch drops agent-session vars, keeps PATH/HOME/LANG" '[[ "${got}" == "HOME LANG PATH " ]]'

cat >"${T}/launch" <<'SH'
#!/bin/bash
nohup "$1/Contents/MacOS/fake" >/dev/null 2>&1 &
SH
chmod +x "${T}/launch"

# Fake codesign: an app's Contents/sigmode picks "bad" (verify fails), "adhoc"
# (cdhash requirement) or, by default, a Developer ID requirement.
printf '%s\n' '#!/bin/bash
app="${@: -1}"
mode="$(cat "${app}/Contents/sigmode" 2>/dev/null || echo stable)"
if [[ "$1" == --verify ]]; then [[ "${mode}" != bad ]]; exit; fi
if [[ "$1" == -dv ]]; then
  if [[ "${mode}" == adhoc ]]; then echo "TeamIdentifier=not set" >&2; else echo "TeamIdentifier=TEAMID" >&2; fi
  if [[ "${mode}" == norunt ]]; then echo "CodeDirectory v=20500 size=1 flags=0x0(none) hashes=1" >&2
  else echo "CodeDirectory v=20500 size=1 flags=0x10000(runtime) hashes=1" >&2; fi
  echo "Signature size=9000" >&2; echo "TeamIdentifier=TEAMID" >&2
  exit
fi
if [[ "${mode}" == adhoc ]]; then
  echo "designated => cdhash H\"9ca3\"" >&2
else
  echo "designated => identifier \"${TALLPINE_RELEASE_BUNDLE_ID}\" and certificate leaf[subject.OU] = TEAMID" >&2
fi' >"${T}/codesign"
chmod +x "${T}/codesign"
export TALLPINE_RELEASE_CODESIGN="${T}/codesign"

export TALLPINE_RELEASE_BUNDLE_ID=ee.justdev.tallpine-release-test.$$
export TALLPINE_RELEASE_LAUNCHER="${T}/launch"
export TALLPINE_RELEASE_CI_TSV="${T}/ci.tsv"
export TALLPINE_RELEASE_POLL=0.1
export TALLPINE_RELEASE_HEALTH_TIMEOUT=20 TALLPINE_RELEASE_STOP_TIMEOUT=10
# A caller inside a Tallpine session (or a detached release run) leaks these; the tests set them explicitly.
unset TALLPINE_SESSION_ID TALLPINE_DATA_DIR TALLPINE_RUN_FILE TALLPINE_SHELL_TERMINAL TALLPINE_RELEASE_DETACHED TALLPINE_RELEASE_NO_DETACH TALLPINE_RELEASE_TEST_ENVFILE TALLPINE_PROJECT_ID TALLPINE_SESSION_TOKEN TALLPINE_SESSION_TOKEN_RUN_FILE TALLPINE_SESSION_AUTH TALLPINE_SESSION_AUTH_RUN_FILE VO_SESSION_ID
rl() { "${release}" --state-dir "${state}" --app-dir "${apps}" --no-fetch --yes "$@"; }
rl_ask() { "${release}" --state-dir "${state}" --app-dir "${apps}" --no-fetch "$@" </dev/null; }
points() { ls -1d "${state}/releases"/2* 2>/dev/null | grep -v '\.partial$' | wc -l | tr -d ' '; }
no_leftovers() { [[ -z "$(ls -A "${state}/releases" | grep -v '^2' | grep -v '^rolled-back$')" && -z "$(ls -d "${state}/releases"/*.partial 2>/dev/null)" ]]; }
# The checks live in release-local.test.d/, sourced in order; they share this file's state and helpers.
for part in install migration build notify detach lock daemons; do
  # shellcheck source=/dev/null
  . "${script_dir}/release-local.test.d/${part}.sh"
done
[[ "${fail}" -eq 0 ]] && echo "all release-local tests passed"
exit "${fail}"
