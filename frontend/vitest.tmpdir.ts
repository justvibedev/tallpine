import { mkdtempSync, readdirSync, rmSync, statSync } from "node:fs";
import { tmpdir } from "node:os";
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

// Short on purpose: tests bind unix sockets below this dir (sun_path is 104
// bytes on macOS).
const RUN_DIR_PREFIX = "tp-vt-";
// Far longer than any run, so a sweep never removes a concurrent run's dir.
const STALE_RUN_MS = 24 * 60 * 60 * 1000;

function sweepStaleRunDirs(base: string, now: number): void {
	let names: string[];
	try {
		names = readdirSync(base);
	} catch {
		return;
	}
	for (const name of names) {
		if (!name.startsWith(RUN_DIR_PREFIX)) continue;
		const dir = path.join(base, name);
		try {
			if (now - statSync(dir).mtimeMs > STALE_RUN_MS) rmSync(dir, { recursive: true, force: true });
		} catch {
			// Raced with its owner or another sweep; the next run retries.
		}
	}
}

/**
 * B602: tests make temp dirs under os.tmpdir() and most never remove them,
 * which left thousands of entries in /private/tmp. Give the whole run one
 * TMPDIR (vitest workers inherit the env) and remove it when vitest exits, so
 * a test cannot leak past its run. A run killed before exit leaves its dir;
 * the next run removes those once they are a day old.
 */
export function useRunScopedTmpdir(
	env: NodeJS.ProcessEnv = process.env,
	onExit: (cleanup: () => void) => void = (cleanup) => process.once("exit", cleanup),
	now: number = Date.now(),
): void {
	const base = env.TMPDIR?.trim() || tmpdir();
	if (path.basename(base).startsWith(RUN_DIR_PREFIX)) return; // nested vitest
	sweepStaleRunDirs(base, now);
	const dir = mkdtempSync(path.join(base, RUN_DIR_PREFIX));
	env.TMPDIR = dir;
	onExit(() => {
		try {
			rmSync(dir, { recursive: true, force: true });
		} catch {
			// Best-effort: cleanup must not fail a passing run. The next run sweeps it.
		}
	});
}

/** Vitest globalSetup entry: runs before workers start, so they inherit TMPDIR. */
export function setup(): void {
	useRunScopedTmpdir();
}
