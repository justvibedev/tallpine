#!/usr/bin/env bash
# Tests for scripts/local-ci/install against a temp directory (never ~/.tallpine).
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/local-ci-install.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT
fails=0
check() { if ! "$@"; then fails=$((fails + 1)); echo "FAIL: $*"; fi; }
inst() { TALLPINE_LOCAL_CI_DIR="$tmp/ci" "$here/install" "$@"; }

check inst >/dev/null                              # fresh install
check cmp -s "$here/loop.sh" "$tmp/ci/loop.sh"
check inst >/dev/null                              # up to date: no-op
echo '# hand edit' >>"$tmp/ci/loop.sh"
if inst >/dev/null 2>&1; then fails=$((fails + 1)); echo "FAIL: install overwrote a local edit"; fi
check grep -q '# hand edit' "$tmp/ci/loop.sh"      # edit survived the refusal
check inst --force >/dev/null                      # --force replaces it
check cmp -s "$here/loop.sh" "$tmp/ci/loop.sh"
check test -n "$(ls "$tmp"/ci/loop.sh.bak-* 2>/dev/null)"   # and keeps a backup
# An older repo copy that was installed by this script (hash recorded) updates without --force.
printf '# older\n' >"$tmp/ci/loop.sh"; shasum -a 256 "$tmp/ci/loop.sh" | cut -d' ' -f1 >"$tmp/ci/.loop.sh.installed"
check inst >/dev/null
check cmp -s "$here/loop.sh" "$tmp/ci/loop.sh"
[ "$fails" = 0 ] && echo "local-ci install tests: ok"
exit "$fails"
