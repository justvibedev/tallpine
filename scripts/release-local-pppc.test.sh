#!/usr/bin/env bash
# Tests for `scripts/release-local pppc` with a fake codesign. Needs plutil (macOS).
#
#   scripts/release-local-pppc.test.sh
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
release="${script_dir}/release-local"
product="$(sed -n 's/^[[:space:]]*ProductName[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' "${script_dir}/../backend/internal/brand/brand.go")"
cli="$(sed -n 's/^[[:space:]]*CLIName[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' "${script_dir}/../backend/internal/brand/brand.go")"

T="$(mktemp -d "${TMPDIR:-/tmp}/tp-pppc-test.XXXXXX")"
T="$(cd "${T}" && pwd -P)"
trap 'rm -rf "${T}"' EXIT
fail=0
ok() { printf 'ok   %s\n' "$1"; }
bad() { printf 'FAIL %s\n' "$1"; fail=1; }

export TALLPINE_RELEASE_BUNDLE_ID=ee.justdev.tp-pppc-test HOME="${T}/home" TMPDIR="${T}/tmp"
mkdir -p "${HOME}" "${TMPDIR}"

# Fake codesign. Contents/sigmode (app) or <daemon>.mode: stable | adhoc | unsigned | bad.
cp "${script_dir}/testdata/release-local-fake-codesign.sh" "${T}/codesign"
chmod +x "${T}/codesign"
export TALLPINE_RELEASE_CODESIGN="${T}/codesign"

app="${T}/${product}.app"
mkdir -p "${app}/Contents/Resources/daemon"
cat >"${app}/Contents/Info.plist" <<PL
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>CFBundleIdentifier</key><string>${TALLPINE_RELEASE_BUNDLE_ID}</string></dict></plist>
PL
daemon="${app}/Contents/Resources/daemon/${cli}"
: >"${daemon}"
out="${T}/out.mobileconfig"

run() { "${release}" --state-dir "${T}/state" pppc "${app}" --out "${out}" >"${T}/log" 2>&1; }
refuses() { # refuses <name> <pattern>
  rm -f "${out}" "${T}"/tmp/tallpine-pppc.*
  if run; then bad "$1: refused"; return; fi
  if grep -q "$2" "${T}/log" && [[ ! -e "${out}" ]] && ! ls "${T}"/tmp/tallpine-pppc.* >/dev/null 2>&1; then ok "$1"; else bad "$1: message/no output"; cat "${T}/log"; fi
}

if run; then ok "builds a profile"; else bad "builds a profile"; cat "${T}/log"; fi
plutil -lint "${out}" >/dev/null && ok "plutil -lint passes" || bad "plutil -lint passes"
for svc in SystemPolicyDocumentsFolder SystemPolicyDesktopFolder SystemPolicyDownloadsFolder; do
  [[ "$(grep -c "<key>${svc}</key>" "${out}")" == 1 ]] && ok "has ${svc}" || bad "has ${svc}"
done
[[ "$(grep -c "<string>${TALLPINE_RELEASE_BUNDLE_ID}</string>" "${out}")" == 3 ]] && ok "app entry per service" || bad "app entry per service"
[[ "$(grep -c "<string>${TALLPINE_RELEASE_BUNDLE_ID}.daemon</string>" "${out}")" == 3 ]] && ok "daemon entry per service" || bad "daemon entry per service"
[[ "$(grep -c 'leaf\[subject.OU\] = TESTTEAM01' "${out}")" == 6 ]] && ok "requirements come from codesign" || bad "requirements come from codesign"
grep -q "<string>TeamIdentifier</string>" "${out}" && grep -q "<string>TESTTEAM01</string>" "${out}" && grep -q com.apple.servicemanagement "${out}" && ok "login-items rule" || bad "login-items rule"
[[ "$(grep -c '<string>Allow</string>' "${out}")" == 6 ]] && ok "all Allow" || bad "all Allow"

echo adhoc >"${app}/Contents/sigmode"; refuses "refuses an ad hoc app" "unsigned, ad hoc"
echo bad >"${app}/Contents/sigmode"; refuses "refuses an app that fails verification" "fails verification"
rm "${app}/Contents/sigmode"
echo adhoc >"${daemon}.mode"; refuses "refuses an ad hoc daemon" "ad hoc signed or has no Team ID"
echo unsigned >"${daemon}.mode"; refuses "refuses an unsigned daemon" "unsigned"
rm "${daemon}.mode"
echo OTHERTEAM1 >"${daemon}.team"; refuses "refuses mixed teams" "different teams"
rm "${daemon}.team"
rm "${daemon}"; refuses "refuses a missing daemon" "daemon missing"

exit "${fail}"
