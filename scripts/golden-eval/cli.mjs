// Wiring of the live modes to the real world: processes, files, the clock.
import { appendFileSync, existsSync, mkdirSync, readdirSync, readFileSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import { sh as realSh } from "./eval-repo.mjs";
import { evalProjectId, guarded, preflight, runAll, runId } from "./live.mjs";
import { setupProjects } from "./setup.mjs";

/** Claude Code keeps a worktree's transcripts in <projects>/<path with / and . as ->/*.jsonl; null when none is found. */
export function findTranscript(roots, worktreePath) {
	const munged = worktreePath.replace(/[/.]/g, "-");
	const parts = [];
	for (const root of roots) {
		const dir = join(root, munged);
		if (existsSync(dir)) for (const f of readdirSync(dir).filter((n) => n.endsWith(".jsonl")).sort()) parts.push(readFileSync(join(dir, f), "utf8"));
	}
	return parts.length ? parts.join("\n") : undefined;
}

export async function cliMain({ mode, a, items, runs, concurrency }) {
	const evalRoot = a.get("eval-root");
	if (!evalRoot) {
		console.error("--eval-root <dir> is required (where the pruned eval repos live)");
		return 2;
	}
	const sh = guarded(realSh);
	try {
		preflight({ projects: runs.map(evalProjectId) });
		if (mode === "setup") {
			const src = a.get("src") ?? realSh("git", ["rev-parse", "--path-format=absolute", "--git-common-dir"]).stdout.trim();
			const ids = setupProjects({
				runs, items, src, evalRoot, ctx: { sh: realSh, exists: existsSync },
				config: { harness: a.get("harness") ?? "claude-code", workerModel: a.get("worker-model"), plannerModel: a.get("planner-model"), permission: a.get("permission") },
			});
			console.log(`registered ${ids.length} eval projects: ${ids.join(", ")}`);
			return 0;
		}
		const out = a.get("out") ?? "golden-eval-out";
		mkdirSync(out, { recursive: true });
		const roots = (a.get("claude-projects") ?? join(homedir(), ".claude", "projects")).split(",");
		const dataDir = a.get("data-dir") ?? process.env.TALLPINE_DATA_DIR ?? join(homedir(), ".tallpine", "data");
		const ctx = {
			sh, outDir: out, delayMin: Number(a.get("delay-min") ?? 70), pollMs: Number(a.get("poll-sec") ?? 60) * 1000,
			maxMs: Number(a.get("max-hours") ?? 8) * 3600000, now: () => Date.now(), sleep: (ms) => new Promise((r) => setTimeout(r, ms)),
			projectFor: evalProjectId, cloneFor: (run) => join(evalRoot, runId(run)),
			fs: {
				exists: existsSync, read: async (p) => readFileSync(p, "utf8"), write: async (p, t) => writeFileSync(p, t),
				append: async (p, t) => appendFileSync(p, t), list: async (p) => readdirSync(p), mkdir: async (p) => mkdirSync(p, { recursive: true }),
			},
			findTranscript: (sid, project) => findTranscript(roots, join(dataDir, "worktrees", project, sid)),
		};
		const { rows, failures } = await runAll({ runs, items, ctx, concurrency });
		console.log(`done: ${rows.length} rows in ${join(out, "rows.jsonl")}, ${failures.length} failed${failures.length ? ` (see ${join(out, "failures.jsonl")})` : ""}`);
		return failures.length ? 1 : 0;
	} catch (e) {
		console.error(String(e.message ?? e));
		return 2;
	}
}
