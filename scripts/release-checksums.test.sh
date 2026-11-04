#!/usr/bin/env bash
# Regression test for scripts/release-checksums.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
d="$(mktemp -d)"; trap 'rm -rf "${d}"' EXIT
printf 'one' > "${d}/a.zip"; printf 'two' > "${d}/b.AppImage"
"${here}/release-checksums" "${d}" >/dev/null
[[ "$(wc -l < "${d}/SHA256SUMS" | tr -d ' ')" == 2 ]] || { echo "FAIL: expected 2 lines"; exit 1; }
grep -q "^$(printf one | shasum -a 256 | cut -d' ' -f1)  a.zip$" "${d}/SHA256SUMS" || { echo "FAIL: a.zip hash"; exit 1; }
grep -q 'SHA256SUMS$' "${d}/SHA256SUMS" && { echo "FAIL: lists itself"; exit 1; }
echo ok
