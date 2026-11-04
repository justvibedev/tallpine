import assert from "node:assert/strict";
import { test } from "node:test";
import { evalProjectId, guarded, preflight, refusedCommand, runAll, runId, runOne } from "./live.mjs";
import { setupProjects } from "./setup.mjs";

const MIN = 60000;
const usage = { models: [{ model: "m", inputTokens: 10, cacheCreationTokens: 0, cacheReadTokens: 0, outputTokens: 5, turns: 1 }] };
const item = (ref, extra = {}) => ({ ref, kind: "single", baseCommit: "b".repeat(40), title: `Item ${ref}`, note: "note", doneWhen: "done", answers: [{ question: "who pays for the seat", answer: "The booker" }], decisions: "d", ...extra });

// A simulated daemon and clock. Plan: drafting 10 min, then awaiting_answers
// until answered, writing 10 min, awaiting_approval until approved; the
// implementer commits 30 min after it starts building.
function world({ split = false, failOn, failSpawnOnce = false, failShowOnce = false } = {}) {
	let t = Date.UTC(2026, 10, 3);
	const calls = [];
	const files = new Map();
	const items = new Map(); // eval ref -> state
	let n = 0;
	let active = 0;
	let maxActive = 0;
	const st = (ref) => items.get(ref);
	const planOf = (ref, arm) => {
		const s = st(ref);
		let status = "drafting";
		if (s.approvedAt) status = "approved";
		else if (s.answeredAt) status = t >= s.answeredAt + 10 * MIN ? "awaiting_approval" : "writing";
		else if (s.startedAt && t >= s.startedAt + 10 * MIN) status = "awaiting_answers";
		return { status, plannerSessionId: arm === "in-place" ? `impl-${ref}` : `plan-${ref}`, startedAt: new Date(s.startedAt).toISOString(), questions: [{ id: "q1", text: "Who pays for a shared seat?", options: [{ id: "a", label: "Each friend" }, { id: "b", label: "The booker" }], recommended: "a" }] };
	};
	const sh = (cmd, args) => {
		calls.push([cmd, ...args]);
		const ok = (stdout = "") => ({ code: 0, stdout, stderr: "" });
		const flag = (f) => args[args.indexOf(f) + 1];
		if (cmd === "git") {
			if (args.includes("diff")) return ok("patch");
			const ref = [...items.keys()].find((r) => args.join(" ").includes(`tp/impl-${r}`));
			const s = ref && st(ref);
			return ok(s?.buildAt && t >= s.buildAt + 30 * MIN ? `abc123\t${new Date(s.buildAt + 30 * MIN).toISOString()}\n` : "");
		}
		const [a, b] = args;
		if (a === "backlog" && b === "add") {
			if (failOn && args[2] === failOn) return { code: 1, stdout: "", stderr: "boom" };
			const ref = `B${++n}`;
			items.set(ref, { arm: undefined, open: args.includes("--open") });
			active++;
			maxActive = Math.max(maxActive, active);
			return ok(JSON.stringify({ item: { ref } }));
		}
		if (a === "backlog" && b === "show") return ok(JSON.stringify({ item: { ref: args[2], state: st(args[2]).open ? "open" : "inbox" } }));
		if (a === "backlog" && b === "accept") return (st(args[2]).open = true), ok();
		if (a === "backlog" && b === "ls") return ok(JSON.stringify({ items: split ? [{ ref: "B90" }, { ref: "B80" }] : [] }));
		if (a === "plan" && b === "start") {
			const s = st(args[args.length - 1]);
			if (!s.open) return { code: 1, stdout: "", stderr: `${args[args.length - 1]} is inbox: only open items can be planned (BACKLOG_ITEM_NOT_OPEN)` };
			Object.assign(s, { arm: "separate", startedAt: t });
			return ok();
		}
		if (a === "spawn") {
			if (failSpawnOnce) {
				failSpawnOnce = false;
				return { code: 1, stdout: "", stderr: "spawn down" };
			}
			const backlog = flag("--backlog");
			const plan = flag("--plan");
			// A separate-arm build spawn names a child ref the fake does not know: use the planned item.
			const s = plan === "separate" ? st([...items.keys()].find((r) => items.get(r).arm === "separate")) : st(backlog);
			if (!s.open) return { code: 1, stdout: "", stderr: `${backlog} is inbox: only open items can be started (BACKLOG_ITEM_NOT_OPEN)` };
			if (plan === "in-place") Object.assign(s, { arm: "in-place", startedAt: t, buildAt: undefined, inPlace: true });
			else s.buildAt = t;
			const ref = [...items.entries()].find(([, v]) => v === s)[0];
			return ok(`spawned session ${s.inPlace || plan === "separate" ? `impl-${ref}` : "?"}\n`);
		}
		if (a === "plan" && b === "show" && failShowOnce) {
			failShowOnce = false;
			return { code: 1, stdout: "", stderr: "daemon restarting" };
		}
		if (a === "plan" && b === "show") return ok(JSON.stringify({ plan: planOf(args[2], st(args[2]).arm) }));
		if (a === "plan" && b === "answer") return (st(args[2]).answeredAt = t), ok();
		if (a === "plan" && b === "approve") {
			const s = st(args[2]);
			s.approvedAt = t;
			if (s.inPlace) s.buildAt = t;
			return ok();
		}
		if (a === "session" && b === "get") return ok(JSON.stringify({ session: { branch: `tp/${args[2]}`, harness: "claude-code", isTerminated: false } }));
		if (a === "session" && b === "usage") return ok(JSON.stringify(usage));
		if (a === "session" && b === "kill") {
			if (String(args[2]).startsWith("impl-")) active--;
			return ok();
		}
		return { code: 1, stdout: "", stderr: `unexpected ${args.join(" ")}` };
	};
	const ctx = {
		sh: guarded(sh), outDir: "/out", delayMin: 70, pollMs: MIN, maxMs: 24 * 60 * MIN, now: () => t, sleep: async (ms) => { t += ms; },
		projectFor: evalProjectId, cloneFor: (r) => `/eval/${runId(r)}`,
		fs: { exists: (p) => files.has(p), read: async (p) => files.get(p), write: async (p, x) => void files.set(p, x), append: async (p, x) => void files.set(p, (files.get(p) ?? "") + x), mkdir: async () => {}, list: async (d) => [...new Set([...files.keys()].filter((k) => k.startsWith(d + "/")).map((k) => k.slice(d.length + 1).split("/")[0]))] },
	};
	return { ctx, calls, files, info: () => ({ maxActive }) };
}

const events = (w, id) => JSON.parse(w.files.get(`/out/${id}/events.json`));
const at = (ev, type) => Date.parse(ev.find((e) => e.type === type).at);

test("in-place arm: the proxy answers and approves 70 min after each wait, and the row is complete", async () => {
	const w = world();
	const row = await runOne({ run: { ref: "B1", arm: "in-place", rep: 1 }, item: item("B1"), ctx: w.ctx });
	const ev = events(w, "B1-in-place-r1");
	assert.equal(at(ev, "answered") - at(ev, "questions"), 70 * MIN);
	assert.equal(at(ev, "approved") - at(ev, "plan"), 70 * MIN);
	assert.deepEqual(w.calls.find((c) => c[1] === "spawn").slice(2, 8), ["--project", "eval-b1-in-place-r1", "--backlog", "B1", "--plan", "in-place"]);
	assert.ok(!w.calls.some((c) => c[1] === "plan" && c[2] === "start"), "in-place never starts a separate planner");
	const answers = JSON.parse(w.files.get("/out/B1-in-place-r1/answers.json"));
	assert.deepEqual(answers.questions, { q1: { option: "b" } }, "the recorded real answer is used");
	assert.equal(row.questions, 1);
	assert.equal(row.tokens.planner, 0);
	assert.equal(row.tokens.implementer, 15);
	assert.equal(row.ownerWaitMs, 140 * MIN);
	assert.ok(row.wallToFirstHeadMs > 0);
	assert.equal(w.files.get("/out/B1-in-place-r1/first-head.patch"), "patch");
});

test("separate arm on a multi-PR plan: plan first, then spawn child 1 only after approval, planner tokens counted", async () => {
	const w = world({ split: true });
	const row = await runOne({ run: { ref: "B1", arm: "separate", rep: 1 }, item: item("B1", { kind: "multi" }), ctx: w.ctx });
	const order = w.calls.filter((c) => c[0] === "tallpine").map((c) => c.slice(1, 3).join(" "));
	const idx = (s) => order.indexOf(s);
	assert.ok(idx("plan start") < idx("plan approve") && idx("plan approve") < idx("spawn --project"), order.join(" | "));
	const spawn = w.calls.find((c) => c[1] === "spawn");
	assert.equal(spawn[spawn.indexOf("--backlog") + 1], "B80", "child 1 (lowest ref), never the parent");
	assert.equal(spawn[spawn.indexOf("--plan") + 1], "separate");
	assert.equal(row.tokens.planner, 15);
});

test("no command of a run pushes, opens a PR, reviews or merges", async () => {
	const w = world();
	await runOne({ run: { ref: "B1", arm: "in-place", rep: 1 }, item: item("B1"), ctx: w.ctx });
	for (const c of w.calls) assert.equal(refusedCommand(c[0], c.slice(1)), "", c.join(" "));
	assert.throws(() => w.ctx.sh("git", ["push"]), /never push/);
	assert.throws(() => w.ctx.sh("gh", ["pr", "create"]), /gh is not used/);
	assert.throws(() => w.ctx.sh("tallpine", ["review", "trigger"]), /not used by eval runs/);
	assert.equal(w.calls.filter((c) => c.includes("--project") && !c.includes("eval-b1-in-place-r1")).length, 0, "only the eval project is touched");
});

test("a run resumes from its saved state without filing or spawning again", async () => {
	const w = world();
	const run = { ref: "B1", arm: "in-place", rep: 1 };
	w.ctx.maxMs = 15 * MIN; // times out while waiting for the answer
	await assert.rejects(runOne({ run, item: item("B1"), ctx: w.ctx }), /timed out/);
	w.ctx.maxMs = 24 * 60 * MIN;
	await runOne({ run, item: item("B1"), ctx: w.ctx });
	assert.equal(w.calls.filter((c) => c[1] === "backlog" && c[2] === "add").length, 1);
	assert.equal(w.calls.filter((c) => c[1] === "spawn").length, 1);
});

test("runAll keeps at most two runs in flight and records a failed run without stopping the others", async () => {
	const w = world({ failOn: "Item B3" });
	const runs = ["B1", "B2", "B3", "B4", "B5"].map((ref) => ({ ref, arm: "in-place", rep: 1 }));
	const { rows, failures } = await runAll({ runs, items: runs.map((r) => item(r.ref)), ctx: w.ctx, concurrency: 9 });
	assert.ok(w.info().maxActive <= 2, `max in flight ${w.info().maxActive}`);
	assert.equal(rows.length, 4);
	assert.deepEqual(failures.map((f) => f.ref), ["B3"]);
	assert.match(failures[0].error, /boom/);
	assert.equal(w.files.get("/out/rows.jsonl").trim().split("\n").length, 4);
});

test("preflight refuses any non-eval project", () => {
	assert.throws(() => preflight({ projects: ["agent-orchestrator"] }), /not an eval project/);
	preflight({ projects: ["eval-b310-in-place-r1", "eval-b88-separate-r2"] });
});

test("every run gets its own eval project and clone", () => {
	const ids = [{ ref: "B1", arm: "in-place", rep: 1 }, { ref: "B1", arm: "separate", rep: 1 }, { ref: "B1", arm: "in-place", rep: 2 }].map(evalProjectId);
	assert.equal(new Set(ids).size, 3);
	assert.deepEqual(ids, ["eval-b1-in-place-r1", "eval-b1-separate-r1", "eval-b1-in-place-r2"]);
});

test("a finished run's sessions are killed, and a failed run's too", async () => {
	const w = world({ split: true });
	await runOne({ run: { ref: "B1", arm: "separate", rep: 1 }, item: item("B1", { kind: "multi" }), ctx: w.ctx });
	const kills = w.calls.filter((c) => c[1] === "session" && c[2] === "kill").map((c) => c[3]).sort();
	assert.deepEqual(kills, ["impl-B1", "plan-B1"], "the implementer and the separate planner both end");
	const f = world();
	f.ctx.maxMs = 15 * MIN;
	await assert.rejects(runOne({ run: { ref: "B1", arm: "in-place", rep: 1 }, item: item("B1"), ctx: f.ctx }), /timed out/);
	assert.ok(f.calls.some((c) => c[1] === "session" && c[2] === "kill" && c[3] === "impl-B1"), "the author session ends on failure");
});

test("a finished run run again returns its row, fetches no usage and adds no duplicate row", async () => {
	const w = world();
	const runs = [{ ref: "B1", arm: "in-place", rep: 1 }];
	await runAll({ runs, items: [item("B1")], ctx: w.ctx });
	const usageCalls = () => w.calls.filter((c) => c[1] === "session" && c[2] === "usage").length;
	const before = usageCalls();
	const again = await runAll({ runs, items: [item("B1")], ctx: w.ctx });
	assert.equal(again.rows.length, 1);
	assert.equal(usageCalls(), before);
	assert.equal(w.files.get("/out/rows.jsonl").trim().split("\n").length, 1);
});

test("a failed spawn is retried on resume instead of polling a plan never started", async () => {
	const w = world({ failSpawnOnce: true });
	const run = { ref: "B1", arm: "in-place", rep: 1 };
	await assert.rejects(runOne({ run, item: item("B1"), ctx: w.ctx }), /spawn down/);
	await runOne({ run, item: item("B1"), ctx: w.ctx });
	assert.equal(w.calls.filter((c) => c[1] === "spawn").length, 2, "failed once, then succeeded");
	assert.equal(w.calls.filter((c) => c[1] === "backlog" && c[2] === "add").length, 1);
});

test("setup registers one eval project per item with in-place on and the role models, only fields a creating orchestrator may set", () => {
	const calls = [];
	const sh = (cmd, args) => {
		calls.push([cmd, ...args]);
		if (cmd === "git") {
			const sub = args.slice(2).join(" ");
			return { code: 0, stderr: "", stdout: sub.startsWith("for-each-ref") ? "refs/heads/main\n" : sub.startsWith("rev-parse") ? "b".repeat(40) : "" };
		}
		return { code: args[1] === "get" ? 1 : 0, stdout: "", stderr: "" };
	};
	const ids = setupProjects({ runs: [{ ref: "B1", arm: "in-place", rep: 1 }, { ref: "B2", arm: "separate", rep: 2 }], items: [item("B1"), item("B2")], ctx: { sh, exists: () => true }, src: "/src", evalRoot: "/eval", config: { workerModel: "w", plannerModel: "p" } });
	assert.deepEqual(ids, ["eval-b1-in-place-r1", "eval-b2-separate-r2"]);
	const set = calls.find((c) => c[1] === "project" && c[2] === "set-config");
	const cfg = JSON.parse(set[set.indexOf("--config-json") + 1]);
	assert.equal(cfg.planningMode, undefined, "planningMode is not an orchestrator-writable field");
	assert.equal(cfg.planInPlace, true);
	assert.equal(cfg.eval, true, "the eval mark opens the orchestrator's scope to the project");
	assert.equal(cfg.worker.agentConfig.model, "w");
	assert.equal(cfg.planner.agentConfig.model, "p");
	assert.ok(calls.some((c) => c[1] === "project" && c[2] === "add" && c.includes("/eval/B1-in-place-r1")));
});

test("a filtered resume keeps the other runs' rows", async () => {
	const w = world();
	const runs = [{ ref: "B1", arm: "in-place", rep: 1 }, { ref: "B2", arm: "in-place", rep: 1 }];
	const items = runs.map((r) => item(r.ref));
	await runAll({ runs, items, ctx: w.ctx });
	await runAll({ runs: runs.slice(0, 1), items, ctx: w.ctx });
	assert.equal(w.files.get("/out/rows.jsonl").trim().split("\n").length, 2);
});

test("a transient plan show failure kills no session early and the run still finishes", async () => {
	const w = world({ failShowOnce: true });
	const row = await runOne({ run: { ref: "B1", arm: "in-place", rep: 1 }, item: item("B1"), ctx: w.ctx });
	assert.ok(row.wallToFirstHeadMs > 0);
	const kills = w.calls.filter((c) => c[1] === "session" && c[2] === "kill");
	assert.equal(kills.length, 1, "one kill, at the end");
	assert.equal(w.calls.indexOf(kills[0]), w.calls.length - 1, "after the row was built");
});

test("an error that is not a terminal outcome leaves the sessions alive for a resume", async () => {
	const w = world({ failShowOnce: true });
	w.ctx.sleep = async () => { throw new Error("interrupted"); }; // the process dies during the retry wait
	await assert.rejects(runOne({ run: { ref: "B1", arm: "in-place", rep: 1 }, item: item("B1"), ctx: w.ctx }), /interrupted/);
	assert.equal(w.calls.filter((c) => c[1] === "session" && c[2] === "kill").length, 0);
});

test("setup builds one pruned base per item and a cheap local clone per run", () => {
	const calls = [];
	const have = new Set();
	const sh = (cmd, args) => {
		calls.push([cmd, ...args]);
		if (cmd === "git" && args[0] === "clone") have.add(args[args.length - 1]);
		if (cmd === "git") {
			const sub = args.slice(2).join(" ");
			return { code: 0, stderr: "", stdout: sub.startsWith("for-each-ref") ? "refs/heads/main\n" : sub.startsWith("rev-parse") ? "b".repeat(40) : "" };
		}
		return { code: args[1] === "get" ? 1 : 0, stdout: "", stderr: "" };
	};
	const runs = ["in-place", "separate"].flatMap((arm) => [1, 2].map((rep) => ({ ref: "B1", arm, rep })));
	setupProjects({ runs, items: [item("B1")], ctx: { sh, exists: (p) => have.has(p) }, src: "/src", evalRoot: "/eval", config: {} });
	const clones = calls.filter((c) => c[0] === "git" && c[1] === "clone");
	assert.equal(clones.filter((c) => c.includes("/src")).length, 1, "the source is cloned once, for the base");
	assert.equal(clones.filter((c) => c[c.length - 2] === "/eval/base-B1").length, 4, "each run clones the pruned base");
});

test("the eval item is filed open, and a leftover inbox item is accepted instead of duplicated", async () => {
	const w = world();
	const run = { ref: "B1", arm: "in-place", rep: 1 };
	await runOne({ run, item: item("B1"), ctx: w.ctx });
	const add = w.calls.find((c) => c[1] === "backlog" && c[2] === "add");
	assert.ok(add.includes("--open"), "backlog add must skip the inbox");
	// An earlier attempt left an inbox item and a state file pointing at it.
	const w2 = world();
	w2.ctx.fs.write("/out/B1-in-place-r1/state.json", JSON.stringify({ evalRef: "B1" }));
	w2.calls.length = 0;
	w2.ctx.sh("tallpine", ["backlog", "add", "Item B1", "--project", "x"]); // the leftover: filed without --open
	w2.calls.length = 0;
	await runOne({ run, item: item("B1"), ctx: w2.ctx });
	assert.equal(w2.calls.filter((c) => c[1] === "backlog" && c[2] === "add").length, 0, "no duplicate item");
	assert.equal(w2.calls.filter((c) => c[1] === "backlog" && c[2] === "accept").length, 1, "the inbox item is accepted");
});
