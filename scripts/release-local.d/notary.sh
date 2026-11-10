#!/usr/bin/env bash
# Sourced by scripts/release-local (never run directly): code signing, notarization checks, hardened-runtime and helper identity checks.
# ------------------------------------------------------------------ signing

# macOS keys privacy grants (Documents, Desktop, ...) to the app's code
# identity. An ad hoc signature is a cdhash, new on every build, so every
# install would ask again. A Developer ID signature gives a stable designated
# requirement (bundle id + team), which keeps the grants.
codesign_cmd() { "${TALLPINE_RELEASE_CODESIGN:-codesign}" "$@"; }

sign_identity() { printf '%s\n' "${TALLPINE_SIGN_IDENTITY:-${APPLE_SIGNING_IDENTITY:-}}"; }

# signing_setup: pick the identity, export it for forge (or unset it), warn
# loudly when the build will be ad hoc. Sets the caller's "identity".
signing_setup() {
  # release.env is sourced, not exported: without this the release repo never
  # reaches the npm -> build-daemon.mjs child that stamps it into the binary.
  [[ -n "${TALLPINE_RELEASE_GITHUB_REPO:-}" ]] && export TALLPINE_RELEASE_GITHUB_REPO
  identity="$(sign_identity)"
  if [[ -n "${identity}" ]]; then
    say "Signing with: ${identity} (a keychain dialog may ask to use the key: choose Always Allow)"
    export APPLE_SIGNING_IDENTITY="${identity}"
    unset TALLPINE_ALLOW_UNSIGNED
  elif [[ "${opt_allow_unsigned}" -eq 1 ]]; then
    unset APPLE_SIGNING_IDENTITY
    export TALLPINE_ALLOW_UNSIGNED=1
    warn "!!! UNSIGNED BUILD (--allow-unsigned): no signing identity found; the app will be signed ad hoc and is NOT a releasable artifact !!!"
    warn "macOS will ask again for Documents and other privacy access after every install"
  else
    die "no signing identity found (set TALLPINE_SIGN_IDENTITY or APPLE_SIGNING_IDENTITY, e.g. 'Developer ID Application: Your Company (TEAMID)', in the environment or in ~/.tallpine/release.env); refusing to build a release without signing. For a local test build only, pass --allow-unsigned"
  fi
}

notary_profile() { printf '%s\n' "${TALLPINE_NOTARY_PROFILE:-}"; }

# notary_setup: export the profile to the build (release.env is sourced, not
# exported) or say notarization is skipped. Sets the caller's "notary".
notary_setup() {
  notary="$(notary_profile)"
  if [[ "${opt_notarize}" -ne 1 ]]; then
    notary=""
    unset TALLPINE_NOTARY_PROFILE
    say "Notarization skipped (pass --notarize for a public release build)"
  elif [[ -n "${notary}" && -z "${identity:-}" ]]; then
    notary=""
    unset TALLPINE_NOTARY_PROFILE
    say "Notarization skipped: unsigned build"
  elif [[ -n "${notary}" ]]; then
    export TALLPINE_NOTARY_PROFILE="${notary}"
    say "Notarizing with keychain profile: ${notary}"
  else
    unset TALLPINE_NOTARY_PROFILE
    die "--notarize needs TALLPINE_NOTARY_PROFILE (environment or ~/.tallpine/release.env)"
  fi
}

# screen_locked: true while the macOS session is locked (the data-protection
# keychain, where notarytool profiles live, is unreadable then).
screen_locked() {
  "${TALLPINE_RELEASE_IOREG:-ioreg}" -n Root -d1 -a 2>/dev/null |
    tr -d '\n\t ' | grep -q 'CGSSessionScreenIsLocked</key><true/>'
}

# notary_check: verify the keychain profile works; prints only ok/failed + count.
notary_check() {
  local profile out count
  profile="$(notary_profile)"
  [[ -n "${profile}" ]] || die "no TALLPINE_NOTARY_PROFILE set (environment or ~/.tallpine/release.env)"
  screen_locked && die "the Mac is locked, so the keychain profile '${profile}' is unreadable: unlock the Mac and rerun (the profile is not missing)"
  out="$("${TALLPINE_RELEASE_XCRUN:-xcrun}" notarytool history --keychain-profile "${profile}" 2>&1)" ||
    die "notarytool could not use keychain profile '${profile}' (create it with: xcrun notarytool store-credentials ${profile} --apple-id <id> --team-id <team>)"
  count="$(printf '%s\n' "${out}" | grep -c '^ *id: ' || true)"
  say "notary-check: ok, profile '${profile}' works (${count} submissions in history)"
}

# require_notarized <app> [dmg]: die unless the artifacts are stapled and Gatekeeper
# accepts the app as a Notarized Developer ID.
require_notarized() {
  local app="$1" dmg="${2:-}" out
  "${TALLPINE_RELEASE_XCRUN:-xcrun}" stapler validate "${app}" >/dev/null 2>&1 ||
    die "notarization was requested but the app is not stapled (stapler validate failed): ${app}"
  if [[ -n "${dmg}" ]]; then
    "${TALLPINE_RELEASE_XCRUN:-xcrun}" stapler validate "${dmg}" >/dev/null 2>&1 ||
      die "notarization was requested but the dmg is not stapled (stapler validate failed): ${dmg}"
  fi
  out="$("${TALLPINE_RELEASE_SPCTL:-spctl}" -a -vvv -t execute "${app}" 2>&1)" || true
  [[ "${out}" == *"accepted"* && "${out}" == *"source=Notarized Developer ID"* ]] ||
    die "Gatekeeper does not report 'accepted, source=Notarized Developer ID' for ${app}"
}

# verify_signature <app>: 0 stable, 1 does not verify, 2 verifies but ad hoc.
verify_signature() {
  local app="$1" req team
  codesign_cmd --verify --deep --strict "${app}" >/dev/null 2>&1 || return 1
  req="$(codesign_cmd -d -r- "${app}" 2>&1 || true)"
  team="$(codesign_cmd -dv "${app}" 2>&1 | sed -n 's/^TeamIdentifier=//p' | head -n 1)"
  [[ -n "${team}" && "${team}" != "not set" ]] || return 2
  [[ "${req}" == *"leaf[subject.OU] = ${team}"* && "${req}" == *"identifier \"${bundle_id}\""* ]] || return 2
}

# require_hardened_runtime <app>: die unless codesign -dv shows the runtime flag.
# Capture first: grep -q exiting early would SIGPIPE codesign and, under
# pipefail, fail a correctly signed app.
require_hardened_runtime() {
  local app="$1" details
  details="$(codesign_cmd -dv "${app}" 2>&1 || true)"
  # Process substitution, not <<<: a here-string is written through a pipe before
  # grep starts, and codesign output exceeds the 512 bytes macOS shrinks pipes to
  # under memory pressure (B23), which deadlocks. The writer is its own process.
  grep -Eq '^CodeDirectory .*flags=0x[0-9a-f]+\([^)]*\bruntime\b' < <(printf '%s\n' "${details}") ||
    die "the app is signed without the hardened runtime (codesign -dv flags lack 'runtime'): ${app}"
}


# require_team_id_baked <app>: die unless the bundled daemon carries the release
# Team ID and cgo signature checking. Without them the daemon's peer check is off.
require_team_id_baked() {
  local app="$1" daemon output
  daemon="${app}/Contents/Resources/daemon/${cli_name}"
  output="$("${daemon}" version --team-id 2>/dev/null || true)"
  [[ "${output}" == "$(release_team_id)" ]] ||
    die "the bundled daemon was not built with the release Team ID and signature checking (got '${output:-none}', expected $(release_team_id)): ${daemon}"
}

# require_helper_identities <app>: die unless every bundled helper is signed by
# the release Team ID with the hardened runtime. The app-level check above does
# not prove this: nested binaries can be ad hoc or signed by another team. A SHA
# pin would add nothing here (the helpers are rebuilt every release, so a pin
# would only restate what the build just produced; the runtime check is the
# Team ID requirement in frontend/src/main/helper-signature.ts).
# release_team_id: the Developer ID Team ID releases must be signed by. From
# TALLPINE_RELEASE_TEAM_ID, else the "(TEAMID)" suffix of the signing identity
# (both settable in the untracked ~/.tallpine/release.env). Nothing is hardcoded.
release_team_id() {
  local id
  if [[ -n "${TALLPINE_RELEASE_TEAM_ID:-}" ]]; then printf '%s\n' "${TALLPINE_RELEASE_TEAM_ID}"; return 0; fi
  id="$(sign_identity)"
  if [[ "${id}" =~ \(([A-Z0-9]{10})\)[[:space:]]*$ ]]; then printf '%s\n' "${BASH_REMATCH[1]}"; return 0; fi
  die "release Team ID unknown: set TALLPINE_RELEASE_TEAM_ID (or a signing identity ending in '(TEAMID)') in the environment or ~/.tallpine/release.env"
}
require_helper_identities() {
  local app="$1" res rel helper team details release_team
  release_team="$(release_team_id)" || exit 1
  res="${app}/Contents/Resources"
  while IFS= read -r rel; do
    helper="${res}/${rel}"
    [[ -e "${helper}" ]] || die "bundled helper missing from the app: ${helper}"
    details="$(codesign_cmd -dv "${helper}" 2>&1 || true)"
    team="$(printf '%s\n' "${details}" | sed -n 's/^TeamIdentifier=//p' | head -n 1)"
    [[ "${team}" == "${release_team}" ]] ||
      die "bundled helper is unsigned or signed by another team (TeamIdentifier='${team:-none}', expected ${release_team}): ${helper}"
    require_hardened_runtime "${helper}"
  done < <(grep -v '^[[:space:]]*\(#\|$\)' "${script_dir}/bundled-helpers.txt")
}


# notarize_artifact <file>: submit a signed .pkg/.dmg to the notary service with
# the keychain profile, wait for the verdict, then staple the ticket. Dies when
# Apple does not accept it.
notarize_artifact() {
  local file="$1" profile out
  profile="$(notary_profile)"
  [[ -n "${profile}" ]] || die "no TALLPINE_NOTARY_PROFILE set (environment or ~/.tallpine/release.env)"
  out="$("${TALLPINE_RELEASE_XCRUN:-xcrun}" notarytool submit "${file}" --keychain-profile "${profile}" --wait 2>&1)" ||
    die "notarization of ${file} failed: ${out}"
  [[ "${out}" == *"status: Accepted"* ]] || die "notarization of ${file} was not accepted: ${out}"
  "${TALLPINE_RELEASE_XCRUN:-xcrun}" stapler staple "${file}" >/dev/null 2>&1 ||
    die "stapling the notarization ticket to ${file} failed"
}
