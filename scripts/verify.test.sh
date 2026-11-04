#!/usr/bin/env bash
# Golden test for the merge gate's job list: `scripts/verify --all --tsv` must
# equal scripts/testdata/verify-all.tsv, and every job the old local CI loop ran
# must be in it. Adding or removing a gate job means updating the golden file
# (scripts/verify --all --tsv > scripts/testdata/verify-all.tsv) in the same PR.
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
golden="$here/testdata/verify-all.tsv"
got="$("$here/verify" --all --tsv)" || { echo "FAIL: verify --all --tsv exited non-zero"; exit 1; }
rc=0
if [ "$got" != "$(cat "$golden")" ]; then
	echo "FAIL: verify --all --tsv differs from $golden:"
	diff <(cat "$golden") <(printf '%s\n' "$got")
	rc=1
fi
# Old loop job -> verify job (loop names that were renamed or folded in).
for old in lockfile-sync go-fmt go-build go-vet go-test cli-e2e go-lint api-drift fe-tsc fe-tsc-e2e fe-tsgo \
	fe-vitest docs-i18n docs-test docs-build docs-cli mobile e2e-gate release-local-test heredoc-guard gitleaks; do
	printf '%s\n' "$got" | cut -f1 | grep -qx "$old" || { echo "FAIL: old loop job $old is not in verify --all"; rc=1; }
done
# --all must fail, not skip, when gitleaks is missing (the old loop failed too).
if ! grep -q 'mode" = all \]; then' "$here/verify" || ! grep -q 'gitleaks is not installed' "$here/verify"; then
	echo "FAIL: verify --all must fail when gitleaks is missing"; rc=1
fi
# A failed package of this module (tallpine.eu/...) must be picked for the one retry.
eval "$(sed -n '/^failing_go_packages()/p' "$here/verify")"
vf="$(mktemp)"
printf 'ok\ttallpine.eu/tallpine/backend/a\t0.1s\nFAIL\ttallpine.eu/tallpine/backend/b\t0.2s\nFAIL\n' >"$vf"
[ "$(failing_go_packages "$vf")" = "tallpine.eu/tallpine/backend/b" ] || { echo "FAIL: failing_go_packages missed the module's FAIL line"; rc=1; }
rm -f "$vf"
# A flaky vitest file's first-run assertion message and stack must be extracted (ANSI stripped) before the log dir is deleted.
eval "$(sed -n '/^vitest_failure_details()/,/^}/p' "$here/verify")"
vj="$(mktemp)"
printf '%s' '{"testResults":[{"name":"/r/frontend/src/a.test.tsx","status":"failed","assertionResults":[{"status":"passed","fullName":"ok one","failureMessages":[]},{"status":"failed","fullName":"A > breaks","failureMessages":["\u001b[31mAssertionError: expected 1 to be 2\u001b[39m\n    at src/a.test.tsx:9:3"]}]},{"name":"/r/frontend/src/b.test.tsx","status":"failed","assertionResults":[{"status":"failed","fullName":"B > other","failureMessages":["nope"]}]}]}' >"$vj"
det="$(vitest_failure_details "$vj" /r/frontend src/a.test.tsx)"
case "$det" in *"src/a.test.tsx > A > breaks"*"AssertionError: expected 1 to be 2"*"at src/a.test.tsx:9:3"*) ;; *) echo "FAIL: vitest_failure_details lost the assertion or stack: $det"; rc=1 ;; esac
case "$det" in *$'\033'*|*"B > other"*) echo "FAIL: vitest_failure_details kept ANSI or an unrequested file"; rc=1 ;; esac
rm -f "$vj"
# A go test killed by a signal (rc>128, no FAIL line) is re-run once, and the signal and time are named (B347).
eval "$(sed -n '/^failing_go_packages()/p;/^go_test()/,/^}/p' "$here/verify")"
gt="$(mktemp -d)"; mkdir -p "$gt/backend" "$gt/bin"
cat >"$gt/bin/go" <<'G'
#!/bin/bash
n=$(cat "$GT/n" 2>/dev/null || echo 0); echo $((n+1)) >"$GT/n"
[ "$n" = 0 ] && kill -TERM $$
echo "ok pkg 0.1s"
G
chmod +x "$gt/bin/go"
go_packages() { echo ./...; }
(export GT="$gt" PATH="$gt/bin:$PATH"; cd "$gt"; with_slot() { "$@"; }; CORES=2 count_flag="-count=1" NICE=0 race="" GO_TEST_TIMEOUT=1m retry=1 log_dir="$gt" flaky=(); go_test >"$gt/out" 2>&1; echo "rc=$?" >>"$gt/out"; printf '%s\n' "${flaky[@]}" >>"$gt/out")
grep -q 'rc=0' "$gt/out" && grep -q 'killed by signal 15 at ' "$gt/out" || { echo "FAIL: signal-killed go test not retried: $(cat "$gt/out")"; rc=1; }
rm -rf "$gt"
# --since planning (B350): which jobs `--all --tsv --since <sha>` plans for a change set. Runs in a scratch git repo
# with copies of the scripts so the commits are synthetic.
sp="$(mktemp -d)"
mkdir -p "$sp/scripts" "$sp/backend" "$sp/frontend/src/docs/content" "$sp/frontend/src/a"
cp -R "$here/verify" "$here/verify.d" "$sp/scripts/"
printf 'package x\n' >"$sp/backend/x.go"
printf 'a\n' >"$sp/frontend/src/a/a.ts"
printf 'd\n' >"$sp/frontend/src/docs/content/a.md"
(cd "$sp" && git init -q && git add -A && git -c user.name=t -c user.email=t@t commit -qm base)
plan() { # plan <label>: commit the working tree and print the planned job names
	(cd "$sp" && git add -A && git -c user.name=t -c user.email=t@t commit -qm "$1" && TMPDIR="$sp" scripts/verify --all --tsv --since "$2" | grep "	" | cut -f1 | tr '\n' ' ')
}
full="$(cut -f1 "$golden" | tr '\n' ' ')"
base="$(git -C "$sp" rev-parse HEAD)"
mkdir -p "$sp/docs" && printf 'more\n' >"$sp/docs/readme.md"
p="$(plan docs "$base")"
case "$p" in *go-test*|*fe-vitest*) echo "FAIL: docs-only change planned Go/vitest: $p"; rc=1 ;; esac
case "$p" in *go-invariants*) ;; *) echo "FAIL: docs-only change did not plan go-invariants: $p"; rc=1 ;; esac
mkdir -p "$sp/scripts/golden-eval" && printf 'x\n' >"$sp/scripts/golden-eval/a.mjs"
p="$(plan golden-eval "$base")"
case "$p" in *golden-eval-test*) ;; *) echo "FAIL: scripts/golden-eval change did not plan golden-eval-test: $p"; rc=1 ;; esac
case "$p" in *go-test*|*fe-vitest*|*local-ci-test*) echo "FAIL: scripts/golden-eval change planned unrelated steps: $p"; rc=1 ;; esac
base="$(git -C "$sp" rev-parse HEAD)"
printf '# x\n' >>"$sp/scripts/verify"
p="$(plan scripts "$base")"
[ "$p" = "$full" ] || { echo "FAIL: scripts/ change did not plan the full list: $p"; rc=1; }
base="$(git -C "$sp" rev-parse HEAD)"
printf 'x\n' >"$sp/root.cfg"
p="$(plan unknown "$base")"
[ "$p" = "$full" ] || { echo "FAIL: unknown-area file did not plan the full list: $p"; rc=1; }
base="$(git -C "$sp" rev-parse HEAD)"
printf '{}\n' >"$sp/frontend/package-lock.json"
p="$(plan lockfile "$base")"
[ "$p" = "$full" ] || { echo "FAIL: lockfile change did not plan the full list: $p"; rc=1; }
base="$(git -C "$sp" rev-parse HEAD)"
git -C "$sp" mv backend/x.go docs/x.go
p="$(plan rename "$base")"
case "$p" in *go-test*) ;; *) echo "FAIL: a rename out of backend/ did not plan Go steps: $p"; rc=1 ;; esac
rm -rf "$sp"
# B360: adapt_caps splits VERIFY_CORE_BUDGET across the concurrent steps (go-test 1/3, lint 1/4, vitest 1/2, rest 1/6),
# each clamped to at least 2; a worker (no budget) keeps its caps.
eval "$(sed -n '/^adapt_caps()/,/^}/p' "$here/verify.d/par.sh")"
caps() { # caps <budget> [priority]: print "CORES LINT VITEST MAIN"
	( VERIFY_PRIORITY="${2:-1}" VERIFY_CORE_BUDGET="$1" NCPU=18 CORES=2 LINT_CORES=2 VITEST_CAP=6 MAIN_CORES=2; adapt_caps; echo "$CORES $LINT_CORES $VITEST_CAP $MAIN_CORES" )
}
for want in "12:4 3 6 2" "24:8 6 12 4" "4:2 2 2 2" "2:2 2 2 2"; do
	cg="$(caps "${want%%:*}")"
	[ "$cg" = "${want#*:}" ] || { echo "FAIL: budget ${want%%:*} split '$cg', want '${want#*:}'"; rc=1; }
done
[ "$(caps 12 0)" = "2 2 6 2" ] || { echo "FAIL: non-loop run changed its caps: $(caps 12 0)"; rc=1; }
# B361: a SIGTERM that reaches verify mid-step leaves a summary with a FAIL line naming the signal (not a log
# that just stops), and exits 128+15. The real on_signal and trap lines are extracted from scripts/verify.d/signal.sh.
sig_out="$(mktemp)"
{
	echo 'set -uo pipefail; tsv=0; failed=(); timings=("go-fmt 1s ok")'
	sed -n '/^on_signal()/,/^}/p' "$here/verify.d/signal.sh"
	sed -n "/^	trap 'on_signal 15 TERM' TERM/p" "$here/verify.d/signal.sh"
	echo 'sleep 30 & wait'
} >"$sig_out.sh"
bash "$sig_out.sh" >"$sig_out" 2>&1 &
sp_pid=$!; sleep 1; kill -TERM "$sp_pid"; wait "$sp_pid"; sp_rc=$?
{ [ "$sp_rc" = 143 ] && grep -q '^FAIL: verify-killed-by-SIGTERM' "$sig_out" && grep -q 'go-fmt' "$sig_out"; } ||
	{ echo "FAIL: SIGTERM to verify left no summary (rc $sp_rc): $(cat "$sig_out")"; rc=1; }
pkill -f "$sig_out.sh" 2>/dev/null; rm -f "$sig_out" "$sig_out.sh"
# --lint-changed widens to ./... without --new-from-rev when the lint config changes (and only then).
sp="$(mktemp -d)"; mkdir -p "$sp/scripts" "$sp/backend"
cp -R "$here/verify" "$here/verify.d" "$here/size-ratchet" "$sp/scripts/"
printf 'package x\n' >"$sp/backend/x.go"; printf 'module x\n\ngo 1.22\n' >"$sp/backend/go.mod"; printf 'linters:\n' >"$sp/backend/.golangci.yml"
(cd "$sp" && git init -q && git add -A && git -c user.name=t -c user.email=t@t commit -qm base0)
(cd "$sp" && git add -A && git -c user.name=t -c user.email=t@t commit -qm cfgbase)
printf '// d\n' >>"$sp/backend/x.go"
la="$(cd "$sp" && VERIFY_PRINT_LINT_ARGS=1 TMPDIR="$sp" scripts/verify --lint-changed --base HEAD 2>&1 | grep -E '^run ')"
case "$la" in *new-from-rev*) ;; *) echo "FAIL: --lint-changed on a Go-only change should use new-from-rev: $la"; rc=1 ;; esac
printf 'linters: x\n' >"$sp/backend/.golangci.yml"
la="$(cd "$sp" && VERIFY_PRINT_LINT_ARGS=1 TMPDIR="$sp" scripts/verify --lint-changed --base HEAD 2>&1 | grep -E '^run ')"
case "$la" in *new-from-rev*) echo "FAIL: lint config change must not use new-from-rev: $la"; rc=1 ;; *"./..."*) ;; *) echo "FAIL: lint config change must lint ./...: $la"; rc=1 ;; esac

[ "$rc" = 0 ] && echo "verify job list: ok ($(printf '%s\n' "$got" | grep -c .) jobs)"
exit "$rc"
