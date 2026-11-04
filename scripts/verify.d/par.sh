# Sourced by scripts/verify: concurrent heavy steps (pstep/wait_p), the machine-wide
# core budget (with_slot) and load-adaptive caps (CORES, VITEST_CAP).
#
# Budget: heavy steps (go-test, go-lint, fe-vitest) from every verify on this machine, and every other
# broad test entry point (scripts/go test ./..., npm test), take one slot of the shared pool in
# scripts/test-slot (default 4 slots, queueing in FIFO order; a killed holder frees its slot). The loop
# (VERIFY_PRIORITY=1, set by --loop) is a reserved lane and never takes a pool slot. make lint queues too.
NCPU="$(sysctl -n hw.ncpu 2>/dev/null || nproc 2>/dev/null || echo 4)"
CORES="${GOMAXPROCS:-2}" VITEST_CAP="$VITEST_WORKERS" LINT_CORES=2 MAIN_CORES=2 # workers keep their own GOMAXPROCS
# The loop may use free cores (load average below half the machine); under load, today's caps.
adapt_caps() {
	[ "${VERIFY_PRIORITY:-0}" = 1 ] || return 0
	# The loop's core budget (VERIFY_CORE_BUDGET, set by local-ci/loop.sh) is split across the steps that
	# run at once: vitest 1/2 (the longest step, so it gets the most), go-test 1/3, lint 1/4, the rest (e2e, mobile,
	# tsc) 1/6. Shares sum past 1 on purpose: go-test and lint finish early, vitest runs on. Workers never set it.
	if [ "${VERIFY_CORE_BUDGET:-0}" -gt 0 ] 2>/dev/null; then
		local b="$VERIFY_CORE_BUDGET"
		CORES=$((b / 3 < 2 ? 2 : b / 3)) LINT_CORES=$((b / 4 < 2 ? 2 : b / 4))
		VITEST_CAP=$((b / 2 < 2 ? 2 : b / 2)) MAIN_CORES=$((b / 6 < 2 ? 2 : b / 6))
		return 0
	fi
	local load free
	load="$(sysctl -n vm.loadavg 2>/dev/null | awk '{gsub(/[{}]/, ""); print int($1)}')"
	free=$((NCPU - ${load:-NCPU}))
	if [ "$free" -ge $((NCPU / 2)) ]; then
		CORES=$((free / 2 > 6 ? 6 : free / 2))
		[ "$CORES" -lt 2 ] && CORES=2
		VITEST_CAP=$((CORES + 2))
	fi
}
with_slot() { # with_slot <command...>: run it holding one slot of the machine-wide pool (scripts/test-slot)
	"$repo/scripts/test-slot" -- "$@"
}

# pstep: like step, but in --loop mode runs in the background (heavy independent steps);
# wait_p joins them and merges their results. Each keeps its own nice level and caps.
pnames=() ppids=()
pstep() {
	local name="$1"
	if [ "$par" != 1 ]; then
		step "$@"
		return
	fi
	(
		failed=() flaky=() timings=()
		step "$@"
		printf '%s\n' "${failed[@]}" >"$log_dir/p.$name.failed"
		printf '%s\n' "${flaky[@]}" >"$log_dir/p.$name.flaky"
		printf '%s\n' "${timings[@]}" >"$log_dir/p.$name.timings"
	) >"$log_dir/p.$name.out" 2>&1 &
	pnames+=("$name")
	ppids+=($!)
}
wait_p() {
	local i n line prc
	for i in "${!pnames[@]}"; do
		n="${pnames[$i]}"
		wait "${ppids[$i]}"
		prc=$?
		cat "$log_dir/p.$n.out"
		# Fail closed: a step killed before it recorded its result must not pass silently.
		if [ "$prc" -ne 0 ] || [ ! -f "$log_dir/p.$n.timings" ]; then
			failed+=("$n")
			echo "--- $n FAILED (background step died, rc $prc)"
			continue
		fi
		while IFS= read -r line; do [ -n "$line" ] && failed+=("$line"); done <"$log_dir/p.$n.failed"
		while IFS= read -r line; do [ -n "$line" ] && flaky+=("$line"); done <"$log_dir/p.$n.flaky"
		while IFS= read -r line; do [ -n "$line" ] && timings+=("$line"); done <"$log_dir/p.$n.timings"
	done
	pnames=() ppids=()
}
adapt_caps
