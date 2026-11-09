#!/usr/bin/env bash
# Sourced by scripts/release-local (never run directly): build, request-build, detached build, build notification.
# -------------------------------------------------------------------- build

build_env() {
  TALLPINE_BUILD_CACHE_DIR="${state_dir}/build-cache" env -u TALLPINE_SESSION_ID -u TALLPINE_DATA_DIR -u TALLPINE_RUN_FILE -u TALLPINE_PORT -u TALLPINE_TMUX_SOCKET_NAME -u TALLPINE_SESSION_TOKEN -u TALLPINE_SESSION_AUTH -u TALLPINE_SESSION_AUTH_RUN_FILE "$@"
}

# B187: refuse to run from a checkout whose own scripts/release-local(.d) is behind origin/main
# (the project-root checkout is never updated; an old copy once refused the current bundle id).
require_fresh_script() {
  [[ "${TALLPINE_RELEASE_SKIP_STALE_CHECK:-}" == 1 ]] && return 0
  # A never-updated checkout also has a stale origin/main ref: fetch first.
  if [[ "${opt_fetch}" -eq 1 ]]; then
    git -C "${repo_root}" fetch --quiet origin 2>/dev/null || warn "fetch failed; checking staleness against the local origin/main"
  fi
  git -C "${repo_root}" rev-parse --verify --quiet "origin/main:scripts/release-local" >/dev/null 2>&1 || return 0
  # The command is the dispatcher plus scripts/release-local.d: stale if either differs from origin/main.
  git -C "${repo_root}" diff --quiet origin/main -- scripts/release-local scripts/release-local.d 2>/dev/null && return 0
  # Same script content is fine; differing content only counts as stale when
  # origin/main's version is strictly newer than this checkout's HEAD.
  if git -C "${repo_root}" merge-base --is-ancestor "origin/main" HEAD 2>/dev/null; then return 0; fi
  die "this checkout (${repo_root}) has a scripts/release-local that differs from origin/main and is behind it. Run it from a fresh worktree instead: git fetch origin && git worktree add \"\${TMPDIR:-/tmp}/rl-main\" origin/main && \"\${TMPDIR:-/tmp}/rl-main/scripts/release-local\" $*"
}

# Step timing: step_begin / step_end <name> print one short line and remember it.
build_times=""
step_t0=0
step_begin() { step_t0="${SECONDS}"; }
step_end() {
  local d="$((SECONDS - step_t0))"
  build_times="${build_times}${build_times:+ }$1=${d}s"
  say "  $1: ${d}s"
  return 0
}

# record_build_times <sha> <total seconds> <notarized|"">: one stdout line, one
# capped TSV line (last 200 kept); also trims old install logs to the newest 20.
record_build_times() {
  local tsv="${releases_dir}/build-times.tsv"
  say "Build ${1:0:12} ${3:-not-notarized}: total $2s (${build_times})"
  mkdir -p "${releases_dir}"
  printf '%s\t%s\t%s\ttotal=%ss\t%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "${1:0:12}" "${3:-no-notarize}" "$2" "${build_times}" >>"${tsv}"
  if [[ "$(wc -l <"${tsv}")" -gt 200 ]]; then
    tail -n 200 "${tsv}" >"${tsv}.tmp" && mv "${tsv}.tmp" "${tsv}"
  fi
  # shellcheck disable=SC2012
  ls -1t "${releases_dir}"/install-*.log 2>/dev/null | tail -n +21 | while IFS= read -r f; do rm -f "${f}"; done
  return 0
}

# package_timing_filter <log> <phases file>: appends stdin to <log> unchanged and writes
# "<name> <seconds>" per phase to <phases file>. A phase runs from its marker line to the next marker.
# Markers: npm script headers ("> pkg@1 name") and electron-forge's "❯ ..." lines. Pure observation.
package_timing_filter() {
  python3 -I -c 'import re,sys,time
log,out=sys.argv[1],sys.argv[2]
forge=[(r"^❯ Running prePackage hook$","prepackage"),(r"^❯ \[plugin-vite\]","vite-renderer-main"),
       (r"^❯ Packaging application","forge-package"),(r"^❯ Finalizing package","finalize-codesign"),
       (r"^❯ Running postPackage hook$","postpackage"),(r"^❯ Making|^❯ Notariz|^❯ Waiting for notar","notarize")]
cur,t0,res=None,time.monotonic(),[]
def mark(n):
    global cur,t0
    now=time.monotonic()
    if cur: res.append((cur,now-t0))
    cur,t0=n,now
with open(log,"ab") as f:
    for line in sys.stdin.buffer:
        f.write(line); f.flush()
        t=line.decode("utf-8","replace").rstrip()
        m=re.match(r"^> \S+@\S+ (\S+)$",t)
        if m: mark("npm:"+m.group(1)); continue
        for rx,n in forge:
            if re.search(rx,t): mark(n); break
mark(None)
with open(out,"w") as f:
    for n,d in res: f.write("%s %d\n"%(n,round(d)))
' "$1" "$2"
}

# record_package_phases <phases file>: logs the phases and adds them to build_times (name=Ns).
record_package_phases() {
  local f="$1" n d out=""
  [[ -s "${f}" ]] || return 0
  while read -r n d; do
    [[ "${d}" -ge 1 ]] || continue
    out="${out}${out:+ }${n}=${d}s"
    build_times="${build_times}${build_times:+ }package.${n}=${d}s"
  done <"${f}"
  say "  package phases: ${out:-all under 1s}"
  return 0
}

# install_deps <tree> <subdir> <log>: npm ci from the shared npm cache (--prefer-offline).
install_deps() {
  local d="$1/$2" log="$3"
  [[ -f "${d}/package-lock.json" ]] || { [[ "$2" == packages/product-ui ]] && return 0; die "no package-lock.json in ${d}"; }
  (cd "${d}" && build_env nice -n 10 npm ci --prefer-offline --no-audit --no-fund) >>"${log}" 2>&1 ||
    { tail -n 20 "${log}" >&2; die "npm ci failed in ${d} (full log: ${log})"; }
}

# notify_build <log> <sha> <rc>: one line to the orchestrator, like loop.sh's build_on_green.
# Target: ~/.tallpine/local-ci/notify-target if that session is live, else the oldest live orchestrator.
notify_build() {
  local log="$1" sha="$2" rc="$3" app msg cli
  if [[ "${rc}" -eq 0 ]]; then
    app="$(grep '^Built: ' "${log}" | tail -n 1 | cut -c8-)"
    msg="[local-ci] build ${sha:0:10} OK: ${app:-unknown}. Log: ${log}"
  else
    msg="[local-ci] build ${sha:0:10} FAILED (rc ${rc}). Log: ${log}"
  fi
  cli="${TALLPINE_RELEASE_NOTIFY_CLI:-}"
  if [[ -z "${cli}" ]]; then
    for cli in "${HOME}/Applications/${product_name}.app/Contents/Resources/daemon/${cli_name}" "$(command -v "${cli_name}" || true)"; do
      [[ -x "${cli}" ]] && break
      cli=""
    done
  fi
  [[ -n "${cli}" ]] || { say "notify: no CLI found"; return 0; }
  local target
  target="$(head -n 1 "$(dirname "${ci_tsv}")/../notify-target" 2>/dev/null | tr -d '[:space:]')"
  say "$(tp_notify_orchestrator "${cli}" agent-orchestrator "${target}" "${msg}")"
}


# build_exists <sha>: a build-times row (written only by a finished build) says it was built. A leftover tree from a failed build does not count.
build_exists() {
  grep -q "$(printf '\t%s\t' "${1:0:12}")" "${releases_dir}/build-times.tsv" 2>/dev/null
}

# build_detached <sha>: builds in its own session (never installs), then notifies. Log next to local-ci.tsv.
build_detached() {
  local sha="$1" log self
  log="$(dirname "${ci_tsv}")/build-${sha:0:8}.log"
  self="${TALLPINE_RELEASE_BUILD_BIN:-${release_self}}"
  # B441: the child opens its log after detaching, so an unwritable log dir (a sandboxed caller) would fail
  # silently while this reports success. Open it here first, where the failure can be loud and non-zero.
  { mkdir -p "$(dirname "${log}")" && : >"${log}"; } 2>/dev/null ||
    die "cannot write the build log ${log} (is this shell sandboxed? use 'tallpine release request-build' so the daemon builds)"
  say "Building ${sha:0:12} now, detached (log ${log})"
  local defs child
  defs="$(declare -f notify_build say tp_notify_orchestrator)"
  child='"$3" --state-dir "$4" build "$2" --no-fetch </dev/null; rc=$?
notify_build "$1" "$2" "$rc"'
  env -i HOME="${HOME}" PATH="${PATH}" USER="${USER:-}" LOGNAME="${LOGNAME:-${USER:-}}" TMPDIR="${TMPDIR:-/tmp}" \
    TALLPINE_RELEASE_CI_TSV="${ci_tsv}" TALLPINE_RELEASE_NOTIFY_CLI="${TALLPINE_RELEASE_NOTIFY_CLI:-}" \
    TALLPINE_RELEASE_SKIP_STALE_CHECK="${TALLPINE_RELEASE_SKIP_STALE_CHECK:-}" \
    product_name="${product_name}" cli_name="${cli_name}" ci_tsv="${ci_tsv}" \
    python3 -I -c 'import os,sys
os.setsid()
log=os.open(sys.argv[1],os.O_WRONLY|os.O_CREAT|os.O_TRUNC,0o644)
os.dup2(log,1); os.dup2(log,2)
os.execv("/bin/bash",["bash","-c",sys.argv[2],"bash",sys.argv[1],sys.argv[3],sys.argv[4],sys.argv[5]])' \
    "${log}" "${defs}"$'\n'"${child}" "${sha}" "${self}" "${state_dir}" </dev/null >/dev/null 2>&1 &
}

# request-build: ask for a build of the next main commit that passes local CI. If origin/main's head
# already passed and has no build yet, build it right away; otherwise leave the marker for loop.sh.
cmd_request_build() {
  local sha
  mkdir -p "${builds_dir}"
  if [[ "${opt_fetch}" -eq 1 ]]; then
    git -C "${repo_root}" fetch --quiet origin main 2>/dev/null || warn "could not fetch origin main; using the local origin/main"
  fi
  sha="$(git -C "${repo_root}" rev-parse --verify --quiet "origin/main^{commit}" 2>/dev/null || true)"
  if [[ -n "${sha}" && "$(ci_row "${sha}")" == pass$'\t'* ]]; then
    if build_exists "${sha}"; then
      say "origin/main ${sha:0:12} passed local CI and is already built; nothing to do (the next passing main commit is built via the marker)"
    elif build_locked "${sha}"; then
      say "origin/main ${sha:0:12} is already being built"
      return 0
    else
      build_detached "${sha}" # dies first when it cannot start, keeping an earlier request
      rm -f "${builds_dir}/request-build"
      return 0
    fi
  fi
  printf '%s\n' "${sha}" >"${builds_dir}/request-build" # main's head at request time (B341): loop.sh only builds a descendant
  say "Requested: the local CI loop builds the next passing main commit (it never installs)"
}

cmd_build() {
  local ref="${1:-origin/main}" sha dir app commit version identity notary dmg log sub t_total0
  # Fail fast: refusing to build unsigned must not wait for npm ci.
  signing_setup
  notary_setup
  if [[ "${opt_fetch}" -eq 1 ]]; then
    say "Fetching origin..."
    git -C "${repo_root}" fetch --quiet origin
  fi
  sha="$(git -C "${repo_root}" rev-parse --verify --quiet "${ref}^{commit}")" || die "unknown ref ${ref}"
  say "Building ${ref} at ${sha}"
  require_ci_pass "${sha}"

  acquire_build_lock "${sha}"
  dir="${builds_dir}/${sha}"
  if [[ -e "${dir}" ]]; then
    say "Removing the previous build tree ${dir}"
    git -C "${repo_root}" worktree remove --force "${dir}" 2>/dev/null || rm -rf "${dir}"
    git -C "${repo_root}" worktree prune
  fi
  mkdir -p "${builds_dir}"
  git -C "${repo_root}" worktree add --quiet --detach "${dir}" "${sha}"

  version="$(sed -n 's/^[[:space:]]*"version":[[:space:]]*"\([^"]*\)".*/\1/p' "${dir}/frontend/package.json" | head -n 1)"
  # Non-notarized builds are local: a dev suffix plus VOBuildKind=local keep the updater off.
  if [[ -n "${notary}" ]]; then unset TALLPINE_BUILD_KIND; else export TALLPINE_BUILD_KIND=local; version="${version:-0.0.0}-dev.${sha:0:8}"; fi
  export TALLPINE_BUILD_VERSION="${version:-dev}" TALLPINE_BUILD_COMMIT="${sha}" TALLPINE_BUILD_DIRTY=false
  TALLPINE_BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  export TALLPINE_BUILD_DATE
  # The repo's pinned Go toolchain, when this commit has it.
  [[ -x "${dir}/scripts/bin/go" ]] && export PATH="${dir}/scripts/bin:${PATH}"

  # Verbose npm/electron output goes to a log inside the build tree (pruned with it).
  log="${dir}/release-build.log"
  : >"${log}"
  say "Build log: ${log}"
  t_total0="${SECONDS}"
  step_begin
  for sub in . packages/product-ui frontend; do
    install_deps "${dir}" "${sub}" "${log}"
  done
  step_end deps
  step_begin
  # The output still goes to the log unchanged; the filter only notes when each npm script and
  # electron-forge phase started, so the log and build-times.tsv show where "package" spends its time.
  (set -o pipefail; cd "${dir}/frontend" && build_env nice -n 10 npm run package 2>&1 | package_timing_filter "${log}" "${log}.phases") ||
    { tail -n 20 "${log}" >&2; die "packaging failed (full log: ${log})"; }
  step_end "package${notary:+(incl. notarize)}"
  record_package_phases "${log}.phases"

  if [[ -n "$(git -C "${dir}" status --porcelain --untracked-files=no)" ]]; then
    git -C "${dir}" status --short --untracked-files=no >&2
    # The bundle is stamped clean, so it must match the commit.
    [[ "${opt_force}" -eq 1 ]] || die "the build modified tracked files in ${dir}, so the app is not exactly ${sha}; pass --force to keep it"
    warn "the build modified tracked files in ${dir}"
  fi
  app="$(find "${dir}/frontend/out" -maxdepth 2 -type d -name "${product_name}.app" 2>/dev/null | head -n 1)"
  [[ -n "${app}" ]] || die "no ${product_name}.app under ${dir}/frontend/out"
  step_begin
  if [[ -n "${identity}" ]]; then
    verify_signature "${app}" || die "the app signed with '${identity}' does not verify with a stable requirement (codesign --verify --deep --strict, codesign -d -r-): ${app}"
    require_hardened_runtime "${app}"
    require_helper_identities "${app}"
    say "Signature verified: stable requirement for ${bundle_id}, hardened runtime"
  fi
  step_end verify
  if [[ -n "${notary}" ]]; then
    step_begin
    dmg="$(find "${dir}/frontend/out" -name '*.dmg' 2>/dev/null | head -n 1)"
    require_notarized "${app}" "${dmg}"
    say "Notarization verified: stapled, accepted as Notarized Developer ID"
    step_end notary-verify
  fi
  commit="$(app_commit "${app}")"
  if [[ -z "${commit}" ]]; then
    warn "the built app carries no build commit (this commit predates build stamping)"
  elif [[ "${commit}" != "${sha}" ]]; then
    die "the built app reports commit ${commit}, expected ${sha}"
  fi

  # Older build trees; the newest ${keep} stay.
  local old
  old="$(ls -1td "${builds_dir}"/*/ 2>/dev/null | tail -n +"$((keep + 1))" || true)"
  printf '%s\n' "${old}" | while IFS= read -r path; do
    [[ -n "${path}" ]] || continue
    say "Pruning build tree ${path%/}"
    git -C "${repo_root}" worktree remove --force "${path%/}" 2>/dev/null || rm -rf "${path%/}"
  done
  git -C "${repo_root}" worktree prune

  record_build_times "${sha}" "$((SECONDS - t_total0))" "${notary:+notarized}"
  say ""
  say "Built: ${app}"
  say "Commit: ${sha}"
  say "Install with: scripts/release-local install \"${app}\""
}

