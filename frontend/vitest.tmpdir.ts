import { statSync } from "node:fs";
import path from "node:path";

/**
 * B462: under the Claude Code sandbox (Tallpine security mode) the shell's
 * TMPDIR is the shared /tmp/claude-<uid>, where unix-socket binds are denied;
 * only the per-session CLAUDE_CODE_TMPDIR allows them. Tests that listen on a
 * socket under os.tmpdir() then fail with EPERM. Point TMPDIR at the session
 * dir for the test run (vitest workers inherit the env). Unset outside the
 * sandbox, so nothing changes there.
 */
export function useSessionTmpdir(env: NodeJS.ProcessEnv = process.env): void {
	const dir = env.CLAUDE_CODE_TMPDIR?.trim();
	if (!dir || !path.isAbsolute(dir)) return;
	try {
		if (!statSync(dir).isDirectory()) return;
	} catch {
		return;
	}
	env.TMPDIR = dir;
}
