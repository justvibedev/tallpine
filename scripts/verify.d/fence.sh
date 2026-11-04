# Sourced by scripts/verify and scripts/merge-gate: make the gate work inside the Claude
# security-mode fence (B463) without weakening any step. Outside it nothing changes.
#
# The fence's shell TMPDIR is the shared /tmp/claude-<uid>, where unix-socket binds are denied;
# only the per-session CLAUDE_CODE_TMPDIR allows them (Go tests that listen under t.TempDir()
# otherwise fail with EPERM). Its test dirs are too long for sun_path, so tallpine's socket
# helpers alias them under TALLPINE_SOCKET_ALIAS_ROOT, which must be a dir the fence lets us
# write: /tmp/claude.
# DEVELOPER_DIR makes the /usr/bin shims (git, clang) skip xcrun, whose cache dir the fence may
# not write (the fence allows it only for sessions started with the xcrun_db rule).
if [ -n "${CLAUDE_CODE_TMPDIR:-}" ] && [ -d "$CLAUDE_CODE_TMPDIR" ] && [ "${TMPDIR:-}" != "$CLAUDE_CODE_TMPDIR" ]; then
	export TMPDIR="$CLAUDE_CODE_TMPDIR"
	# Tests that need a PTY (denied by the fence on purpose) skip only when this is set and /dev/ptmx
	# cannot be opened (testenv keeps TALLPINE_TEST_* but scrubs CLAUDE_*).
	export TALLPINE_TEST_FENCED=1
	if [ -z "${TALLPINE_SOCKET_ALIAS_ROOT:-}" ] && [ -d /tmp/claude ] && [ -w /tmp/claude ]; then
		export TALLPINE_SOCKET_ALIAS_ROOT=/tmp/claude
	fi
fi
if [ -z "${DEVELOPER_DIR:-}" ] && command -v xcode-select >/dev/null 2>&1; then
	DEVELOPER_DIR="$(xcode-select -p 2>/dev/null)" && export DEVELOPER_DIR
fi
