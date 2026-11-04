# shellcheck shell=bash
# Sourced by scripts/release-local: the `publish` command. Publishes a built,
# notarized app as a GitHub Release on the public repo the desktop updater reads
# (frontend/src/main/update-feed.ts). Without --publish-github it only checks
# the guards and prints what it would upload.
#
# Test-only:
#   TALLPINE_RELEASE_REPO     owner/repo to publish to (default justvibedev/tallpine)
#   TALLPINE_RELEASE_GH       gh command (default gh)
#   TALLPINE_RELEASE_XCRUN    xcrun command (default xcrun; shared with build)
#   TALLPINE_RELEASE_PKGBUILD / _PRODUCTSIGN / _PKGUTIL / _SECURITY   pkg tool commands

# Must match frontend/src/main/update-feed.ts; the override is for a test repo.
publish_repo="${TALLPINE_RELEASE_REPO:-justvibedev/tallpine}"

gh_cmd() { ${TALLPINE_RELEASE_GH:-gh} "$@"; }
stapler_cmd() { "${TALLPINE_RELEASE_XCRUN:-xcrun}" stapler "$@"; }

# publish_tag_free <tag>: die when the tag or a release for it already exists.
publish_tag_free() {
  local tag="$1" out
  if out="$(gh_cmd release view "${tag}" -R "${publish_repo}" 2>&1)"; then
    die "release ${tag} already exists on ${publish_repo}; bump the version in frontend/package.json"
  fi
  [[ "${out}" == *"release not found"* || "${out}" == *"Not Found"* ]] ||
    die "could not check ${publish_repo} for ${tag} (gh: ${out}); is gh signed in?"
  if out="$(gh_cmd api "repos/${publish_repo}/git/ref/tags/${tag}" 2>&1)"; then
    die "tag ${tag} already exists on ${publish_repo}"
  fi
  [[ "${out}" == *"Not Found"* || "${out}" == *"404"* ]] ||
    die "could not check ${publish_repo} for tag ${tag} (gh: ${out})"
}

cmd_publish() {
  local app="${1:-}" version tag team frontend assets zip dmg prerelease=()
  [[ -n "${app}" && -d "${app}" ]] || die "usage: release-local publish <app> [--publish-github]"
  app="$(cd "${app}" && pwd -P)"
  [[ "$(plist_get "${app}" CFBundleIdentifier)" == "${bundle_id}" ]] || die "${app} is not a ${product_name} build (bundle id)"
  version="$(plist_get "${app}" CFBundleShortVersionString)"
  [[ "${version}" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] || die "version '${version}' is not a release version"
  app_dirty "${app}" && die "the app was built from a dirty tree; only clean builds are published"
  [[ "$(plist_get "${app}" VOBuildDirty)" == "false" ]] || die "the app carries no clean build stamp (VOBuildDirty)"
  tag="v${version}"

  # Notarization happens at build time only when a notary profile is set; a
  # build without it is not something Gatekeeper or the updater should get.
  [[ -n "${TALLPINE_NOTARY_PROFILE:-}" ]] ||
    die "refusing to publish: TALLPINE_NOTARY_PROFILE is unset, so this build was not notarized (build again with --notarize and it set)"
  require_pkg_identity
  verify_signature "${app}" || die "refusing to publish: ${app} is not signed with a stable Developer ID requirement"
  team="$(codesign_cmd -dv "${app}" 2>&1 | sed -n 's/^TeamIdentifier=//p' | head -n 1)"
  publish_team_id="$(release_team_id)" || exit 1
  [[ "${team}" == "${publish_team_id}" ]] || die "refusing to publish: signed by team '${team}', expected ${publish_team_id}"
  require_hardened_runtime "${app}"
  stapler_cmd validate "${app}" >/dev/null 2>&1 || die "refusing to publish: ${app} has no stapled notarization ticket"
  publish_tag_free "${tag}"
  [[ "${version}" == *-* ]] && prerelease=(--prerelease)

  if [[ "${opt_publish_github}" -ne 1 ]]; then
    say "Guards passed for ${tag} (${app})."
    say "Dry run: nothing was built or uploaded. Pass --publish-github to make the zip and dmg,"
    say "generate latest-mac.yml and the blockmap, and create release ${tag} on ${publish_repo}."
    return 0
  fi

  # <tree>/frontend/out/<name>-darwin-<arch>/<Product>.app
  frontend="$(cd "${app}/../../.." && pwd -P)"
  [[ -f "${frontend}/package.json" && -f "${frontend}/scripts/feed.mjs" ]] || die "${app} is not inside a build tree's frontend/out"
  say "Making the zip and dmg from ${app}..."
  (cd "${frontend}" && build_env nice -n 10 npm run make -- --skip-package)
  zip="$(find "${frontend}/out/make" -name "*${version}*.zip" -path '*darwin*' | head -n 1)"
  dmg="$(find "${frontend}/out/make" -name "*.dmg" | head -n 1)"
  [[ -n "${zip}" && -n "${dmg}" ]] || die "make produced no zip/dmg under ${frontend}/out/make"
  assets="$(mktemp -d "${TMPDIR:-/tmp}/tallpine-publish.XXXXXX")"
  cp "${zip}" "${dmg}" "${assets}/"
  (cd "${frontend}" && node scripts/feed.mjs "${assets}" "${version}")
  # Admin assets for Jamf: the PPPC/login-items profile from this signed build and the custom schema.
  [[ -f "${frontend}/src/shared/jamf-schema.json" ]] || die "missing ${frontend}/src/shared/jamf-schema.json"
  cp "${frontend}/src/shared/jamf-schema.json" "${assets}/tallpine-jamf-schema.json"
  opt_out="${assets}/${product_name}-PPPC.mobileconfig" cmd_pppc "${app}"
  say "Building the signed, notarized .pkg..."
  build_pkg "${app}" "${version}" "${assets}/${product_name}-${version}.pkg"
  say "Creating release ${tag} on ${publish_repo}:"
  ls -1 "${assets}"
  gh_cmd release create "${tag}" -R "${publish_repo}" --title "${product_name} ${version}" --generate-notes ${prerelease[@]+"${prerelease[@]}"} "${assets}"/*
  say "Published ${tag}."
}
