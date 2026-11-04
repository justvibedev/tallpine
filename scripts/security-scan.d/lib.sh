# shellcheck shell=bash
# Shared helpers for scripts/security-scan. Sourced, never executed.
# Needs SS_OUT (results dir) set by the caller. Parsers are pure functions of a
# raw result file so scripts/security-scan.test.sh can feed them fixtures.

ss_log() { printf 'security-scan: %s\n' "$*" >&2; }

# ss_summary <tool> <text>: one line per tool in summary.txt.
ss_summary() { printf '%-12s %s\n' "$1" "$2" >>"$SS_OUT/summary.txt"; }

# ss_state <key> <value>: counters --strict decides on.
ss_state() { printf '%s=%s\n' "$1" "$2" >>"$SS_OUT/.state"; }

# ss_state_get <key>: last recorded value, 0 when absent.
ss_state_get() {
	local v
	v="$(sed -n "s/^$1=//p" "$SS_OUT/.state" 2>/dev/null | tail -n 1)"
	printf '%s' "${v:-0}"
}

# ss_strict_failures: prints one reason per line; returns the number of reasons.
ss_strict_failures() {
	local n=0 v
	for v in govulncheck osv; do
		if [ "$(ss_state_get "${v}_error")" -gt 0 ]; then
			echo "$v: scanner failed or was skipped, so a clean result cannot be trusted"
			n=$((n + 1))
		fi
	done
	v="$(ss_state_get govulncheck_reachable)"
	if [ "$v" -gt 0 ]; then
		echo "govulncheck: $v reachable vulnerabilities"
		n=$((n + 1))
	fi
	v="$(($(ss_state_get osv_high) + $(ss_state_get osv_critical)))"
	if [ "$v" -gt 0 ]; then
		echo "osv-scanner: $v high/critical findings"
		n=$((n + 1))
	fi
	return "$n"
}

# ss_tool_missing <tool> <cmd>: records a skipped line and returns 0 when cmd is absent.
ss_tool_missing() {
	command -v "$2" >/dev/null 2>&1 && return 1
	ss_summary "$1" "SKIPPED ($2 not installed)"
	return 0
}

# --- parsers (pure: raw file in, text out) -------------------------------

# govulncheck -format json stream -> "reachable=N package=N module=N" (distinct
# OSV ids at their deepest level; only symbol-level traces are reachable).
ss_parse_govulncheck() {
	jq -rs '
	  [.[] | select(.finding) | .finding] | group_by(.osv)
	  | map(if any(.[]; .trace[0].function != null) then "reachable"
	        elif any(.[]; .trace[0].package != null) then "package" else "module" end)
	  | "reachable=\(map(select(. == "reachable")) | length) package=\(map(select(. == "package")) | length) module=\(map(select(. == "module")) | length)"
	' "$1"
}

# gosec -fmt json -> "high=N medium=N low=N"
ss_parse_gosec() {
	jq -r '[.Issues // [] | .[] | .severity] as $s
	  | "high=\($s | map(select(. == "HIGH")) | length) medium=\($s | map(select(. == "MEDIUM")) | length) low=\($s | map(select(. == "LOW")) | length)"' "$1"
}

# osv-scanner --format json -> "critical=N high=N medium=N low=N unknown=N" per
# vulnerability group, bucketed by CVSS max_severity (>=9 critical, >=7 high, >=4 medium).
ss_parse_osv() {
	jq -r '[.results // [] | .[] | .packages // [] | .[] | .groups // [] | .[] | (.max_severity // "" | tonumber? // -1)] as $s
	  | "critical=\($s | map(select(. >= 9)) | length) high=\($s | map(select(. >= 7 and . < 9)) | length) medium=\($s | map(select(. >= 4 and . < 7)) | length) low=\($s | map(select(. >= 0 and . < 4)) | length) unknown=\($s | map(select(. < 0)) | length)"' "$1"
}

# semgrep --json -> "error=N warning=N info=N scan_errors=N parse_warnings=N" (scan_errors: bad
# rulesets; non-zero means the counts cannot be trusted; parse_warnings are partial parses).
ss_parse_semgrep() {
	jq -r '[.results // [] | .[] | .extra.severity] as $s
	  | "error=\($s | map(select(. == "ERROR")) | length) warning=\($s | map(select(. == "WARNING")) | length) info=\($s | map(select(. == "INFO")) | length) scan_errors=\([.errors // [] | .[] | select(.level == "error")] | length) parse_warnings=\([.errors // [] | .[] | select(.level != "error")] | length)"' "$1"
}

# trivy --format json -> "critical=N high=N medium=N low=N unknown=N"
ss_parse_trivy() {
	jq -r '[.Results // [] | .[] | .Vulnerabilities // [] | .[] | .Severity] as $s
	  | "critical=\($s | map(select(. == "CRITICAL")) | length) high=\($s | map(select(. == "HIGH")) | length) medium=\($s | map(select(. == "MEDIUM")) | length) low=\($s | map(select(. == "LOW")) | length) unknown=\($s | map(select(. == "UNKNOWN")) | length)"' "$1"
}

# @electron/fuses read output ("  RunAsNode is Disabled") -> "on=N off=N"
ss_parse_fuses() {
	awk '/ is Enabled *$/ {on++} / is Disabled *$/ {off++} END {printf "on=%d off=%d\n", on, off}' "$1"
}

# Value of key in a "k=v k=v" line.
ss_kv() { printf '%s\n' "$1" | tr ' ' '\n' | sed -n "s/^$2=//p"; }
