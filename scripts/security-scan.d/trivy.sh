# shellcheck shell=bash
# trivy: CycloneDX SBOM (and a vulnerability count) for the repo snapshot, and
# with --app the same for the built app bundle. Trivy sends no telemetry; its
# vulnerability DB is downloaded, nothing is uploaded.

TRIVY_VERSION=0.70.0

ss_trivy_one() { # <label> <subcommand> <target>
	local label="$1" sub="$2" target="$3" c
	trivy "$sub" --quiet --scanners vuln --skip-dirs node_modules --skip-dirs .git \
		--format json --output "$SS_OUT/trivy-$label.json" "$target" >"$SS_OUT/trivy-$label.err" 2>&1 || true
	trivy "$sub" --quiet --skip-dirs node_modules --skip-dirs .git \
		--format cyclonedx --output "$SS_OUT/sbom-$label.cdx.json" "$target" >>"$SS_OUT/trivy-$label.err" 2>&1 || true
	if [ ! -s "$SS_OUT/trivy-$label.json" ] || [ ! -s "$SS_OUT/sbom-$label.cdx.json" ]; then
		ss_summary "trivy-$label" "ERROR (see trivy-$label.err)"
		return 0
	fi
	c="$(ss_parse_trivy "$SS_OUT/trivy-$label.json")"
	ss_summary "trivy-$label" "$c components=$(jq '.components | length' "$SS_OUT/sbom-$label.cdx.json")"
}

ss_run_trivy() {
	ss_tool_missing trivy trivy && return 0
	local ver
	ver="$(trivy --version | sed -n 's/^Version: //p')"
	[ "$ver" = "$TRIVY_VERSION" ] || ss_log "trivy $ver found, pinned $TRIVY_VERSION"
	ss_trivy_one repo fs "$SS_SNAPSHOT"
	[ -n "$SS_APP" ] && ss_trivy_one app rootfs "$SS_APP"
	return 0
}
