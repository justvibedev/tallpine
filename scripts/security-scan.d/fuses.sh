# shellcheck shell=bash
# Read the Electron fuses of a built app (read-only; never changes them).

FUSES_PKG=@electron/fuses@2.1.3

ss_run_fuses() {
	ss_tool_missing fuses npx && return 0
	local raw="$SS_OUT/fuses.txt" c off
	# Run from the results dir so npx never picks up a repo package.json.
	(cd "$SS_OUT" && npx --yes "$FUSES_PKG" read --app "$SS_APP") >"$raw" 2>"$SS_OUT/fuses.err" || true
	if ! grep -q ' is \(Enabled\|Disabled\)' "$raw"; then
		ss_summary fuses "ERROR (see fuses.err)"
		return 0
	fi
	c="$(ss_parse_fuses "$raw")"
	off="$(sed -n 's/^ *\(.*[^ ]\) is Disabled *$/\1/p' "$raw" | tr '\n' ',' | sed 's/,$//')"
	ss_summary fuses "$c off=[$off]"
}
