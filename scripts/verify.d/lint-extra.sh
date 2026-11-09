# Sourced by scripts/verify: the steps --lint adds after go-lint.
# make lint (--lint) runs the whole-tree size-ratchet; --lint-changed (make lint-changed) runs it on changed
# files only and adds the frontend typecheck when frontend files changed (tsgo, same program as tsc).
lint_extra_steps() {
	if [ "$narrow" != 1 ]; then
		step size-ratchet bash scripts/size-ratchet
		return
	fi
	step size-ratchet bash scripts/size-ratchet --changed --base "$base"
	# Changed shell files must pass heredoc-guard before merge (B613). Pass every changed file: the guard
	# applies its own shebang filter, as in its full scan, so no path regex can drift from it.
	local existing
	existing="$(printf '%s\n' "$changes" | while read -r f; do [ -f "$f" ] && echo "$f"; done)"
	# shellcheck disable=SC2086
	[ -z "$existing" ] || step heredoc-guard bash scripts/heredoc-guard.test.sh $existing
	touched "$fe_re" || return 0
	if [ -x frontend/node_modules/.bin/tsgo ] && grep -q '"typecheck:fast"' frontend/package.json; then
		step fe-tsgo fe_typecheck npm run -s typecheck:fast -- --incremental false
		step fe-tsgo-e2e fe_typecheck npx tsgo --noEmit -p tsconfig.e2e.json
	else
		step fe-tsc fe_typecheck npm run -s typecheck
		step fe-tsc-e2e fe_typecheck npm run -s typecheck:e2e
	fi
}
