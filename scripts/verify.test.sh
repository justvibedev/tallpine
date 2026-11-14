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
# B518: the repo-wide testenv meta test runs on every changed-mode push, not only when internal/testenv changes.
sed -n '/^go_invariants()/,/^}/p' "$here/verify" | grep -q './internal/testenv/' || { echo "FAIL: go_invariants must run ./internal/testenv/"; rc=1; }
# A failed package of this module (tallpine.eu/...) must be picked for the one retry.
eval "$(sed -n '/^failing_go_packages()/p' "$here/verify")"
vf="$(mktemp)"
printf 'ok\ttallpine.eu/tallpine/backend/a\t0.1s\nFAIL\ttallpine.eu/tallpine/backend/b\t0.2s\nFAIL\n' >"$vf"
[ "$(failing_go_packages "$vf")" = "tallpine.eu/tallpine/backend/b" ] || { echo "FAIL: failing_go_packages missed the module's FAIL line"; rc=1; }
rm -f "$vf"
# A flaky vitest file's first-run assertion message and stack must be extracted (ANSI stripped) before the log dir is deleted.
eval "$(sed -n '/^vitest_failure_details()/,/^}/p' "$here/verify.d/vitest.sh")"
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
# B625: a vitest file that fails once and passes on the one retry is reported flaky and the step passes;
# a file that fails both attempts still fails it. Fake npx: the first run fails a.test.ts, the retry's result is $RETRY.
eval "$(sed -n '/^vitest_failed_files()/,/^}/p;/^vitest_failure_details()/,/^}/p;/^vitest_retry()/,/^}/p' "$here/verify.d/vitest.sh")"
vr="$(mktemp -d)"; mkdir -p "$vr/bin" "$vr/pkg"
printf '%s\n' '#!/bin/bash' \
	'for a in "$@"; do case "$a" in --outputFile.json=*) out="${a#*=}" ;; esac; done' \
	'n=$(cat "$VR/n" 2>/dev/null || echo 0); echo $((n+1)) >"$VR/n"' \
	'st=failed; code=1; [ "$n" != 0 ] && [ "$RETRY" = pass ] && st=passed && code=0' \
	'printf "{\"testResults\":[{\"name\":\"%s/pkg/a.test.ts\",\"status\":\"%s\",\"assertionResults\":[{\"status\":\"%s\",\"fullName\":\"A\",\"failureMessages\":[\"Test timed out in 5000ms\"]}]}]}" "$VR" "$st" "$st" >"$out"' \
	'exit $code' >"$vr/bin/npx"
chmod +x "$vr/bin/npx"
vr_run() { # vr_run <pass|fail>: prints "rc=<n>" then the flaky lines
	rm -f "$vr/n" "$vr/flaky-detail.txt"
	(export VR="$vr" RETRY="$1" PATH="$vr/bin:$PATH"; cd "$vr"; with_slot() { "$@"; }
		repo="$vr" log_dir="$vr" NICE=0 VITEST_CAP=1 retry=1 flaky=()
		vitest_retry mobile pkg vitest.config.ts >/dev/null 2>&1; echo "rc=$?"; printf '%s\n' ${flaky[@]+"${flaky[@]}"})
}
vr_pass="$(vr_run pass)"
case "$vr_pass" in "rc=0"*"mobile: a.test.ts (failed, then passed on retry)"*) ;; *) echo "FAIL: vitest_retry did not report a retried pass as flaky: $vr_pass"; rc=1 ;; esac
grep -q 'Test timed out in 5000ms' "$vr/flaky-detail.txt" || { echo "FAIL: vitest_retry dropped the first-run failure detail"; rc=1; }
vr_fail="$(vr_run fail)"
case "$vr_fail" in "rc=1"*) ;; *) echo "FAIL: vitest_retry must fail a file that fails both attempts: $vr_fail"; rc=1 ;; esac
case "$vr_fail" in *flaky*|*"retry)"*) echo "FAIL: vitest_retry reported a twice-failing file as flaky: $vr_fail"; rc=1 ;; esac
# B717: vitest killed by a signal before any file failed is an external kill: re-run everything once. Fake npx: run 1 is
# killed (no json); run 2 passes, or fails a.test.ts (then the per-file retry, run 3, passes).
printf '%s\n' '#!/bin/bash' \
	'for a in "$@"; do case "$a" in --outputFile.json=*) out="${a#*=}" ;; esac; done' \
	'n=$(cat "$VR/n" 2>/dev/null || echo 0); echo $((n+1)) >"$VR/n"' \
	'[ "$n" = 0 ] && kill -TERM $$' \
	'st=passed; code=0; [ "$n" = 1 ] && [ "$SECOND" = fail ] && st=failed && code=1' \
	'printf "{\"testResults\":[{\"name\":\"%s/pkg/a.test.ts\",\"status\":\"%s\",\"assertionResults\":[{\"status\":\"%s\",\"fullName\":\"A\",\"failureMessages\":[\"boom\"]}]}]}" "$VR" "$st" "$st" >"$out"' \
	'exit $code' >"$vr/bin/npx"
vs_run() { # vs_run <pass|fail>: the second run's result; prints the output, "rc=<n>", the flaky lines and the run count
	rm -f "$vr/n" "$vr/flaky-detail.txt" "$vr"/mobile-*.json
	(export VR="$vr" SECOND="$1" PATH="$vr/bin:$PATH"; cd "$vr"; with_slot() { "$@"; }
		repo="$vr" log_dir="$vr" NICE=0 VITEST_CAP=1 retry=1 flaky=()
		vitest_retry mobile pkg vitest.config.ts 2>&1; echo "rc=$?"; printf '%s\n' ${flaky[@]+"${flaky[@]}"}; echo "runs=$(cat "$vr/n")")
}
vs_pass="$(vs_run pass)"
case "$vs_pass" in *"killed by signal 15 at "*"rc=0"*"mobile: killed by signal 15 at "*"runs=2"*) ;; *) echo "FAIL: signal-killed vitest not re-run once: $vs_pass"; rc=1 ;; esac
vs_fail="$(vs_run fail)"
case "$vs_fail" in *"killed by signal 15 at "*"re-running once"*"retrying failed files once: a.test.ts"*"rc=0"*"mobile: a.test.ts (failed, then passed on retry)"*"runs=3"*) ;; *) echo "FAIL: killed-then-failing vitest did not fall through to the per-file retry: $vs_fail"; rc=1 ;; esac
rm -rf "$vr"
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
# B556: unparam reports at the declaration line, which a callers-only PR never touches, so no --new-from-rev.
case "$la" in *new-from-rev*) echo "FAIL: --lint-changed must lint touched packages whole (no new-from-rev): $la"; rc=1 ;; *"./."*) ;; *) echo "FAIL: --lint-changed should lint the changed package: $la"; rc=1 ;; esac
printf 'linters: x\n' >"$sp/backend/.golangci.yml"
la="$(cd "$sp" && VERIFY_PRINT_LINT_ARGS=1 TMPDIR="$sp" scripts/verify --lint-changed --base HEAD 2>&1 | grep -E '^run ')"
case "$la" in *new-from-rev*) echo "FAIL: lint config change must not use new-from-rev: $la"; rc=1 ;; *"./..."*) ;; *) echo "FAIL: lint config change must lint ./...: $la"; rc=1 ;; esac
# B613: --lint-changed runs heredoc-guard on every changed shell file (any path), named in the failure.
# The guard is committed in the fixture base, so editing it counts as a change too (it must not flag itself).
hd='<''<'; body="$(head -c 600 /dev/zero | tr '\0' x)" # built apart so this file carries no heredoc marker itself
cp "$here/heredoc-guard.test.sh" "$sp/scripts/"; mkdir -p "$sp/test"
(cd "$sp" && git add -A && git -c user.name=t -c user.email=t@t commit -qm guardbase)
hg_run() { (cd "$sp" && TMPDIR="$sp" scripts/verify --lint-changed --base HEAD >"$sp/hg.out" 2>&1); }
printf '\n# edit\n' >>"$sp/scripts/heredoc-guard.test.sh"
printf '#!/usr/bin/env bash\ncat %sEOF\nsmall\nEOF\n' "$hd" >"$sp/test/small.sh"
printf 'cat %sEOF\n%s\nEOF\n' "$hd" "$body" >"$sp/test/noshebang.sh"
hg_run; grep -q '^--- heredoc-guard ok' "$sp/hg.out" || # other fixture steps (go-version) fail; only this step matters
	{ echo "FAIL: small heredocs, a shebang-less file and an edited guard must pass heredoc-guard: $(grep -B3 heredoc-guard "$sp/hg.out" | tail -8)"; rc=1; }
printf '#!/usr/bin/env bash\ncat %sEOF\n%s\nEOF\n' "$hd" "$body" >"$sp/test/big.test.sh" # outside scripts/
hg_run
if ! grep -q 'test/big.test.sh:2: heredoc EOF body is 601 bytes' "$sp/hg.out"; then echo "FAIL: heredoc-guard did not name big.test.sh: $(tail -8 "$sp/hg.out")"; rc=1; fi
# A change with no shell file leaves the guard an empty list; stock macOS bash 3.2 must not trip set -u on it.
printf 'plain text\n' >"$sp/plain.txt"
/bin/bash "$sp/scripts/heredoc-guard.test.sh" "$sp/plain.txt" >"$sp/hg3.out" 2>&1 || { echo "FAIL: heredoc-guard on a list with no shell file must pass under /bin/bash: $(cat "$sp/hg3.out")"; rc=1; }

[ "$rc" = 0 ] && echo "verify job list: ok ($(printf '%s\n' "$got" | grep -c .) jobs)"
exit "$rc"
