#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"
backend_dir="${repo_root}/backend"
build_dir="${HOME}/.tallpine/build/bin"

can_write_dir() {
  local dir="$1"

  [[ -n "${dir}" ]] || return 1
  mkdir -p "${dir}"
  [[ -d "${dir}" && -w "${dir}" ]]
}

resolve_ao() {
  local resolved

  resolved="$(command -v tallpine || true)"
  if [[ -z "${resolved}" && -n "${goexe:-}" ]]; then
    resolved="$(command -v "tallpine${goexe}" || true)"
  fi

  printf '%s\n' "${resolved}"
}

absolute_path() {
  local path="$1"

  printf '%s/%s\n' "$(cd "$(dirname "${path}")" && pwd -P)" "$(basename "${path}")"
}

install_file() {
  local source_path="$1"
  local target_path="$2"

  if ln -sfn "${source_path}" "${target_path}" 2>/dev/null; then
    printf 'Linked %s\n' "${target_path}"
  else
    rm -f "${target_path}"
    cp "${source_path}" "${target_path}"
    chmod +x "${target_path}"
    printf 'Installed %s\n' "${target_path}"
  fi
}

select_install_dir() {
  local gopath
  local existing_path
  local dir
  local candidate
  local -a path_entries
  gopath="$(go env GOPATH)"
  existing_path="$(resolve_ao)"

  if [[ -n "${existing_path}" && "${existing_path}" = /* ]] && can_write_dir "$(dirname "${existing_path}")"; then
    dirname "${existing_path}"
    return 0
  fi

  local candidates=(
    "${gopath}/bin"
    "/usr/local/bin"
    "/opt/homebrew/bin"
    "${HOME}/.local/bin"
  )

  IFS=':' read -r -a path_entries < <(printf '%s\n' "${PATH:-}")
  for dir in "${path_entries[@]}"; do
    for candidate in "${candidates[@]}"; do
      if [[ "${dir}" == "${candidate}" ]] && can_write_dir "${dir}"; then
        printf '%s\n' "${dir}"
        return 0
      fi
    done
  done

  for dir in "${path_entries[@]}"; do
    if [[ "${dir}" = /* ]] && can_write_dir "${dir}"; then
      printf '%s\n' "${dir}"
      return 0
    fi
  done

  return 1
}

command -v go >/dev/null
goexe="$(go env GOEXE)"
binary_name="tallpine${goexe}"
binary_path="${build_dir}/${binary_name}"

# Stamp the same build metadata as frontend/scripts/build-info.mjs; TALLPINE_BUILD_*
# pins a value.
buildinfo_pkg="tallpine.eu/tallpine/backend/internal/buildinfo"
build_version="${TALLPINE_BUILD_VERSION:-$(sed -n 's/^[[:space:]]*"version":[[:space:]]*"\([^"]*\)".*/\1/p' "${repo_root}/frontend/package.json" | head -n 1)}"
build_commit="${TALLPINE_BUILD_COMMIT-$(git -C "${repo_root}" rev-parse HEAD 2>/dev/null || true)}"
build_date="${TALLPINE_BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
build_dirty="${TALLPINE_BUILD_DIRTY:-}"
if [[ "${build_dirty}" != "true" && "${build_dirty}" != "false" ]]; then
  build_dirty=false
  if [[ -n "${build_commit}" && -n "$(git -C "${repo_root}" status --porcelain 2>/dev/null)" ]]; then
    build_dirty=true
  fi
fi
ldflags="-X ${buildinfo_pkg}.Version=${build_version:-dev} -X ${buildinfo_pkg}.Commit=${build_commit} -X ${buildinfo_pkg}.Date=${build_date} -X ${buildinfo_pkg}.Dirty=${build_dirty}"

mkdir -p "${build_dir}"
# Inject the expected Apple Team ID (read by `tallpine start` to verify downloaded
# releases) from the release signing identity "Name (TEAMID)" in the existing
# release configuration. If unset, the build refuses to install downloaded releases unless the user passes
# --allow-unverified-signer (dev only).
sign_identity="${TALLPINE_SIGN_IDENTITY:-${APPLE_SIGNING_IDENTITY:-}}"
team_id="$(printf '%s' "${sign_identity}" | sed -n 's/.*(\([A-Z0-9]\{10\}\))[[:space:]]*$/\1/p')"
if [[ -n "${TALLPINE_RELEASE_GITHUB_REPO:-}" ]]; then
  [[ "${TALLPINE_RELEASE_GITHUB_REPO}" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || { echo "daemon-build: TALLPINE_RELEASE_GITHUB_REPO must be a GitHub owner/repo slug" >&2; exit 1; }
  ldflags="${ldflags} -X tallpine.eu/tallpine/backend/internal/cli.releaseRepo=${TALLPINE_RELEASE_GITHUB_REPO}"
fi
if [[ -n "${team_id}" ]]; then
  ldflags="${ldflags} -X tallpine.eu/tallpine/backend/internal/releasesigner.ExpectedTeamID=${team_id}"
fi

(cd "${backend_dir}" && go build -ldflags "${ldflags}" -o "${binary_path}" ./cmd/tallpine)

if ! install_dir="$(select_install_dir)"; then
  printf 'Could not find a writable directory on PATH for tallpine\n' >&2
  exit 1
fi
install_path="${install_dir}/${binary_name}"
shim_path=""

install_file "${binary_path}" "${install_path}"

if [[ -n "${goexe}" ]]; then
  shim_path="${install_dir}/tallpine"
  printf '%s\n' \
    '#!/usr/bin/env bash' \
    'script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"' \
    'exec "${script_dir}/tallpine.exe" "$@"' > "${shim_path}"
  chmod +x "${shim_path}"
fi

resolved="$(resolve_ao)"
if [[ -z "${resolved}" ]]; then
  printf 'tallpine did not resolve on PATH after installing %s\n' "${install_path}" >&2
  exit 1
fi
resolved_path="$(absolute_path "${resolved}")"
install_abs_path="$(absolute_path "${install_path}")"
shim_abs_path=""
if [[ -n "${shim_path}" ]]; then
  shim_abs_path="$(absolute_path "${shim_path}")"
fi
if [[ "${resolved_path}" != "${install_abs_path}" && "${resolved_path}" != "${shim_abs_path}" ]]; then
  printf 'tallpine resolves to %s, expected %s\n' "${resolved}" "${install_path}" >&2
  exit 1
fi

printf 'Built %s\n' "${binary_path}"
