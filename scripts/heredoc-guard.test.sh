#!/usr/bin/env bash
# Guards against the macOS small-pipe deadlock (B22/B23): bash writes a heredoc
# or here-string to the child through a pipe before the child reads it. When
# macOS shrinks new pipes to 512 bytes under memory pressure, a larger body blocks
# forever. Keep every heredoc under the limit, and never feed unbounded or
# expanded text through a heredoc or <<<; use printf, a file, or < <(cmd).
#
#   scripts/heredoc-guard.test.sh [file...]    # default: every tracked shell script (git ls-files)
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
limit=400
files=()
if (($# > 0)); then
	files=("$@")
else
	while IFS= read -r f; do
		f="${root}/${f}"
		[[ -f "${f}" && "${f}" != */heredoc-guard.test.sh ]] || continue
		# No forks: this loop visits every tracked file (thousands), so read the shebang in-process.
		first=""
		{ IFS= read -r first <"${f}"; } 2>/dev/null || true
		[[ "${first}" == "#!"* && ("${first}" == *sh || "${first}" =~ env\ (ba)?sh) ]] && files+=("${f}")
	done < <(git -C "${root}" ls-files)
fi

fail=0
for f in "${files[@]}"; do
	[[ -f "${f}" ]] || continue
	out="$(awk -v F="${f}" -v LIMIT="${limit}" '
		function report(msg) { printf "%s:%d: %s\n", F, NR, msg; bad = 1 }
		inh {
			t = $0; sub(/^\t+/, "", t)
			if (t == tag) { if (n > LIMIT) printf "%s:%d: heredoc %s body is %d bytes (limit %d)\n", F, start, tag, n, LIMIT; inh = 0; next }
			n += length($0) + 1
			if (!quoted && $0 ~ /\$\(|`/) printf "%s:%d: heredoc %s expands a command; its size is unbounded\n", F, NR, tag
			next
		}
		/^[[:space:]]*#/ { next }
		/\(\(/ { next } # arithmetic shift, not a heredoc
		/<<</ { report("here-string; use < <(printf ...) or a file"); next }
		/<<-?[[:space:]]*\\?["\047]?[A-Za-z_]+/ {
			m = $0; sub(/.*<<-?[[:space:]]*/, "", m)
			quoted = (m ~ /^\\/ || m ~ /^["\047]/); gsub(/^\\|["\047]/, "", m); sub(/[^A-Za-z_].*/, "", m)
			tag = m; start = NR; n = 0; inh = 1
		}' "${f}")"
	if [[ -n "${out}" ]]; then
		printf 'FAIL %s\n' "${out}"
		fail=1
	fi
done
if ((fail)); then exit 1; fi
printf 'PASS heredoc-guard (%d files)\n' "${#files[@]}"
