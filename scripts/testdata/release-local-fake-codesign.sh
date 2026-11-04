#!/bin/bash
target="${@: -1}"
if [[ -d "${target}" ]]; then mode="$(cat "${target}/Contents/sigmode" 2>/dev/null || echo stable)"; id="${TALLPINE_RELEASE_BUNDLE_ID}"
else mode="$(cat "${target}.mode" 2>/dev/null || echo stable)"; id="${TALLPINE_RELEASE_BUNDLE_ID}.daemon"; fi
team="$(cat "${target}.team" 2>/dev/null || echo TESTTEAM01)"
[[ -d "${target}" ]] && team="$(cat "${target}/Contents/team" 2>/dev/null || echo TESTTEAM01)"
case "$1" in
--verify) [[ "${mode}" != bad ]]; exit ;;
-dv) if [[ "${mode}" == adhoc ]]; then echo "TeamIdentifier=not set" >&2; else echo "TeamIdentifier=${team}" >&2; fi; exit ;;
esac
case "${mode}" in
unsigned) echo "code object is not signed at all" >&2; exit 1 ;;
adhoc) echo "designated => cdhash H\"9ca3\"" >&2 ;;
*) echo "designated => identifier \"${id}\" and anchor apple generic and certificate leaf[subject.OU] = ${team}" >&2 ;;
esac
