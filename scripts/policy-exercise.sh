#!/bin/sh
# End-to-end exercise of the enterprise agent policy (B45): a real daemon and a
# real agent session on scratch state, once with no policy (the default) and
# once with the managed file turning on `githubCreds=helper` and
# `envFilter=allowlist`. It never touches ~/.tallpine, the running app or its tmux
# server: state lives in a temp dir, the daemon listens on a free port and tmux
# uses a private socket.
#
#   scripts/policy-exercise.sh [--bin <dev-build-of-tallpine>] [--keep]
#
# The agent is scripts/policy-exercise.probe.sh standing in for `claude`; it
# records what the session sees (variable names only, no values). Exit 0 only if
# every expectation holds. Needs tmux, git and a dev build of tallpine (built with
# scripts/go when --bin is not given; release builds ignore the managed-file
# override this script relies on).
set -eu

here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/.." && pwd)
tallpine=""
keep=0
while [ $# -gt 0 ]; do
	case $1 in
	--bin) tallpine=$2; shift 2 ;;
	--keep) keep=1; shift ;;
	*) echo "usage: $0 [--bin <binary>] [--keep]" >&2; exit 2 ;;
	esac
done

top=$(mktemp -d "${TMPDIR:-/tmp}/vo-policy-exercise.XXXXXX")
daemon_pid=""
socket="vo-exercise-$$"
# kill_hosts ends every detached pty-host / chat-host / agent-process supervisor
# that belongs to this run. They outlive the daemon by design, so match their
# command line on the temp dir (their workspace and data paths live under it,
# as the physical path on macOS) and nothing else.
kill_hosts() {
	phys=$(cd "$top" 2>/dev/null && pwd -P) || phys=$top
	for p in "$top" "$phys"; do
		pkill -TERM -f -- "$p/" 2>/dev/null || true
	done
	sleep 0.5
	for p in "$top" "$phys"; do
		pkill -KILL -f -- "$p/" 2>/dev/null || true
	done
}
cleanup() {
	[ -n "$daemon_pid" ] && kill "$daemon_pid" 2>/dev/null || true
	tmux -L "$socket" kill-server 2>/dev/null || true
	kill_hosts
	[ "$keep" = 1 ] && echo "kept $top" || rm -rf "$top"
}
trap cleanup EXIT INT TERM

if [ -z "$tallpine" ]; then
	tallpine=$top/tallpine
	(cd "$repo/backend" && "$repo/scripts/go" build -o "$tallpine" ./cmd/tallpine)
fi
failures=0
fail() { echo "  FAIL: $1"; failures=$((failures + 1)); }
expect() { # expect <phase> <probe> <want-regex>
	got=$(awk -F'\t' -v k="$2" '$1 == k { print $2 }' "$top/$1/results.tsv")
	if printf '%s' "$got" | grep -Eq "$3"; then echo "  ok   [$1] $2: $got"; else fail "[$1] $2: got '$got', want /$3/"; fi
}

expect_not() { # expect_not <phase> <probe> <unwanted-regex>
	got=$(awk -F'\t' -v k="$2" '$1 == k { print $2 }' "$top/$1/results.tsv")
	if printf '%s' "$got" | grep -Eq "$3"; then fail "[$1] $2: got '$got', must not match /$3/"; else echo "  ok   [$1] $2: $got"; fi
}

# expect_as_default <probe>: a tool must behave under the policy as it does
# without one (a broken tool on this machine fails in both phases).
expect_as_default() {
	want=$(awk -F'\t' -v k="$1" '$1 == k { print $2 }' "$top/default/results.tsv")
	expect policy "$1" "^$want\$"
}

free_port() { python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])'; }

# run_phase <name> <managed-json or empty>
run_phase() {
	name=$1 managed=$2
	root=$top/$name
	mkdir -p "$root/bin" "$root/real" "$root/home" "$root/data"
	cp "$here/policy-exercise.probe.sh" "$root/bin/claude"
	# A stand-in "real" gh: shows whether a token reached it.
	cat >"$root/real/gh" <<'GH'
#!/bin/sh
# Like the real gh, fail when there is no token.
[ -n "${GH_TOKEN:-}" ] || { echo "gh: not logged in" >&2; exit 1; }
echo "gh-ran token=present"
GH
	chmod +x "$root/real/gh"
	tok="exercise-token-$$-$name"
	printf '%s' "$tok" >"$root/expected-token"
	git init -q --bare -b main "$root/origin.git"
	git init -q -b main "$root/repo"
	git -C "$root/repo" -c user.email=x@example.com -c user.name=x commit -q --allow-empty -m init
	git -C "$root/repo" remote add origin "$root/origin.git"
	git -C "$root/repo" push -q origin main
	git -C "$root/repo" remote set-head origin main >/dev/null 2>&1 || true
	[ -n "$managed" ] && printf '%s' "$managed" >"$root/managed.json"

	port=$(free_port)
	# The daemon's own environment: decoy secrets an agent must not inherit.
	denv() {
		env -i HOME="$root/home" PATH="$root/bin:$root/real:$PATH" SHELL="${SHELL:-/bin/sh}" \
			TALLPINE_DATA_DIR="$root/data" TALLPINE_RUN_FILE="$root/running.json" TALLPINE_PORT="$port" \
			TALLPINE_TMUX_SOCKET_NAME="$socket" TALLPINE_TELEMETRY_REMOTE=off \
			${managed:+TALLPINE_MANAGED_POLICY_FILE="$root/managed.json"} \
			TALLPINE_GITHUB_TOKEN="$tok" AWS_SECRET_ACCESS_KEY=decoy DATABASE_URL=decoy STRIPE_KEY=decoy \
			MY_CORP_PASSWORD=decoy "$@"
	}
	denv "$tallpine" daemon >"$root/daemon.log" 2>&1 &
	daemon_pid=$!
	i=0
	until denv "$tallpine" status --json 2>/dev/null | grep -q '"state": "ready"'; do
		i=$((i + 1)); [ $i -gt 150 ] && { cat "$root/daemon.log"; echo "daemon not ready"; exit 1; }
		sleep 0.2
	done
	denv "$tallpine" project add --path "$root/repo" --name exercise >/dev/null
	pid=$(denv "$tallpine" project ls --json 2>/dev/null | python3 -c 'import json,sys; d=json.load(sys.stdin); d=d.get("projects",d) if isinstance(d,dict) else d; print(d[0]["id"])')
	out=$(denv "$tallpine" spawn --project "$pid" --name exercise --harness claude-code --mode tui --prompt hi --skip-agent-check 2>&1) || { echo "$out"; exit 1; }
	sid=$(printf '%s\n' "$out" | grep -Eo '[a-z0-9-]+-[0-9]+' | head -1)
	i=0
	until [ -e "$root/stage1" ]; do
		i=$((i + 1)); [ $i -gt 300 ] && { echo "$out"; tail -20 "$root/daemon.log"; echo "probe did not run"; exit 1; }
		sleep 0.2
	done
	denv "$tallpine" policy show --session "$sid" >"$root/policy-show.txt" 2>&1 || true
	denv "$tallpine" stop >/dev/null 2>&1 || true
	wait "$daemon_pid" 2>/dev/null || true
	daemon_pid=""
	touch "$root/stage2"
	i=0
	until [ -e "$root/stage3" ]; do
		i=$((i + 1)); [ $i -gt 100 ] && { echo "daemon-down probe did not finish"; break; }
		sleep 0.2
	done
	tmux -L "$socket" kill-server 2>/dev/null || true
	kill_hosts
}

echo "== phase 1: no policy (default). Control: the probe must see the leak."
run_phase default ""
expect default env_secret_names 'DATABASE_URL.*TALLPINE_GITHUB_TOKEN|TALLPINE_GITHUB_TOKEN.*DATABASE_URL'
expect default git_push '^ok$'

echo "== phase 2: managed file sets githubCreds=helper, envFilter=allowlist"
run_phase policy '{"schemaVersion":2,"rules":{"githubCreds":{"mode":"helper"},"envFilter":{"mode":"allowlist"}}}'
# Kept on purpose: the session's own VO_* variables and the harness login
# variables (AWS_* for Bedrock); see "Agent environment" in docs/agent-policy.md.
expect_not policy env_secret_names 'DATABASE_URL|STRIPE_KEY|MY_CORP_PASSWORD|TALLPINE_GITHUB_TOKEN|(^| )(GH|GITHUB)_TOKEN'
expect default gh_resolves_to '^real/gh$'
expect_not policy gh_resolves_to '^real/gh$'
expect policy gh_call 'token=present'
expect policy git_credential '^daemon-token$'
expect policy git_credential_other_host '^refused$'
expect policy git_push '^ok$'
expect_as_default ssh
expect_as_default node
expect_as_default go
expect policy down_git_credential '^refused$'
expect policy down_gh '^refused$'
if grep -q "DATABASE_URL" "$top/policy/policy-show.txt" && grep -q "STRIPE_KEY" "$top/policy/policy-show.txt" && grep -q "envFilter" "$top/policy/policy-show.txt"; then
	echo "  ok   decision record names the dropped variables"
else fail "decision record (policy-show.txt) missing envFilter or dropped names"; fi
if grep -rq "$(cat "$top/policy/expected-token")" "$top/policy/policy-show.txt" "$top/policy/daemon.log"; then fail "token value appears in the decision record or daemon log"; else echo "  ok   no token value in the decision record or daemon log"; fi

echo
if [ $failures -eq 0 ]; then echo "policy exercise: PASS"; else echo "policy exercise: $failures FAILED"; fi
[ $failures -eq 0 ]
