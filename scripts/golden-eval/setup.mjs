// Setup: one pruned base repo per item (see eval-repo.mjs), built once, and one
// eval project per run (item x arm x rep) on its own local clone of that base
// (hardlinked objects, so disk use is one pruned store per item), registered
// with the daemon. No run can read another's branches. Idempotent.
import { join } from "node:path";
import { buildBaseRepo, checkBaseRepo, cloneRunRepo } from "./eval-repo.mjs";
import { evalProjectConfig, evalProjectId, guarded, runId } from "./live.mjs";

/**
 * Builds and registers the eval projects. `ctx.sh` runs commands, `ctx.exists`
 * tells whether a path is there. Returns the project ids.
 */
export function setupProjects({ runs, items, ctx, src, evalRoot, config }) {
	const sh = guarded(ctx.sh);
	const ids = [];
	for (const run of runs) {
		const item = items.find((i) => i.ref === run.ref);
		const baseDir = join(evalRoot, `base-${item.ref}`);
		const dir = join(evalRoot, runId(run));
		const id = evalProjectId(run);
		if (!ctx.exists(baseDir)) buildBaseRepo({ src, dir: baseDir, base: item.baseCommit, run: sh });
		checkBaseRepo({ dir: baseDir, base: item.baseCommit, run: sh });
		if (!ctx.exists(dir)) cloneRunRepo({ base: baseDir, dir, run: sh });
		checkBaseRepo({ dir, base: item.baseCommit, run: sh });
		const known = sh("tallpine", ["project", "get", id, "--json"]);
		if (known.code !== 0) {
			const add = sh("tallpine", ["project", "add", "--path", dir, "--id", id, "--name", `Golden eval ${runId(run)}`]);
			if (add.code !== 0) throw new Error(`project add ${id} failed: ${(add.stderr || add.stdout).trim()}`);
		}
		const set = sh("tallpine", ["project", "set-config", id, "--config-json", JSON.stringify(evalProjectConfig(config)), "--json"]);
		if (set.code !== 0) throw new Error(`set-config ${id} failed: ${(set.stderr || set.stdout).trim()}`);
		ids.push(id);
	}
	return ids;
}
