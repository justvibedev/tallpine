# shellcheck shell=bash
# govulncheck and gosec on backend/, run with the repo's pinned Go toolchain
# (scripts/go). `go run pkg@version` resolves the tool through the module proxy
# and verifies it against the Go checksum database (sum.golang.org).

GOVULNCHECK=golang.org/x/vuln/cmd/govulncheck@v1.9.0
GOSEC=github.com/securego/gosec/v2/cmd/gosec@v2.30.0

ss_gotool() { # <args...>, run from backend/ with a scrubbed, telemetry-free env
	(cd "$SS_REPO/backend" && env GOTOOLCHAIN=local GOFLAGS=-buildvcs=false GOTELEMETRY=off \
		PATH="$SS_REPO/scripts/bin:$PATH" HOME="$HOME" "$SS_REPO/scripts/go" "$@")
}

ss_run_govulncheck() {
	local raw="$SS_OUT/govulncheck.json" c
	if ! ss_gotool run "$GOVULNCHECK" -format json ./... >"$raw" 2>"$SS_OUT/govulncheck.err"; then
		# govulncheck -format json exits 0 even with findings; non-zero means it failed.
		ss_state govulncheck_error 1
		ss_summary govulncheck "ERROR (see govulncheck.err)"
		return 0
	fi
	c="$(ss_parse_govulncheck "$raw")"
	ss_state govulncheck_reachable "$(ss_kv "$c" reachable)"
	ss_summary govulncheck "$c"
}

ss_run_gosec() {
	local raw="$SS_OUT/gosec.json" c
	# -no-fail: report-only. -exclude-generated skips sqlc/oapi output.
	ss_gotool run "$GOSEC" -no-fail -exclude-generated -fmt json -out "$raw" ./... >"$SS_OUT/gosec.err" 2>&1 || true
	if [ ! -s "$raw" ]; then
		ss_summary gosec "ERROR (see gosec.err)"
		return 0
	fi
	ss_summary gosec "$(ss_parse_gosec "$raw")"
}
