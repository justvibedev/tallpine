# Sourced by scripts/verify: vitest failure parsing and the retry-once runner (uses $log_dir, $repo, $retry, $NICE,
# $VITEST_CAP, $flaky and with_slot from verify).
# vitest_failure_details <json> <root> <repo-relative-file>...: each failed test's
# title, assertion message and stack (ANSI stripped, capped) from a vitest json
# report, for the files that later passed on retry.
vitest_failure_details() {
	node -e '
		const [json, root, ...files] = process.argv.slice(1);
		const path = require("path");
		const r = JSON.parse(require("fs").readFileSync(json));
		for (const t of r.testResults) {
			const rel = path.relative(root, t.name);
			if (!files.includes(rel)) continue;
			if (t.status === "failed" && !t.assertionResults.some((a) => a.status === "failed")) console.log(`${rel}: ${t.message || "file failed to run"}`);
			for (const a of t.assertionResults.filter((a) => a.status === "failed")) {
				console.log(`${rel} > ${a.fullName}`);
				for (const m of a.failureMessages) console.log(m.replace(/\u001b\[[0-9;]*m/g, "").split("\n").slice(0, 25).join("\n"));
			}
		}' "$@" 2>/dev/null
}

# vitest_failed_files <json> <root>: space-separated root-relative failed test files.
vitest_failed_files() {
	node -e 'const r=JSON.parse(require("fs").readFileSync(process.argv[1]));console.log(r.testResults.filter(t=>t.status==="failed").map(t=>require("path").relative(process.argv[2],t.name)).join(" "))' "$@" 2>/dev/null
}

# vitest_retry <label> <dir> <config> [extra first-run args...]: run vitest in <dir>
# (repo-relative) with <config>; re-run only the failed files once. A file that
# passes on the retry is reported as flaky, one that fails again fails the step.
vitest_retry() {
	local label="$1" dir="$2" config="$3"
	shift 3
	local base_args=(run --config "$config" --maxWorkers="$VITEST_CAP" --reporter=dot)
	local json1="$log_dir/$label-1.json" rc
	(cd "$dir" && with_slot nice -n "$NICE" npx vitest "${base_args[@]}" "$@" --reporter=json --outputFile.json="$json1") >"$log_dir/$label-1.out" 2>&1
	rc=$?
	tail -n 25 "$log_dir/$label-1.out"
	[ $rc -eq 0 ] && return 0
	local files
	files="$(vitest_failed_files "$json1" "$repo/$dir")"
	# Killed by a signal (rc>128, e.g. SIGTERM from a reaper or pkill) before any file failed: an external
	# kill, not a test failure (B717; same as go test, B347). Re-run everything once.
	if [ $rc -gt 128 ] && [ -z "$files" ] && [ "$retry" = 1 ]; then
		local sig=$((rc - 128)) when
		when="$(date '+%Y-%m-%d %H:%M:%S %Z')"
		echo "=== $label killed by signal $sig at $when (external kill, not a test failure); re-running once"
		(cd "$dir" && nice -n "$NICE" npx vitest "${base_args[@]}" "$@" --reporter=json --outputFile.json="$json1") >"$log_dir/$label-1.out" 2>&1
		rc=$?
		tail -n 25 "$log_dir/$label-1.out"
		if [ $rc -eq 0 ]; then
			flaky+=("$label: killed by signal $sig at $when, passed on retry (sender unknown)")
			return 0
		fi
		files="$(vitest_failed_files "$json1" "$repo/$dir")"
	fi
	[ "$retry" = 1 ] && [ -n "$files" ] || return $rc
	echo "=== retrying failed files once: $files"
	local json2="$log_dir/$label-2.json"
	# shellcheck disable=SC2086
	(cd "$dir" && nice -n "$NICE" npx vitest "${base_args[@]}" --reporter=json --outputFile.json="$json2" $files) >"$log_dir/$label-2.out" 2>&1
	rc=$?
	tail -n 25 "$log_dir/$label-2.out"
	local still f recovered=""
	still="$(vitest_failed_files "$json2" "$repo/$dir")"
	for f in $files; do
		case " $still " in *" $f "*) ;; *) flaky+=("$label: $f (failed, then passed on retry)"); recovered="$recovered $f" ;; esac
	done
	# The log dir is deleted when verify exits: keep the first-run evidence.
	# shellcheck disable=SC2086
	[ -n "$recovered" ] && vitest_failure_details "$json1" "$repo/$dir" $recovered >>"$log_dir/flaky-detail.txt"
	return $rc
}
