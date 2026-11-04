# shellcheck shell=bash
# osv-scanner on every tracked lockfile. Runs against local copies of the OSV
# databases (--offline-vulnerabilities), so only the public advisory data is
# downloaded; package names and versions are not sent to any API.

OSV_VERSION=2.3.8

ss_lockfiles() { # tracked lockfiles / go.mod, one per line, relative to the repo
	git -C "$SS_REPO" ls-files -- '*package-lock.json' '*go.mod' '*pnpm-lock.yaml' '*yarn.lock' \
		'*Cargo.lock' '*poetry.lock' '*requirements.txt' '*Gemfile.lock' '*Podfile.lock'
}

ss_run_osv() {
	if ss_tool_missing osv-scanner osv-scanner; then
		ss_state osv_error 1
		return 0
	fi
	local ver raw="$SS_OUT/osv-scanner.json" args=() f c
	ver="$(osv-scanner --version 2>&1 | sed -n 's/^osv-scanner version: //p')"
	[ "$ver" = "$OSV_VERSION" ] || ss_log "osv-scanner $ver found, pinned $OSV_VERSION"
	ss_lockfiles >"$SS_OUT/lockfiles.txt"
	while IFS= read -r f; do args+=(-L "$SS_REPO/$f"); done <"$SS_OUT/lockfiles.txt"
	# Exit 1 means "vulnerabilities found"; anything else without output is an error.
	(cd "$SS_REPO" && osv-scanner scan source --offline-vulnerabilities --download-offline-databases \
		--format json --output-file "$raw" "${args[@]}") >"$SS_OUT/osv-scanner.err" 2>&1 || true
	if [ ! -s "$raw" ]; then
		ss_state osv_error 1
		ss_summary osv-scanner "ERROR (see osv-scanner.err)"
		return 0
	fi
	c="$(ss_parse_osv "$raw")"
	ss_state osv_critical "$(ss_kv "$c" critical)"
	ss_state osv_high "$(ss_kv "$c" high)"
	ss_summary osv-scanner "$c lockfiles=$(wc -l <"$SS_OUT/lockfiles.txt" | tr -d ' ')"
}
