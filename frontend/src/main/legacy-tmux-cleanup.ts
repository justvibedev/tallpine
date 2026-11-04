import { readdir, rm, rmdir } from "node:fs/promises";
import path from "node:path";
import { HOME_DIR_NAME } from "../shared/brand";

/** Before the data-dir unification, packaged tmux was staged under ~/.tallpine/runtime/tmux. */
export function legacyStagedTmuxRoot(homeDir: string): string {
	return path.join(homeDir, HOME_DIR_NAME, "runtime", "tmux");
}

/**
 * The legacy root to reclaim for this instance, or null when nothing moved.
 * With TALLPINE_DATA_DIR set the old staging root was already
 * `$TALLPINE_DATA_DIR/runtime/tmux` (same as the new one), so the shared
 * ~/.tallpine/runtime/tmux belongs to another install and must not be touched.
 */
export function legacyStagedTmuxRootFor(env: Record<string, string | undefined>, homeDir: string): string | null {
	return env.TALLPINE_DATA_DIR?.trim() ? null : legacyStagedTmuxRoot(homeDir);
}

export interface LegacyTmuxCleanupDeps {
	readdir: (dir: string) => Promise<string[]>;
	rm: (target: string) => Promise<void>;
	rmdir: (dir: string) => Promise<void>;
}

const realDeps: LegacyTmuxCleanupDeps = {
	readdir: (dir) => readdir(dir),
	rm: (target) => rm(target, { recursive: true, force: true }),
	rmdir: (dir) => rmdir(dir),
};

/**
 * Remove old staged tmux version dirs. Call only once the new copy is staged
 * and a daemon this app spawned (so it uses the new path) is running.
 * Never touches the new staging root, nor any dir holding `inUseBinary` (the
 * binary of a daemon that may still run from the old path). Returns the dirs removed.
 */
export async function removeLegacyStagedTmux(
	legacyRoot: string,
	newBinary: string,
	inUseBinary: string | null = null,
	deps: LegacyTmuxCleanupDeps = realDeps,
): Promise<string[]> {
	const newRoot = path.resolve(path.dirname(path.dirname(newBinary)));
	if (path.resolve(legacyRoot) === newRoot) return [];
	let entries: string[];
	try {
		entries = await deps.readdir(legacyRoot);
	} catch {
		return [];
	}
	const removed: string[] = [];
	for (const entry of entries) {
		const dir = path.join(legacyRoot, entry);
		if (inUseBinary && path.resolve(inUseBinary).startsWith(path.resolve(dir) + path.sep)) continue;
		if (path.resolve(dir) === path.resolve(path.dirname(newBinary))) continue;
		await deps.rm(dir);
		removed.push(dir);
	}
	// Drop the now-empty parents; rmdir refuses non-empty directories.
	for (const parent of [legacyRoot, path.dirname(legacyRoot)]) {
		try {
			await deps.rmdir(parent);
		} catch {
			break;
		}
	}
	return removed;
}
