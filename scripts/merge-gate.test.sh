#!/usr/bin/env bash
# Tests scripts/merge-gate against a local bare "origin" with a stubbed gh.
set -uo pipefail
here="$(cd "$(dirname "$0")/.." && pwd)"
T="$(mktemp -d "${TMPDIR:-/tmp}/merge-gate-test.XXXXXX")"; trap 'rm -rf "$T"' EXIT
export TALLPINE_DATA_DIR="$T/data" TALLPINE_TEST_SLOTS=0 GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@t GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@t
fails=0
check() { if [ "$2" = "$3" ] || { [ "${4:-}" = re ] && [[ "$3" =~ $2 ]]; }; then echo "ok   $1"; else echo "FAIL $1: want [$2] got [$3]"; fails=$((fails + 1)); fi; }

git init -q --bare "$T/origin.git"
git clone -q "$T/origin.git" "$T/w" 2>/dev/null; cd "$T/w"
git checkout -q -b main; mkdir scripts
cp "$here/scripts/size-ratchet" "$here/scripts/size-ratchet.allow" scripts/ 2>/dev/null
cp "$here/scripts/test-slot" scripts/
echo one >a.txt; git add -A; git commit -q -m base; git push -q origin main
mkdir -p "$T/bin"
cat >"$T/bin/gh" <<'GH'
#!/bin/sh
case "$*" in
*"--json reviews"*) echo "$GH_APPROVED" ;;
*"--json title"*) echo "my change" ;;
*"--json mergeCommit"*) echo "abc123" ;;
*"pr merge"*) echo "$*" >>"$GH_LOG" ;;
esac
GH
chmod +x "$T/bin/gh"; export PATH="$T/bin:$PATH" GH_LOG="$T/gh.log"
# MERGE_GATE_VERIFY stands in for scripts/verify: fails (with a verify-style summary) for bad.txt PRs.
export MERGE_GATE_VERIFY='cat >/dev/null; echo ran-verify; if [ -f bad.txt ]; then printf "  early FAIL\nverify --changed summary (logs: x)\n  go-vet 1s ok\n  go-lint  3s  FAIL\nFAIL: go-lint\n"; false; fi'
mkpr() { git checkout -q -B "pr$1" origin/main; echo "$2" >"$3"; git add -A; git commit -q -m "pr $1"; git push -q origin "HEAD:refs/pull/$1/head"; git rev-parse HEAD; }
gate() { (cd "$T/w" && bash "$here/scripts/merge-gate" "$@" 2>/dev/null); }
cp "$here/scripts/merge-gate" scripts/ 2>/dev/null; git checkout -q main

sha1="$(mkpr 1 two a.txt)"; git checkout -q main
export GH_APPROVED=""
check "refuses without approval" "^FAILED approval log=" "$(gate 1)" re
export GH_APPROVED="$sha1"
check "github approval at head passes, rule runs" "READY $sha1" "$(gate 1 --no-merge)"
check "verify ran in the gate tree despite reading stdin" "ran-verify" "$(cat "$T"/data/merge-gate/pr1-*/verify.log | head -n1)"
export GH_APPROVED=""
check "tallpine approval at head passes" "READY $sha1" "$(gate 1 --no-merge --tallpine-approved "$sha1")"

sha2="$(mkpr 2 x bad.txt)"; git checkout -q main
check "failing verify job named with log" "^FAILED go-lint log=.*/verify.log" "$(gate 2 --no-merge --tallpine-approved "$sha2")" re

# identical-patch rebase keeps approval
git checkout -q main; echo more >b.txt; git add -A; git commit -q -m main2; git push -q origin main
sha3="$(mkpr 3 three c.txt)"; git checkout -q -B old "$sha3~0"; git checkout -q -B "pr3b" "$sha3"
git checkout -q main; git checkout -q -b reb origin/main; git cherry-pick "$sha3" >/dev/null; echo y >/dev/null
newhead="$(git rev-parse HEAD)"
git checkout -q main; git push -q -f origin "$newhead:refs/pull/3/head"
git checkout -q -B base2 origin/main; echo extra >d.txt; git add -A; git commit -q -m main3; git push -q origin base2:main
git fetch -q origin
check "identical-patch rebase keeps approval" "READY $newhead" "$(gate 3 --no-merge --tallpine-approved "$sha3")"

# main moving during the gate refuses
export MERGE_GATE_VERIFY='git -C "$MG_ORIGIN" update-ref refs/heads/main "$(git -C "$MG_ORIGIN" commit-tree "main^{tree}" -p main -m moved)"'
export MG_ORIGIN="$T/origin.git"
check "main moved during gate refuses" "^FAILED recheck log=" "$(gate 1 --no-merge --tallpine-approved "$sha1")" re
git fetch -q origin
export MERGE_GATE_VERIFY='cat >/dev/null; echo ran-verify'

# a PR that edits verify to skip a check still fails: main's verify runs, and so does the PR's
unset MERGE_GATE_VERIFY
git fetch -q origin; git checkout -q -B main origin/main; mkdir -p scripts; printf '#!/bin/sh\n[ -f bad.txt ] && { echo "verify --changed summary (logs: x)"; echo "  go-lint  1s  FAIL"; exit 1; }\nexit 0\n' >scripts/verify; chmod +x scripts/verify
git add -A; git commit -q -m "verify v1"; git push -q origin main
git checkout -q -B pr5 origin/main; echo x >bad.txt; printf '#!/bin/sh\nexit 0\n' >scripts/verify; git add -A; git commit -q -m "weaken verify"; git push -q origin HEAD:refs/pull/5/head; sha5="$(git rev-parse HEAD)"; git checkout -q main
check "PR weakening verify still fails on main's verify" "^FAILED go-lint log=.*/verify.log" "$(gate 5 --no-merge --tallpine-approved "$sha5")" re
export MERGE_GATE_VERIFY='cat >/dev/null; echo ran-verify'

# go-e2e: the changed Go packages are tested with -tags e2e, and a failure fails the gate
git fetch -q origin; git checkout -q -B main origin/main
printf '#!/bin/sh\ncase " $* " in *" -tags e2e "*) ;; *) exit 0 ;; esac\n[ -f fail_e2e.go ] || [ -f pkg/fail_e2e.go ] && { echo "e2e failed"; exit 1; }\nexit 0\n' >scripts/go; chmod +x scripts/go
mkdir -p backend/pkg; echo 'package pkg' >backend/pkg/a.go; git add -A; git commit -q -m "go stub"; git push -q origin main
git checkout -q -B pr6 origin/main; echo 'package pkg' >backend/pkg/fail_e2e.go; git add -A; git commit -q -m "e2e fails"; git push -q origin HEAD:refs/pull/6/head; sha6="$(git rev-parse HEAD)"; git checkout -q main
check "failing e2e step fails the gate" "^FAILED go-e2e log=.*/go-e2e.log" "$(gate 6 --no-merge --tallpine-approved "$sha6")" re
git checkout -q -B pr7 origin/main; echo 'package pkg' >backend/pkg/ok.go; git add -A; git commit -q -m "e2e ok"; git push -q origin HEAD:refs/pull/7/head; sha7="$(git rev-parse HEAD)"; git checkout -q main
check "passing e2e step lets the gate pass" "READY $sha7" "$(gate 7 --no-merge --tallpine-approved "$sha7")"

# B435: a data dir the gate cannot write (a sandboxed session) puts the logs under $TMPDIR, not a denied mkdir
mkdir -p "$T/gtmp"; : >"$T/notadir"
out="$(TALLPINE_DATA_DIR="$T/notadir" TMPDIR="$T/gtmp" GH_APPROVED="" gate 1 --no-merge)"
check "unwritable data dir logs under TMPDIR" "^FAILED approval log=$T/gtmp/merge-gate-logs/pr1-.*/approval.log" "$out" re

# B492: verify runs with the gate's own GOCACHE under the data dir, not the shared per-user cache
V0="$MERGE_GATE_VERIFY"; export MERGE_GATE_VERIFY='cat >/dev/null; echo "cache=$GOCACHE" >"'"$T"'/gocache.seen"'
GH_APPROVED="$sha1" gate 1 --tallpine-approved "$sha1" --no-merge >/dev/null
check "gate uses own GOCACHE" "cache=$T/data/merge-gate/gocache" "$(cat "$T/gocache.seen" 2>/dev/null)"
export MERGE_GATE_VERIFY="$V0"

# merge: squash command pinned to head with [skip ci]
rm -f "$GH_LOG"; export GH_APPROVED="$sha1"
out="$(gate 1 --tallpine-approved "$sha1")"; check "merge prints MERGED" "MERGED abc123" "$out"
check "gh pr merge pinned" "pr merge 1 -R $T/origin.git --squash --subject my change (#1) [skip ci] --match-head-commit $sha1" "$(cat "$GH_LOG")"
# B537: a gate killed past its EXIT trap strands its worktree; the next gate prunes it, but not a live one.
git worktree add -q --detach "$T/merge-gate.stale" main; sh -c 'exit 0' & deadpid=$!; wait "$deadpid"; echo "$deadpid" >"$T/merge-gate.stale.pid"
git worktree add -q --detach "$T/merge-gate.live" main; echo $$ >"$T/merge-gate.live.pid"
gate 1 --no-merge --tallpine-approved "$sha1" >/dev/null
check "stale gate worktree pruned" "" "$(git worktree list | grep merge-gate.stale)"
check "live gate worktree kept" "1" "$(git worktree list | grep -c merge-gate.live)"
[ "$fails" = 0 ] || { echo "$fails failure(s)"; exit 1; }
echo "all ok"
