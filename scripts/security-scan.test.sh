#!/usr/bin/env bash
# Tests for scripts/security-scan: result parsers on fixtures, --strict gating,
# the results-dir guard, and the no-telemetry flags, using stub osv-scanner and
# docker binaries. Runs no real scanner and touches nothing outside a temp dir.
#
#   scripts/security-scan.test.sh
set -uo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
scan="$here/security-scan"
T="$(mktemp -d "${TMPDIR:-/tmp}/vo-secscan-test.XXXXXX")"
trap 'rm -rf "$T"' EXIT
fail=0
ok() { printf 'ok   %s\n' "$1"; }
bad() { printf 'FAIL %s\n' "$1"; fail=1; }
eq() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1: want '$3' got '$2'"; fi; }

export SS_OUT="$T/lib"
mkdir -p "$SS_OUT"
# shellcheck source=/dev/null
. "$here/security-scan.d/lib.sh"

# govulncheck: only symbol-level traces count as reachable.
cat >"$T/vuln.json" <<'J'
{"config":{}}
{"finding":{"osv":"GO-1","trace":[{"module":"stdlib","function":"Serve","package":"net/http"}]}}
{"finding":{"osv":"GO-1","trace":[{"module":"stdlib","package":"net/http"}]}}
{"finding":{"osv":"GO-2","trace":[{"module":"x/y","package":"x/y/z"}]}}
{"finding":{"osv":"GO-3","trace":[{"module":"x/w"}]}}
J
eq "govulncheck reachable/package/module split" "$(ss_parse_govulncheck "$T/vuln.json")" "reachable=1 package=1 module=1"

cat >"$T/osv.json" <<'J'
{"results":[{"packages":[{"groups":[{"max_severity":"9.8"},{"max_severity":"7.5"},{"max_severity":"5.0"},{"max_severity":"2.0"},{"max_severity":""}]}]}]}
J
eq "osv severity buckets" "$(ss_parse_osv "$T/osv.json")" "critical=1 high=1 medium=1 low=1 unknown=1"
echo '{"results":[]}' >"$T/osv-empty.json"
eq "osv empty" "$(ss_parse_osv "$T/osv-empty.json")" "critical=0 high=0 medium=0 low=0 unknown=0"

echo '{"Issues":[{"severity":"HIGH"},{"severity":"MEDIUM"},{"severity":"MEDIUM"}]}' >"$T/gosec.json"
eq "gosec severities" "$(ss_parse_gosec "$T/gosec.json")" "high=1 medium=2 low=0"

echo '{"results":[{"extra":{"severity":"ERROR"}},{"extra":{"severity":"WARNING"}}],"errors":[{"level":"error"},{"level":"warn"}]}' >"$T/sg.json"
eq "semgrep severities and broken-ruleset errors" "$(ss_parse_semgrep "$T/sg.json")" "error=1 warning=1 info=0 scan_errors=1 parse_warnings=1"

echo '{"Results":[{"Vulnerabilities":[{"Severity":"HIGH"},{"Severity":"LOW"}]},{"Target":"x"}]}' >"$T/trivy.json"
eq "trivy severities" "$(ss_parse_trivy "$T/trivy.json")" "critical=0 high=1 medium=0 low=1 unknown=0"

printf '  RunAsNode is Disabled\n  OnlyLoadAppFromAsar is Enabled\n  WasmTrapHandlers is Enabled\n' >"$T/fuses.txt"
eq "fuses on/off" "$(ss_parse_fuses "$T/fuses.txt")" "on=2 off=1"

# End to end with a stub osv-scanner that reports one high finding.
mkdir -p "$T/bin"
cat >"$T/bin/osv-scanner" <<'S'
#!/bin/sh
if [ "$1" = "--version" ]; then echo "osv-scanner version: 2.3.8"; exit 0; fi
out=""; while [ $# -gt 0 ]; do [ "$1" = "--output-file" ] && out="$2"; shift; done
echo '{"results":[{"packages":[{"groups":[{"max_severity":"8.1"}]}]}]}' >"$out"
exit 1
S
chmod +x "$T/bin/osv-scanner"
PATH="$T/bin:$PATH" "$scan" "$T/o1" --only osv >"$T/o1.log" 2>&1
eq "default run is report-only (exit 0 despite osv high)" "$?" "0"
grep -q 'osv-scanner  *critical=0 high=1' "$T/o1/summary.txt" && ok "summary.txt has the osv line" || bad "summary.txt osv line"
PATH="$T/bin:$PATH" "$scan" "$T/o2" --only osv --strict >"$T/o2.log" 2>&1
eq "--strict exits 1 on osv high" "$?" "1"
"$scan" "$T/o3" --only fuses --strict >"$T/o3.log" 2>&1
eq "--strict exits 0 with nothing to fail on" "$?" "0"

# A gating tool that fails or is missing must fail --strict (but not the default run).
cat >"$T/bin/osv-scanner" <<'S'
#!/bin/sh
if [ "$1" = "--version" ]; then echo "osv-scanner version: 2.3.8"; exit 0; fi
exit 127
S
PATH="$T/bin:$PATH" "$scan" "$T/o6" --only osv --strict >"$T/o6.log" 2>&1
eq "--strict exits 1 when osv-scanner produces no output" "$?" "1"
PATH="$T/bin:$PATH" "$scan" "$T/o7" --only osv >"$T/o7.log" 2>&1
eq "default run stays exit 0 when osv-scanner fails" "$?" "0"
mkdir -p "$T/empty"
PATH="$T/empty:/usr/bin:/bin" "$scan" "$T/o8" --only osv --strict >"$T/o8.log" 2>&1
eq "--strict exits 1 when osv-scanner is not installed" "$?" "1"
mkdir -p "$T/gobin" && printf '#!/bin/sh\nexit 1\n' >"$T/gobin/go" && chmod +x "$T/gobin/go"
SS_OUT="$T/o9"; mkdir -p "$SS_OUT"; : >"$SS_OUT/.state"; SS_REPO="$T/fakerepo"; mkdir -p "$T/fakerepo/scripts" "$T/fakerepo/backend"
cp "$T/gobin/go" "$T/fakerepo/scripts/go"
. "$here/security-scan.d/go.sh"
ss_run_govulncheck
ss_strict_failures >/dev/null; eq "--strict reports a failed govulncheck" "$?" "1"
SS_OUT="$T/lib"

# Results dir inside the repo is refused.
"$scan" "$here/../docs/vo-scan-should-not-exist" --only fuses >"$T/o4.log" 2>&1
eq "results dir inside the repo is refused" "$?" "2"
if [ ! -e "$here/../docs/vo-scan-should-not-exist" ]; then ok "refused results dir is not created"; else bad "refused results dir was created"; rm -rf "$here/../docs/vo-scan-should-not-exist"; fi
"$scan" "docs/vo-scan-relative-should-not-exist/a/b" --only fuses >"$T/o4b.log" 2>&1
eq "relative path into the repo is refused" "$?" "2"
[ ! -e "$here/../docs/vo-scan-relative-should-not-exist" ] && ok "refused relative dir is not created" || { bad "relative dir created"; rm -rf "$here/../docs/vo-scan-relative-should-not-exist"; }

# Semgrep runs with metrics off and a read-only source mount (stub docker records argv).
cat >"$T/bin/docker" <<'S'
#!/bin/sh
printf '%s\n' "$@" >"$DOCKER_ARGS"
exit 0
S
chmod +x "$T/bin/docker"
DOCKER_ARGS="$T/docker.args" PATH="$T/bin:$PATH" "$scan" "$T/o5" --only semgrep >"$T/o5.log" 2>&1
grep -qx -- '--metrics=off' "$T/docker.args" && ok "semgrep --metrics=off" || bad "semgrep --metrics=off missing"
grep -qx 'SEMGREP_SEND_METRICS=off' "$T/docker.args" && ok "SEMGREP_SEND_METRICS=off" || bad "SEMGREP_SEND_METRICS=off missing"
grep -q ':/src:ro$' "$T/docker.args" && ok "semgrep source mount is read-only" || bad "source mount not read-only"
grep -q '^semgrep/semgrep:[0-9.]*@sha256:' "$T/docker.args" && ok "semgrep image pinned by tag and digest" || bad "semgrep image not pinned"

# Every Go tool is pinned.
if grep -hE '@(latest|master|main)\b' "$here"/security-scan "$here"/security-scan.d/*.sh | grep -v '^#'; then bad "unpinned tool"; else ok "no unpinned tool references"; fi

exit "$fail"
