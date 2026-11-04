#!/bin/sh
# Stand-in for the `claude` binary, used only by scripts/policy-exercise.sh.
# The daemon launches it as the agent of a real session, so everything it
# observes is what a real agent would see. It records one "name<TAB>result" line
# per probe into <phase>/results.tsv (names and booleans only, never a value)
# and exits when the exercise removes the phase's "stay" file.
case "${1:-}" in
-v | --version) echo "2.0.0 (Claude Code)"; exit 0 ;;
--session-id) ;; # the session launch
*) exit 0 ;; # the daemon's own probes (auth status, ...)
esac

export GIT_TERMINAL_PROMPT=0 # a missing helper must fail, not prompt on the tty
root=$(cd "$(dirname "$0")/.." && pwd)
res=$root/results.tsv
: >"$res"
note() { printf '%s\t%s\n' "$1" "$2" >>"$res"; }
have() { command -v "$1" >/dev/null 2>&1; }

# 1. Secret-looking variables in the environment: names only.
leaked=$(env | grep -iE '^[^=]*(token|secret|api_key|password|database_url|stripe)[^=]*=' | cut -d= -f1 | sort | tr '\n' ' ')
note env_secret_names "${leaked:-none}"

# 2. The shim and the credential helper.
note gh_resolves_to "$(command -v gh 2>/dev/null | sed "s#^$root/##")"
note gh_call "$(gh pr view 2>&1 | head -1)"
want=$(cat "$root/expected-token")
got=$(printf 'protocol=https\nhost=github.com\n\n' | git credential fill 2>/dev/null | sed -n 's/^password=//p')
if [ -z "$got" ]; then note git_credential none; elif [ "$got" = "$want" ]; then note git_credential daemon-token; else note git_credential other; fi
other=$(printf 'protocol=https\nhost=evil.example\n\n' | git credential fill 2>/dev/null | sed -n 's/^password=//p')
if [ -n "$other" ]; then note git_credential_other_host issued; else note git_credential_other_host refused; fi

# 3. Everyday tools still work.
git -c user.email=x@example.com -c user.name=x commit --allow-empty -qm probe >/dev/null 2>&1
if git push -q origin HEAD:refs/heads/exercise >/dev/null 2>&1; then note git_push ok; else note git_push FAILED; fi
for tool in "ssh -V" "node --version" "go version"; do
	set -- $tool
	if ! have "$1"; then note "$1" "not-on-PATH"; continue; fi
	if "$@" >/dev/null 2>&1; then note "$1" ok; else note "$1" FAILED; fi
done
touch "$root/stage1"

# 4. Daemon down: the helper and the shim must fail closed, nothing cached.
while [ ! -e "$root/stage2" ]; do sleep 0.2; done
if printf 'protocol=https\nhost=github.com\n\n' | git credential fill 2>/dev/null | grep -q '^password='; then note down_git_credential issued; else note down_git_credential refused; fi
if gh pr view >/dev/null 2>&1; then note down_gh ran-with-token; else note down_gh refused; fi
touch "$root/stage3"
sleep 600
