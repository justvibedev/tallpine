#!/usr/bin/env bash
# Sourced by scripts/release-local.test.sh (shares its state and helpers): build: stale-script guard, package timings, build lock takeover.
# B187: build refuses from a checkout whose scripts/release-local is behind origin/main.
unset TALLPINE_RELEASE_SKIP_STALE_CHECK
S="${T}/stale"; mkdir -p "${S}/up/scripts" "${S}/up/backend/internal/brand"
cp "${script_dir}/../backend/internal/brand/brand.go" "${S}/up/backend/internal/brand/"
cp "${release}" "${script_dir}/release-local-publish.sh" "${S}/up/scripts/"
cp -R "${script_dir}/release-local.d" "${script_dir}/lib" "${S}/up/scripts/"
git -C "${S}/up" init -q -b main && git -C "${S}/up" add -A
git -C "${S}/up" -c user.name=t -c user.email=t@t commit -qm old
git clone -q "${S}/up" "${S}/clone"
echo "# newer" >> "${S}/up/scripts/release-local"
git -C "${S}/up" -c user.name=t -c user.email=t@t commit -qam new
# No pre-fetch and no --no-fetch: the guard itself must fetch to see the newer origin/main.
if "${S}/clone/scripts/release-local" --allow-unsigned --state-dir "${S}/st" build >"${T}/out" 2>&1; then
  bad "stale checkout with an out-of-date origin/main ref is refused"
else
  check "stale checkout with an out-of-date origin/main ref is refused" 'grep -q "behind it" "${T}/out"'
fi
git -C "${S}/clone" fetch -q origin
if "${S}/clone/scripts/release-local" --no-fetch --allow-unsigned --state-dir "${S}/st" build >"${T}/out" 2>&1; then
  bad "stale checkout build is refused"
else
  check "stale checkout build is refused with the fresh-worktree command" 'grep -q "behind it" "${T}/out" && grep -q "git worktree add" "${T}/out"'
fi
if "${S}/clone/scripts/release-local" --no-fetch --state-dir "${S}/st" install "${T}/nonexistent.app" >"${T}/out" 2>&1; then
  bad "stale checkout install is refused"
else
  check "stale checkout install is refused, naming install" 'grep -q "behind it" "${T}/out" && grep -q "release-local\" install" "${T}/out"'
fi
git -C "${S}/clone" fetch -q origin
git -C "${S}/clone" -c user.name=t -c user.email=t@t merge -q --ff-only origin/main
"${S}/clone/scripts/release-local" --no-fetch --allow-unsigned --state-dir "${S}/st" build >"${T}/out" 2>&1 || true
check "up-to-date checkout passes the staleness guard" '! grep -q "behind it" "${T}/out"'

# The bypass variable skips the guard.
git -C "${S}/clone" reset -q --hard HEAD~1 2>/dev/null || true
TALLPINE_RELEASE_SKIP_STALE_CHECK=1 "${S}/clone/scripts/release-local" --no-fetch --state-dir "${S}/st" install "${T}/nonexistent.app" >"${T}/out" 2>&1 || true
check "TALLPINE_RELEASE_SKIP_STALE_CHECK=1 bypasses the guard" '! grep -q "behind it" "${T}/out"'
# A change only in scripts/release-local.d (not the dispatcher) is just as stale.
S2="${T}/stale2"; mkdir -p "${S2}/up/scripts" "${S2}/up/backend/internal/brand"
cp "${script_dir}/../backend/internal/brand/brand.go" "${S2}/up/backend/internal/brand/"
cp "${release}" "${script_dir}/release-local-publish.sh" "${S2}/up/scripts/"; cp -R "${script_dir}/release-local.d" "${script_dir}/lib" "${S2}/up/scripts/"
git -C "${S2}/up" init -q -b main && git -C "${S2}/up" add -A && git -C "${S2}/up" -c user.name=t -c user.email=t@t commit -qm old
git clone -q "${S2}/up" "${S2}/clone"
echo "# newer" >> "${S2}/up/scripts/release-local.d/common.sh"
git -C "${S2}/up" -c user.name=t -c user.email=t@t commit -qam new
git -C "${S2}/clone" fetch -q origin
"${S2}/clone/scripts/release-local" --no-fetch --allow-unsigned --state-dir "${S2}/st" build >"${T}/out" 2>&1 && rc=0 || rc=$?
check "a stale scripts/release-local.d alone is refused" '[[ ${rc} -ne 0 ]] && grep -q "behind it" "${T}/out"'
export TALLPINE_RELEASE_SKIP_STALE_CHECK=1

# request-build drops a marker in release-builds/; timings TSV is capped; install logs pruned.
"${release}" --state-dir "${T}/rb" request-build >"${T}/out" 2>&1
check "request-build writes the marker" '[[ -f "${T}/rb/release-builds/request-build" ]]'
# package sub-step timings: the filter keeps the log byte-identical and names the phases.
printf '%s\n' '> tallpine@1 build:daemon' 'go stuff' '> tallpine@1 package' '❯ Running prePackage hook' '❯ [plugin-vite] Building production Vite bundles' '❯ Packaging application' '❯ Finalizing package' '❯ Running postPackage hook' 'done' >"${T}/pk.in"
( "${release}" --state-dir "${T}/rb" package-timing-check "${T}/pk.log" "${T}/pk.ph" <"${T}/pk.in" >"${T}/out" 2>&1 )
check "package timing filter keeps the log unchanged" 'cmp -s "${T}/pk.in" "${T}/pk.log"'
check "package timing names every phase" '( for n in npm:build:daemon npm:package prepackage vite-renderer-main forge-package finalize-codesign postpackage; do grep -q "^${n} " "${T}/pk.ph" || exit 1; done )'
# request-build builds origin/main right away when it passed CI and has no build; the marker stays otherwise.
RB="${T}/rbrepo"; mkdir -p "${RB}/scripts" "${RB}/backend/internal/brand"
cp "${release}" "${script_dir}/release-local-publish.sh" "${RB}/scripts/"; cp -R "${script_dir}/release-local.d" "${script_dir}/lib" "${RB}/scripts/"; cp "${script_dir}/../backend/internal/brand/brand.go" "${RB}/backend/internal/brand/"
git init -q --bare "${T}/rborigin.git"
git -C "${RB}" init -q -b main; git -C "${RB}" -c user.email=t@t -c user.name=t add -A; git -C "${RB}" -c user.email=t@t -c user.name=t commit -qm init
git -C "${RB}" remote add origin "${T}/rborigin.git"; git -C "${RB}" push -q origin main; git -C "${RB}" fetch -q origin
RBSHA="$(git -C "${RB}" rev-parse origin/main)"
printf '#!/bin/bash\necho "$@" >"%s/fakebuild.args"\necho "Built: /x/app"\n' "${T}" >"${T}/fakebuild"; chmod +x "${T}/fakebuild"
export TALLPINE_RELEASE_BUILD_BIN="${T}/fakebuild" TALLPINE_RELEASE_NOTIFY_CLI=/nonexistent
rbq() { TALLPINE_RELEASE_CI_TSV="${T}/rb-ci.tsv" "${RB}/scripts/release-local" --state-dir "${T}/rbs" "$@"; }
mkdir -p "${T}/rbs"; printf 'main\t%s\tfail\tx\n' "${RBSHA}" >"${T}/rb-ci.tsv"
rbq request-build >"${T}/out" 2>&1
check "request-build with a failed CI row leaves the marker, builds nothing" '[[ -f "${T}/rbs/release-builds/request-build" && ! -e "${T}/fakebuild.args" ]]'
printf 'main\t%s\tpass\tok\n' "${RBSHA}" >"${T}/rb-ci.tsv"
rbq request-build >"${T}/out" 2>&1
for i in $(seq 1 50); do [[ -f "${T}/fakebuild.args" ]] && break; sleep 0.2; done
check "request-build builds a passed, unbuilt origin/main right away" 'grep -q "build ${RBSHA} --no-fetch" "${T}/fakebuild.args" && [[ ! -f "${T}/rbs/release-builds/request-build" ]]'
for i in $(seq 1 50); do [[ -d "${T}/rbs/release-builds/.building-${RBSHA}" ]] || break; sleep 0.2; done
mkdir -p "${T}/rbs/releases"; printf '2026-01-01T00:00:00Z\t%s\tno-notarize\ttotal=1s\t\n' "${RBSHA:0:12}" >"${T}/rbs/releases/build-times.tsv"
rm -f "${T}/fakebuild.args"; rbq request-build >"${T}/out" 2>&1
check "request-build does not rebuild an already built commit (marker written for the next one)" '[[ ! -e "${T}/fakebuild.args" && -f "${T}/rbs/release-builds/request-build" ]]'
# a leftover tree from a failed build is retried; a stale lock is taken over, a fresh one is respected
rm -f "${T}/rbs/releases/build-times.tsv" "${T}/fakebuild.args"; mkdir -p "${T}/rbs/release-builds/${RBSHA}"
rbq request-build >"${T}/out" 2>&1
for i in $(seq 1 50); do [[ -f "${T}/fakebuild.args" ]] && break; sleep 0.2; done
check "request-build retries when only a leftover build tree exists" '[[ -f "${T}/fakebuild.args" ]]'
for i in $(seq 1 50); do [[ -d "${T}/rbs/release-builds/.building-${RBSHA}" ]] || break; sleep 0.2; done
rm -f "${T}/fakebuild.args"; mkdir -p "${T}/rbs/release-builds/.building-${RBSHA}"
rbq request-build >"${T}/out" 2>&1; sleep 0.3 # grace: a wrongly started detached build would write fakebuild.args within this
check "a fresh build lock stops request-build from starting another build" '[[ ! -e "${T}/fakebuild.args" ]]'
rbq --allow-unsigned build "${RBSHA}" --no-fetch >"${T}/out" 2>&1 && bad_lock=1 || bad_lock=0
check "cmd_build itself refuses while the lock is fresh" '[[ "${bad_lock}" -eq 0 ]] && grep -q "already being built" "${T}/out"'
touch -t 202001010000 "${T}/rbs/release-builds/.building-${RBSHA}"
rbq request-build >"${T}/out" 2>&1
for i in $(seq 1 50); do [[ -f "${T}/fakebuild.args" ]] && break; sleep 0.2; done
check "a stale build lock does not block request-build" '[[ -f "${T}/fakebuild.args" ]]'
unset TALLPINE_RELEASE_BUILD_BIN TALLPINE_RELEASE_NOTIFY_CLI
# Stale-lock takeover (bug 2): a takeover already claimed blocks, concurrent takeovers pick exactly one winner, and an EXIT trap set earlier still runs.
BLS="${T}/bl"; BLB="${BLS}/release-builds"; BLSHA="abcdef1234567890"; mkdir -p "${BLB}"
bl() { "${release}" --state-dir "${BLS}" "$@"; }
mkdir -p "${BLB}/.building-${BLSHA}"; touch -t 202001010000 "${BLB}/.building-${BLSHA}"; mkdir "${BLB}/.takeover-${BLSHA}"
bl build-lock-check "${BLSHA}" >"${T}/out" 2>&1 && bl_claimed=1 || bl_claimed=0
check "a stale lock is not taken over while another takeover holds the claim" '[[ "${bl_claimed}" -eq 0 ]] && grep -q "takeover already in progress" "${T}/out" && [[ -d "${BLB}/.takeover-${BLSHA}" ]]'
rm -rf "${BLB}/.takeover-${BLSHA}" "${BLB}/.building-${BLSHA}"
mkdir -p "${BLB}/.building-${BLSHA}"; touch -t 202001010000 "${BLB}/.building-${BLSHA}"
rm -f "${T}/bl-winners"; : >"${T}/bl-winners"
for i in 1 2 3 4 5 6; do ( bl build-lock-check "${BLSHA}" 2 >"${T}/bl-out-$i" 2>&1 && echo won >>"${T}/bl-winners" ) & done; wait
check "concurrent takeovers of one stale lock produce exactly one winner" '[[ "$(grep -c won "${T}/bl-winners")" -eq 1 ]]'
rm -rf "${BLB}/.building-${BLSHA}" "${BLB}/.takeover-${BLSHA}"
mkdir -p "${BLB}/.building-${BLSHA}"; touch -t 202001010000 "${BLB}/.building-${BLSHA}"
mkdir "${BLB}/.takeover-${BLSHA}"; touch -t 202001010000 "${BLB}/.takeover-${BLSHA}"
bl build-lock-check "${BLSHA}" >"${T}/out" 2>&1 || true
check "a takeover claim left by a killed builder does not block later builds forever" '[[ ! -e "${BLB}/.takeover-${BLSHA}" ]] && grep -q locked "${T}/out"'
rm -rf "${BLB}/.building-${BLSHA}" "${BLB}/.takeover-${BLSHA}"

mkdir -p "${T}/rt"; seq 1 250 >"${T}/rt/build-times.tsv"
for i in $(seq 1 25); do : >"${T}/rt/install-$i.log"; touch -t "2026010100$(printf %02d $i)" "${T}/rt/install-$i.log"; done
"${release}" --state-dir "${T}/rb" record-times-check "${T}/rt" >"${T}/out" 2>&1
check "build-times.tsv keeps the last 200 lines" '[[ "$(wc -l <"${T}/rt/build-times.tsv")" -eq 200 ]] && tail -n 1 "${T}/rt/build-times.tsv" | grep -q abcdef123456'
check "install logs pruned to the newest 20" '[[ "$(ls "${T}"/rt/install-*.log | wc -l | tr -d " ")" -eq 20 ]]'
check "summary is one stdout line" '[[ "$(wc -l <"${T}/out")" -eq 1 ]]'
nst="${T}/nst"; napps="${T}/napps"; mkdir -p "${nst}" "${napps}"
nrl() { "${release}" --state-dir "${nst}" --app-dir "${napps}" --no-fetch --yes --force "$@"; }
for c in "${A}" "${C}" "${D}"; do make_app "${T}/nb-${c:0:1}" "${c}" "${nst}"; done
cp -R "${T}/nb-a/${product}.app" "${napps}/"; "${T}/launch" "${napps}/${product}.app"
for i in $(seq 1 50); do [[ -f "${nst}/running.json" ]] && break; sleep 0.2; done
