// Live replay: runs one golden item through one arm against an eval project,
// playing the owner-proxy (answers and approves a fixed delay after each wait),
// and records the run's events and artifacts. All side effects go through
// ctx so the loop is tested without a daemon: ctx.sh runs commands, ctx.now and
// ctx.sleep are the clock, ctx.fs is storage.
import { join } from "node:path";
import { buildRow } from "./collect.mjs";
import { armCommands, DELAY_MIN, matchAnswers, MAX_CONCURRENCY } from "./plan.mjs";

/** Commands a replay must never run: nothing here may push, open a PR or merge. */
export function refusedCommand(cmd, args) {
	if (cmd === "gh") return "gh is not used by eval runs";
	if (cmd === "git" && args.includes("push")) return "eval runs never push";
	if (cmd === "tallpine" && ["review", "merge", "pr"].includes(args[0])) return `tallpine ${args[0]} is not used by eval runs`;
	return "";
}

/** ctx.sh wrapped so a refused command throws before it runs. */
export function guarded(sh) {
	return (cmd, args, opts) => {
		const why = refusedCommand(cmd, args);
		if (why) throw new Error(why);
		return sh(cmd, args, opts);
	};
}

/** One id per run (item x arm x rep): <ref>-<arm>-r<rep>. */
export const runId = (run) => `${run.ref}-${run.arm}-r${run.rep}`;

/**
 * Every run gets its own eval project and pruned clone, so a run can never read
 * another arm's or an earlier attempt's branches. Anything not named eval-* is
 * refused so a run cannot touch a real project.
 */
export const evalProjectId = (run) => `eval-${runId(run).toLowerCase()}`;

/** Throws unless every project is an eval project. The daemon enforces who may answer and approve plans (the owner or the orchestrator). */
export function preflight({ projects }) {
	for (const p of projects) {
		if (!/^eval-[a-z0-9-]+$/.test(p)) throw new Error(`project ${p} is not an eval project (eval-<ref>)`);
	}
}

/**
 * Project config of an eval project: marked eval (the creating orchestrator may
 * then drive it and act as the owner's proxy), in-place on, models per arm role. Only
 * fields an orchestrator may set on a project it created (agentConfigKeys), so
 * the orchestrator can do the whole setup. The arm is explicit on every spawn,
 * so planningMode stays at its default.
 */
export function evalProjectConfig({ harness = "claude-code", workerModel, plannerModel, permission = "bypass-permissions" }) {
	const role = (model) => ({ agent: harness, agentConfig: { ...(model ? { model } : {}), permissions: permission } });
	return { defaultBranch: "main", planInPlace: true, eval: true, worker: role(workerModel), planner: role(plannerModel ?? workerModel) };
}

const iso = (ms) => new Date(ms).toISOString();

function tp(ctx, args, { json = false } = {}) {
	const r = ctx.sh("tallpine", args);
	if (r.code !== 0) throw new Error(`tallpine ${args.slice(0, 2).join(" ")} failed: ${(r.stderr || r.stdout).trim()}`);
	return json ? JSON.parse(r.stdout) : r.stdout;
}

const sessionOf = (out) => /spawned session (\S+)/.exec(out)?.[1];

async function load(ctx, path) {
	return ctx.fs.exists(path) ? JSON.parse(await ctx.fs.read(path)) : undefined;
}

/**
 * Runs one golden item through one arm. Resumable: events and state live in
 * <out>/<run id>/, and the owner-proxy delays count from the recorded event
 * times. Resolves to the result row.
 */
/** An outcome that ends a run for good (plan failed, implementer ended, deadline): its sessions are killed. */
class Terminal extends Error {}

/** A poll command that failed this many times in a row (a daemon restart lasts a few) ends the attempt, sessions left alive for a resume. */
const MAX_POLL_FAILURES = 5;

export async function runOne(args) {
	const held = {};
	let ended = false;
	try {
		const row = await runInner(args, held);
		ended = !held.finished;
		return row;
	} catch (e) {
		ended = e instanceof Terminal;
		throw e;
	} finally {
		// A run's sessions end with it once it is over (row written, or a terminal
		// outcome), so nothing keeps building past its slot and the 2-run cap holds.
		// A transient error leaves them alive so a resume can pick the run up.
		if (ended) for (const sid of new Set([held.state?.implementer, held.state?.planner, held.state?.author].filter(Boolean))) {
			const r = args.ctx.sh("tallpine", ["session", "kill", sid]);
			if (r.code !== 0) console.error(`could not end session ${sid}: ${(r.stderr || r.stdout).trim()}`);
		}
	}
}

async function runInner({ run, item, ctx }, held) {
	const dir = join(ctx.outDir, runId(run));
	await ctx.fs.mkdir(dir);
	const finished = await load(ctx, join(dir, "row.json"));
	if (finished) {
		held.finished = true; // a resumed run that already finished keeps its row; usage is not fetched again, nothing is killed
		return finished;
	}
	const eventsPath = join(dir, "events.json");
	const statePath = join(dir, "state.json");
	const events = (await load(ctx, eventsPath)) ?? [];
	const state = (await load(ctx, statePath)) ?? {};
	held.state = state;
	const project = ctx.projectFor(run);
	const clone = ctx.cloneFor(run);
	const delayMs = (ctx.delayMin ?? DELAY_MIN) * 60000;
	const save = async () => {
		await ctx.fs.write(eventsPath, JSON.stringify(events, null, 2));
		await ctx.fs.write(statePath, JSON.stringify(state, null, 2));
	};
	const at = (type) => events.find((e) => e.type === type);
	const mark = async (type) => {
		if (!at(type)) events.push({ at: iso(ctx.now()), type });
		await save();
	};
	const deadline = (at("spawned") ? Date.parse(at("spawned").at) : ctx.now()) + ctx.maxMs;

	if (!state.evalRef) {
		const out = tp(ctx, ["backlog", "add", item.title, "--project", project, "--note", item.note || "", "--done-when", item.doneWhen || "", "--scope", "local", "--open", "--json"]);
		state.evalRef = /"ref":\s*"(B\d+)"/.exec(out)?.[1];
		if (!state.evalRef) throw new Error(`could not read the new item's ref from: ${out.slice(0, 200)}`);
		await save();
	}
	const ref = state.evalRef;
	// An item from an earlier attempt may still sit in the inbox (spawn and plan
	// start refuse inbox items); accept it instead of adding a duplicate.
	const shown = tp(ctx, ["backlog", "show", ref, "--project", project, "--json"], { json: true });
	if ((shown.item ?? shown).state === "inbox") tp(ctx, ["backlog", "accept", ref, "--project", project]);
	const cmds = armCommands({ ...run, ref }, project);
	await mark("spawned"); // the arm's first step: planning time counts in both arms
	// Each plan command is recorded done only after it succeeded, so a failed or
	// interrupted start is retried on resume instead of polling a plan never started.
	for (let i = state.planDone ?? 0; i < cmds.plan.length; i++) {
		const c = cmds.plan[i];
		const out = tp(ctx, c);
		if (c[0] === "spawn") state.author = sessionOf(out);
		state.planDone = i + 1;
		await save();
	}

	// A failed poll is retried (daemon restarts); only the Nth failure in a row ends the attempt.
	const polled = async (fn) => {
		for (let n = 1; ; n++) {
			try {
				return fn();
			} catch (e) {
				if (n >= MAX_POLL_FAILURES) throw e;
				await ctx.sleep(ctx.pollMs);
			}
		}
	};
	let plan;
	for (;;) {
		plan = (await polled(() => tp(ctx, ["plan", "show", ref, "--project", project, "--json"], { json: true }))).plan;
		const now = ctx.now();
		if (plan.status === "failed" || plan.status === "superseded") throw new Terminal(`plan ${plan.status}: ${plan.failure ?? ""}`);
		if (plan.status === "awaiting_answers") {
			await mark("questions");
			if (!at("answered") && now >= Date.parse(at("questions").at) + delayMs) {
				const answers = { questions: matchAnswers(plan.questions ?? [], item.answers), corrections: {} };
				await ctx.fs.write(join(dir, "answers.json"), JSON.stringify(answers, null, 2));
				tp(ctx, ["plan", "answer", ref, "--project", project, "--file", join(dir, "answers.json")]);
				await mark("answered");
			}
		} else if (plan.status === "awaiting_approval") {
			await mark("plan");
			if (!at("approved") && now >= Date.parse(at("plan").at) + delayMs) {
				tp(ctx, ["plan", "approve", ref, "--project", project]);
				await mark("approved");
			}
		} else if (plan.status === "approved") {
			await mark("approved");
			break;
		}
		if (now > deadline) throw new Terminal("timed out waiting on the plan");
		await ctx.sleep(ctx.pollMs);
	}
	await ctx.fs.write(join(dir, "plan.json"), JSON.stringify(plan, null, 2));

	const planner = run.arm === "separate" ? plan.plannerSessionId : undefined;
	state.planner = planner;
	if (run.arm === "in-place") state.implementer = plan.plannerSessionId || state.author;
	else if (!state.implementer) {
		const kids = tp(ctx, ["backlog", "ls", "--parent", ref, "--project", project, "--state", "all", "--json"], { json: true }).items
			.map((i) => i.ref).sort((a, b) => Number(a.slice(1)) - Number(b.slice(1)));
		state.implementer = sessionOf(tp(ctx, cmds.build(kids)[0]));
	}
	await save();

	const session = () => polled(() => tp(ctx, ["session", "get", state.implementer, "--json"], { json: true }));
	let head;
	for (;;) {
		const s = await session();
		const sess = s.session ?? s;
		state.branch = sess.branch;
		state.harness = sess.harness;
		const log = ctx.sh("git", ["-C", clone, "log", "--reverse", "--format=%H%x09%cI", `${item.baseCommit}..${state.branch}`]);
		const first = log.code === 0 ? log.stdout.split("\n").find(Boolean) : undefined;
		if (first) {
			const [sha, when] = first.split("\t");
			head = sha;
			if (!at("head")) events.push({ at: when, type: "head" });
			break;
		}
		if (sess.isTerminated) throw new Terminal("the implementer ended without a commit");
		if (ctx.now() > deadline) throw new Terminal("timed out waiting for the first commit");
		await ctx.sleep(ctx.pollMs);
	}
	await save();

	const patch = ctx.sh("git", ["-C", clone, "diff", item.baseCommit, head]);
	await ctx.fs.write(join(dir, "first-head.patch"), patch.stdout);
	const usage = (sid) => (sid ? tp(ctx, ["session", "usage", sid, "--json"], { json: true }) : undefined);
	const transcript = ctx.findTranscript ? await ctx.findTranscript(state.implementer, project) : undefined;
	const row = buildRow({
		ref: run.ref, arm: run.arm, rep: run.rep, harness: state.harness, plannerUsage: usage(planner), implementerUsage: usage(state.implementer),
		plan: { plan }, events, transcript,
	});
	await ctx.fs.write(join(dir, "row.json"), JSON.stringify(row));
	return row;
}

/** Runs every run with at most `concurrency` (never above 2) in flight; a failed run is recorded, not fatal. */
export async function runAll({ runs, items, ctx, concurrency = MAX_CONCURRENCY }) {
	const rows = [];
	const failures = [];
	const queue = [...runs];
	const worker = async () => {
		for (let run = queue.shift(); run; run = queue.shift()) {
			const item = items.find((i) => i.ref === run.ref);
			try {
				const row = await runOne({ run, item, ctx });
				rows.push(row);
			} catch (e) {
				failures.push({ ...run, error: String(e.message ?? e) });
				await ctx.fs.append(join(ctx.outDir, "failures.jsonl"), JSON.stringify({ ...run, error: String(e.message ?? e) }) + "\n");
			}
		}
	};
	await Promise.all(Array.from({ length: Math.max(1, Math.min(concurrency, MAX_CONCURRENCY)) }, worker));
	// Rebuilt from every saved row (sorted by run id), so a resume never duplicates a row and a filtered resume never drops one.
	const saved = [];
	for (const name of (await ctx.fs.list(ctx.outDir)).sort()) {
		const row = await load(ctx, join(ctx.outDir, name, "row.json"));
		if (row) saved.push(row);
	}
	await ctx.fs.write(join(ctx.outDir, "rows.jsonl"), saved.map((r) => JSON.stringify(r) + "\n").join(""));
	return { rows, failures };
}
