# shellcheck shell=bash
# Semgrep Community Edition from a pinned Docker image. Free registry rulesets
# only (no login, no paid rules). Metrics and version checks are off; the only
# network traffic is fetching the public rules.

SEMGREP_IMAGE=semgrep/semgrep:1.181.1@sha256:3db5c7af55eb1f512eac0abf0ab69a97b52c74b5e9a27314c221aee1a78d7ab2
SEMGREP_RULESETS=(p/golang p/typescript p/javascript p/nodejs)

ss_run_semgrep() {
	ss_tool_missing semgrep docker && return 0
	local raw="$SS_OUT/semgrep.json" cfg=() r
	for r in "${SEMGREP_RULESETS[@]}"; do cfg+=(--config "$r"); done
	# Scan the tracked-file snapshot read-only; the results dir is the only writable mount.
	docker run --rm --init --label "tallpine.session=${TALLPINE_SESSION_ID:-security-scan}" \
		-e SEMGREP_SEND_METRICS=off -e SEMGREP_ENABLE_VERSION_CHECK=0 \
		-v "$SS_SNAPSHOT:/src:ro" -v "$SS_OUT:/out" -w /src "$SEMGREP_IMAGE" \
		semgrep scan --metrics=off --disable-version-check --quiet --json --output /out/semgrep.json \
		"${cfg[@]}" /src >"$SS_OUT/semgrep.err" 2>&1 || true
	if [ ! -s "$raw" ]; then
		ss_summary semgrep "ERROR (see semgrep.err)"
		return 0
	fi
	ss_summary semgrep "$(ss_parse_semgrep "$raw") rulesets=${SEMGREP_RULESETS[*]}"
}
