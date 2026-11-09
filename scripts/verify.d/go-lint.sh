# Sourced by scripts/verify: gofmt, package selection and golangci-lint (whole tree, or changed-only in --changed / --lint-changed).
go_fmt() {
	local out
	if [ "$mode" = changed ] || [ "$narrow" = 1 ]; then
		local files
		files="$(printf '%s\n' "$changes" | grep -E '^backend/.*\.go$' | while read -r f; do [ -f "$f" ] && echo "$f"; done)"
		[ -z "$files" ] && return 0
		# shellcheck disable=SC2086
		out="$(gofmt -l $files)"
	else
		out="$(cd backend && gofmt -l .)"
	fi
	[ -z "$out" ] || {
		echo "need gofmt:"
		echo "$out"
		return 1
	}
}

# Go packages to test: --changed tests the packages whose files changed (by
# directory); --full tests ./... .
go_packages() {
	if [ "$mode" != changed ] && [ "$narrow" = 0 ]; then
		echo "./..."
		return
	fi
	printf '%s\n' "$changes" | grep -E '^backend/.*\.go$' | while read -r f; do
		d="$(dirname "${f#backend/}")"
		[ -d "backend/$d" ] && ls "backend/$d"/*.go >/dev/null 2>&1 && echo "./$d"
	done | sort -u
}

# lint_config_changed: the lint config or tool version changed, so changed-only linting would miss
# findings in untouched packages: lint everything.
lint_config_changed() {
	printf '%s\n' "$changes" | grep -Eq '^backend/\.golangci\.ya?ml$|^backend/go\.(mod|sum)$|^scripts/go$|^scripts/verify(\.d/go-lint\.sh)?$'
}

go_lint() {
	# Same flags as the local-ci go-lint job (~/.tallpine/local-ci/loop.sh lintrun), so a pass here is a pass there.
	local args=(run --allow-parallel-runners --path-mode=abs --concurrency=2)
	if { [ "$mode" = changed ] || [ "$narrow" = 1 ]; } && ! lint_config_changed; then
		local pkgs
		pkgs="$(go_packages)"
		[ -z "$pkgs" ] && return 0
		# No --new-from-rev: unparam/unused report at the declaration, which a PR that only adds callers
		# never touches (B556), so lint the touched packages whole; main is kept clean by the full lint.
		# shellcheck disable=SC2206
		args+=($pkgs)
	fi
	[ "${#args[@]}" -eq 4 ] && args+=(./...)
	[ -n "${VERIFY_PRINT_LINT_ARGS:-}" ] && { echo "${args[*]}"; return 0; }
	(cd backend && GOTOOLCHAIN=local with_slot env GOMAXPROCS="$LINT_CORES" nice -n "$NICE" go run "$GOLANGCI" "${args[@]}")
}

