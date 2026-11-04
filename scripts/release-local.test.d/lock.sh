#!/usr/bin/env bash
# Sourced by scripts/release-local.test.sh (shares its helpers): the install lock records its owner and a dead owner is taken over.
lk="${T}/lk"; mkdir -p "${lk}"
lock_probe() { # runs the real lock_releases in a subshell; prints the owner file then exits
  bash -c '
    die() { echo "die: $*" >&2; exit 1; }
    warn() { echo "warn: $*" >&2; }
    on_exit() { :; }
    releases_dir="$1"
    . "$2"
    lock_releases
    cat "${releases_dir}/.lock/owner"
  ' _ "${lk}" "${script_dir}/release-local.d/lock.sh"
}
sleep 300 & lk_live=$!
sleep 0 & lk_dead=$!; wait "${lk_dead}"
# A live owner blocks.
mkdir -p "${lk}/.lock"; printf '%s %s\n' "${lk_live}" "$(LC_ALL=C TZ=UTC ps -o lstart= -p "${lk_live}" | tr -s ' ')" >"${lk}/.lock/owner"
check "a live owner's install lock is refused" '! lock_probe >"${T}/out" 2>&1 && grep -q "holds" "${T}/out"'
# Same pid but a different start time (pid reuse) is stale.
printf '%s Thu Jan  1 00:00:00 1970\n' "${lk_live}" >"${lk}/.lock/owner"
check "a reused pid (start time differs) is taken over" 'lock_probe >"${T}/out" 2>&1 && grep -q "^[0-9][0-9]* " "${T}/out" && grep -q "taking it over" "${T}/out"'
rm -rf "${lk}/.lock"
# A dead owner is stale.
mkdir -p "${lk}/.lock"; printf '%s Thu Jan  1 00:00:00 1970\n' "${lk_dead}" >"${lk}/.lock/owner"
check "a dead owner's lock is taken over and re-owned" 'lock_probe >"${T}/out" 2>&1 && grep -q "taking it over" "${T}/out" && ! grep -q "^${lk_dead} " "${T}/out"'
rm -rf "${lk}/.lock"
# An empty fresh lock (owner about to be written) is not robbed; an old one is stale.
mkdir -p "${lk}/.lock"
check "a fresh ownerless lock is refused" '! lock_probe >"${T}/out" 2>&1'
touch -t 200001010000 "${lk}/.lock"
check "an old ownerless lock (killed run) is taken over" 'lock_probe >"${T}/out" 2>&1'
kill "${lk_live}" 2>/dev/null; rm -rf "${lk}"
# A claim held by another takeover blocks; a locale/timezone change does not make a live owner look reused.
sleep 300 & lk_live=$!; mkdir -p "${lk}/.lock" "${lk}/.lock.takeover"
printf '%s %s\n' "${lk_live}" "$(LC_ALL=C TZ=UTC ps -o lstart= -p "${lk_live}" | tr -s ' ')" >"${lk}/.lock/owner"
rmdir "${lk}/.lock.takeover"
check "a live owner is refused under a different TZ/LANG" '! LANG=de_DE.UTF-8 TZ=Asia/Tokyo lock_probe >"${T}/out" 2>&1 && grep -q "holds" "${T}/out"'
mkdir "${lk}/.lock.takeover"
printf '%s Thu Jan  1 00:00:00 1970\n' "${lk_live}" >"${lk}/.lock/owner"
check "a stale lock is not taken over while another takeover holds the claim" '! lock_probe >"${T}/out" 2>&1 && grep -q "in progress" "${T}/out"'
kill "${lk_live}" 2>/dev/null; rm -rf "${lk}"
