# shellcheck shell=bash
# Sourced by scripts/release-local (never run directly): the .pkg published for
# MDM (Jamf policy) deployment. A component package that installs the notarized
# Tallpine.app into /Applications, with no pre/postinstall scripts: the app's
# first launch registers the daemon as it does for a dmg install. Publish only;
# `build` never makes a pkg.

pkg_installer_identity() { printf '%s\n' "${TALLPINE_INSTALLER_IDENTITY:-}"; }

# require_pkg_identity: die unless TALLPINE_INSTALLER_IDENTITY is set (checked
# before any upload step so a release is never half-published).
require_pkg_identity() {
  local identity
  identity="$(pkg_installer_identity)"
  [[ -n "${identity}" ]] ||
    die "refusing to publish: TALLPINE_INSTALLER_IDENTITY is unset (the name of the 'Developer ID Installer' certificate used to sign the .pkg)"
  "${TALLPINE_RELEASE_SECURITY:-security}" find-identity -v -p basic 2>/dev/null | grep -qF "${identity}" ||
    die "refusing to publish: installer identity '${identity}' not found in the keychain (security find-identity -v -p basic)"
}

# run_pkgbuild <pkgbuild> <args...>: run pkgbuild with stdout discarded. Recent macOS pkgbuild prints the
# bare line "write: Permission denied" to stderr several times per build even when it succeeds (it
# reproduces on a trivial one-file root, so it is not our payload or paths). Drop only those exact
# lines, and only when pkgbuild succeeded; on failure stderr is shown unchanged and the exit status
# is kept, so a real failure still dies with every diagnostic.
run_pkgbuild() {
  local errf rc=0
  errf="$(mktemp "${TMPDIR:-/tmp}/tallpine-pkgbuild-err.XXXXXX")" || return 1
  "$@" >/dev/null 2>"${errf}" || rc=$?
  if [[ "${rc}" -eq 0 ]]; then grep -vx 'write: Permission denied' "${errf}" >&2 || true; else cat "${errf}" >&2; fi
  rm -f "${errf}"
  return "${rc}"
}

# build_pkg <app> <version> <out.pkg>: pkgbuild, productsign, notarize+staple, verify.
build_pkg() {
  local app="$1" version="$2" out="$3" identity raw root plist pkgb="${TALLPINE_RELEASE_PKGBUILD:-pkgbuild}"
  identity="$(pkg_installer_identity)"
  [[ -n "${identity}" ]] || die "TALLPINE_INSTALLER_IDENTITY is unset"
  raw="${out%.pkg}.unsigned.pkg"
  # Non-relocatable: otherwise Installer updates any other copy of the bundle it finds on disk
  # (Downloads, a build tree) instead of installing to /Applications.
  root="$(mktemp -d "${TMPDIR:-/tmp}/tallpine-pkgroot.XXXXXX")"
  plist="${root}.plist"
  ditto "${app}" "${root}/$(basename "${app}")" || die "could not stage ${app}"
  run_pkgbuild "${pkgb}" --analyze --root "${root}" "${plist}" || die "pkgbuild --analyze failed for ${app}"
  plutil -replace 0.BundleIsRelocatable -bool NO "${plist}" || die "could not mark the bundle non-relocatable"
  run_pkgbuild "${pkgb}" --root "${root}" --component-plist "${plist}" --install-location /Applications \
    --identifier "${bundle_id}" --version "${version}" "${raw}" || die "pkgbuild failed for ${app}"
  rm -rf "${root}" "${plist}"
  "${TALLPINE_RELEASE_PRODUCTSIGN:-productsign}" --sign "${identity}" "${raw}" "${out}" >/dev/null ||
    die "productsign failed: is the certificate '${identity}' in the keychain?"
  rm -f "${raw}"
  notarize_artifact "${out}"
  verify_pkg "${out}"
}

# verify_pkg <pkg>: signed by a Developer ID Installer certificate and stapled.
verify_pkg() {
  local pkg="$1" out
  out="$("${TALLPINE_RELEASE_PKGUTIL:-pkgutil}" --check-signature "${pkg}" 2>&1)" ||
    die "pkgutil does not accept the signature of ${pkg}: ${out}"
  [[ "${out}" == *"Developer ID Installer"* ]] || die "${pkg} is not signed with a Developer ID Installer certificate"
  "${TALLPINE_RELEASE_XCRUN:-xcrun}" stapler validate "${pkg}" >/dev/null 2>&1 || die "${pkg} has no stapled notarization ticket"
}
