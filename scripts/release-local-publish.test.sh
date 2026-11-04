#!/usr/bin/env bash
# Tests for the guards of `scripts/release-local publish` with fake codesign,
# stapler and gh. Nothing is built, and gh never reaches GitHub.
#
#   scripts/release-local-publish.test.sh
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
release="${script_dir}/release-local"
product="$(sed -n 's/^[[:space:]]*ProductName[[:space:]]*=[[:space:]]*"\([^"]*\)".*/\1/p' "${script_dir}/../backend/internal/brand/brand.go")"

T="$(mktemp -d "${TMPDIR:-/tmp}/tp-publish-test.XXXXXX")"
T="$(cd "${T}" && pwd -P)"
trap 'rm -rf "${T}"' EXIT
fail=0
ok() { printf 'ok   %s\n' "$1"; }
bad() { printf 'FAIL %s\n' "$1"; fail=1; }

export TALLPINE_RELEASE_BUNDLE_ID=ee.justdev.tp-publish-test
export HOME="${T}/home"
mkdir -p "${HOME}"

# Fake codesign: Contents/team picks the TeamIdentifier (default TESTTEAM01).
cat >"${T}/codesign" <<'SH'
#!/bin/bash
app="${@: -1}"
team="$(cat "${app}/Contents/team" 2>/dev/null || echo TESTTEAM01)"
if [[ "$1" == --verify ]]; then exit 0; fi
if [[ "$1" == -dv ]]; then
  echo "CodeDirectory v=20500 size=1 flags=0x10000(runtime) hashes=1" >&2
  echo "TeamIdentifier=${team}" >&2
  exit 0
fi
echo "designated => identifier \"${TALLPINE_RELEASE_BUNDLE_ID}\" and certificate leaf[subject.OU] = ${team}" >&2
SH
# Fake pkg tools: pkgbuild/productsign write files and log; xcrun handles notarytool/stapler.
cat >"${T}/pkgbuild" <<SH
#!/bin/bash
echo "pkgbuild \$*" >>"${T}/pkg.log"
if [[ "\$1" == --analyze ]]; then printf '<?xml version="1.0"?><plist version="1.0"><array><dict><key>BundleIsRelocatable</key><true/></dict></array></plist>' >"\${@: -1}"; exit 0; fi
for a in "\$@"; do [[ "\$prev" == --component-plist ]] && plutil -extract 0.BundleIsRelocatable raw "\$a" >>"${T}/reloc"; prev="\$a"; done
: >"\${@: -1}"
SH
cat >"${T}/productsign" <<SH
#!/bin/bash
echo "productsign \$*" >>"${T}/pkg.log"; cp "\$3" "\$4"
SH
cat >"${T}/pkgutil" <<SH
#!/bin/bash
echo "pkgutil \$*" >>"${T}/pkg.log"; echo "   1. Developer ID Installer: Test (TESTTEAM01)"
SH
cat >"${T}/stapler" <<SH
#!/bin/bash
if [[ "\$2" == staple ]]; then echo "stapler \$*" >>"${T}/pkg.log"; exit 0; fi
if [[ "\$1" == notarytool ]]; then echo "notarytool \$*" >>"${T}/pkg.log"; [[ -f "${T}/notary-fail" ]] && { echo "status: Invalid"; exit 0; }; echo "status: Accepted"; exit 0; fi
[[ "\$2" == validate && "\${3}" == *.pkg ]] && exit 0
[[ -f "\${@: -1}/Contents/stapled" ]]
SH
cat >"${T}/security" <<SH
#!/bin/bash
[[ -f "${T}/no-cert" ]] || echo '  1) ABC "Developer ID Installer: Test (TESTTEAM01)"'
SH
chmod +x "${T}/security" "${T}/pkgbuild" "${T}/productsign" "${T}/pkgutil" "${T}/stapler"
export TALLPINE_RELEASE_PKGBUILD="${T}/pkgbuild" TALLPINE_RELEASE_PRODUCTSIGN="${T}/productsign" TALLPINE_RELEASE_PKGUTIL="${T}/pkgutil" TALLPINE_RELEASE_SECURITY="${T}/security"
export TALLPINE_INSTALLER_IDENTITY="Developer ID Installer: Test (TESTTEAM01)"

# Fake gh: logs every call; $T/tags lists tags that exist.
cat >"${T}/gh" <<SH
#!/bin/bash
echo "\$*" >>"${T}/gh.log"
if [[ "\$1" == release && "\$2" == view ]]; then
  grep -qx "\$3" "${T}/tags" 2>/dev/null && exit 0
  echo "release not found" >&2; exit 1
fi
if [[ "\$1" == api ]]; then
  grep -qx "\${2##*/}" "${T}/tags" 2>/dev/null && exit 0
  echo "gh: Not Found (HTTP 404)" >&2; exit 1
fi
exit 0
SH
chmod +x "${T}/codesign" "${T}/gh"
export TALLPINE_RELEASE_CODESIGN="${T}/codesign" TALLPINE_RELEASE_XCRUN="${T}/stapler" TALLPINE_RELEASE_GH="${T}/gh"
: >"${T}/tags"

app="${T}/build/frontend/out/${product}-darwin-arm64/${product}.app"
mkdir -p "${app}/Contents"
cat >"${app}/Contents/Info.plist" <<PL
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>${TALLPINE_RELEASE_BUNDLE_ID}</string>
<key>CFBundleShortVersionString</key><string>1.2.0</string>
<key>VOBuildDirty</key><string>false</string>
</dict></plist>
PL
touch "${app}/Contents/stapled"

publish() { "${release}" --state-dir "${T}/state" publish "${app}" "$@" >"${T}/out" 2>&1; }
refuses() { # refuses <name> <pattern> [publish args]
  local name="$1" pattern="$2"; shift 2
  if publish "$@"; then bad "${name}: refused"; return; fi
  if grep -q "${pattern}" "${T}/out"; then ok "${name}"; else bad "${name}: message"; cat "${T}/out"; fi
}
no_create() { if grep -q '^release create' "${T}/gh.log" 2>/dev/null; then bad "$1: no release created"; else ok "$1: no release created"; fi; }

unset TALLPINE_NOTARY_PROFILE
refuses "refuses without a notary profile" "TALLPINE_NOTARY_PROFILE is unset" --publish-github
no_create "no notary profile"

export TALLPINE_NOTARY_PROFILE=tallpine-notary TALLPINE_RELEASE_TEAM_ID=TESTTEAM01
(unset TALLPINE_INSTALLER_IDENTITY; refuses "refuses without an installer identity" "TALLPINE_INSTALLER_IDENTITY is unset" --publish-github)
no_create "no installer identity"
touch "${T}/no-cert"
refuses "refuses an installer identity not in the keychain" "not found in the keychain" --publish-github
no_create "identity not in keychain"
rm "${T}/no-cert"
rm "${app}/Contents/stapled"
refuses "refuses an app without a stapled ticket" "no stapled notarization ticket" --publish-github
touch "${app}/Contents/stapled"

echo OTHERTEAM1 >"${app}/Contents/team"
refuses "refuses another team's signature" "expected TESTTEAM01" --publish-github
rm "${app}/Contents/team"

echo v1.2.0 >"${T}/tags"
refuses "refuses an existing version tag" "already exists" --publish-github
no_create "existing tag"
: >"${T}/tags"

/usr/libexec/PlistBuddy -c "Set :VOBuildDirty true" "${app}/Contents/Info.plist"
refuses "refuses a dirty build" "dirty tree" --publish-github
/usr/libexec/PlistBuddy -c "Set :VOBuildDirty false" "${app}/Contents/Info.plist"

rm -f "${T}/gh.log"
if publish; then ok "dry run passes the guards"; else bad "dry run passes the guards"; cat "${T}/out"; fi
if grep -q "Dry run" "${T}/out"; then ok "dry run says nothing was uploaded"; else bad "dry run says nothing was uploaded"; fi
no_create "dry run without --publish-github"
if grep -q "release view v1.2.0 -R justvibedev/tallpine" "${T}/gh.log"; then ok "checks the tag on justvibedev/tallpine"; else bad "checks the tag on justvibedev/tallpine"; fi

# Full publish with a fake npm/node: the Jamf assets are in the release create call.
fe="${T}/build/frontend"
mkdir -p "${fe}/scripts" "${fe}/src/shared" "${app}/Contents/Resources/daemon" "${T}/bin"
: >"${fe}/package.json"; : >"${fe}/scripts/feed.mjs"; : >"${app}/Contents/Resources/daemon/tallpine"
echo '{"title":"schema"}' >"${fe}/src/shared/jamf-schema.json"
printf '#!/bin/bash\nmkdir -p out/make/zip/darwin && : >out/make/zip/darwin/x-1.2.0.zip && : >out/make/x.dmg\n' >"${T}/bin/npm"
printf '#!/bin/bash\nexit 0\n' >"${T}/bin/node"
chmod +x "${T}/bin/npm" "${T}/bin/node"
rm -f "${T}/gh.log"
rm -f "${T}/pkg.log"
if PATH="${T}/bin:${PATH}" publish --publish-github; then ok "publishes"; else bad "publishes"; cat "${T}/out"; fi
create="$(grep '^release create' "${T}/gh.log" || true)"
if [[ "${create}" == *"tallpine-jamf-schema.json"* && "${create}" == *"-PPPC.mobileconfig"* ]]; then ok "release create attaches PPPC and schema"; else bad "release create attaches PPPC and schema: ${create}"; fi
if [[ "${create}" == *"-1.2.0.pkg"* ]]; then ok "release create attaches the pkg"; else bad "release create attaches the pkg: ${create}"; fi
pk="$(cat "${T}/pkg.log")"
if [[ "$(cat "${T}/reloc")" == false ]]; then ok "pkg component plist is non-relocatable"; else bad "pkg component plist is non-relocatable: $(cat "${T}/reloc")"; fi
if [[ "${pk}" == *"--install-location /Applications"* && "${pk}" != *"--scripts"* ]]; then ok "pkg installs to /Applications without scripts"; else bad "pkg build args: ${pk}"; fi
order="$(grep -o '^[a-z]*' "${T}/pkg.log" | uniq | tr '\n' ' ')"
if [[ "${order}" == "pkgbuild productsign notarytool stapler pkgutil " ]]; then ok "pkg: build, sign, notarize, staple, verify"; else bad "pkg step order: ${order}"; fi
# Notarization failure stops before gh release create.
rm -f "${T}/gh.log"; touch "${T}/notary-fail"
if PATH="${T}/bin:${PATH}" publish --publish-github; then bad "failed pkg notarization refuses"; else ok "failed pkg notarization refuses"; fi
no_create "pkg notarization failure"
rm -f "${T}/notary-fail"
rm -f "${T}/gh.log"; echo adhoc >"${app}/Contents/Resources/daemon/tallpine.mode"
cat >"${T}/codesign2" <<'SH'
#!/bin/bash
if [[ "${@: -1}" == */daemon/tallpine && "$1" == -d && "$2" == -r- ]]; then echo 'designated => cdhash H"1"' >&2; exit; fi
exec "${REAL_CODESIGN}" "$@"
SH
chmod +x "${T}/codesign2"
if REAL_CODESIGN="${T}/codesign" TALLPINE_RELEASE_CODESIGN="${T}/codesign2" PATH="${T}/bin:${PATH}" publish --publish-github; then bad "ad hoc daemon refuses publish"; else ok "ad hoc daemon refuses publish"; fi
no_create "ad hoc daemon"

# `build` and `install` never make a pkg.
if grep -n 'build_pkg\|pkgbuild\|productsign' "$(dirname "${release}")"/release-local.d/{build,install}.sh >/dev/null; then bad "build never makes a pkg"; else ok "build never makes a pkg"; fi

exit "${fail}"
