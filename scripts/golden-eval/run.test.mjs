import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "node:test";
import { armCommands, buildSchedule, matchAnswers, planRuns } from "./plan.mjs";

const items = [{ ref: "B1" }, { ref: "B2" }, { ref: "B3" }];

test("planRuns covers item x arm x repetition", () => {
	assert.equal(planRuns(items).length, 12);
	assert.deepEqual(planRuns([items[0]], { arms: ["in-place"], reps: 1 }), [{ ref: "B1", arm: "in-place", rep: 1 }]);
});

test("schedule never has more than two runs in flight, even if more is asked", () => {
	const s = buildSchedule(planRuns(items), { concurrency: 8 });
	assert.equal(new Set(s.map((r) => r.slot)).size, 2);
	for (const r of s) {
		const live = s.filter((o) => o.startAfterMin <= r.startAfterMin && r.startAfterMin < o.startAfterMin + 140);
		assert.ok(live.length <= 2, `${live.length} runs overlap at +${r.startAfterMin}`);
	}
});

test("arm commands: in-place spawns once; separate plans first and spawns only after approval", () => {
	const ip = armCommands({ ref: "B1", arm: "in-place", rep: 1 }, "eval");
	assert.deepEqual(ip.plan[0].slice(0, 7), ["spawn", "--project", "eval", "--backlog", "B1", "--plan", "in-place"]);
	assert.deepEqual(ip.build(["B2", "B3"]), []);
	const sep = armCommands({ ref: "B1", arm: "separate", rep: 2 }, "eval");
	assert.deepEqual(sep.plan, [["plan", "start", "--project", "eval", "B1"]]);
	assert.equal(sep.build()[0][4], "B1"); // single PR: the item itself
	assert.equal(sep.build(["B7", "B8"])[0][4], "B7"); // split: child 1, never the parent
	assert.equal(sep.build(["B7", "B8"])[0][6], "separate");
});

test("matchAnswers maps by word overlap and falls back to the recommended option", () => {
	const qs = [
		{ id: "q1", text: "Who pays for a shared seat?", options: [{ id: "a", label: "Each friend" }, { id: "b", label: "The booker" }], recommended: "a" },
		{ id: "q2", text: "Unrelated topic entirely", options: [{ id: "a", label: "x" }, { id: "b", label: "y" }], recommended: "b" },
	];
	const got = matchAnswers(qs, [{ question: "who pays for the seat", answer: "The booker" }]);
	assert.deepEqual(got, { q1: { option: "b" }, q2: { option: "b" } });
});

test("dry-run prints the schedule with 70 min delays and needs no daemon", () => {
	const dir = mkdtempSync(join(tmpdir(), "golden-eval-"));
	try {
		const golden = join(dir, "golden.json");
		writeFileSync(golden, JSON.stringify({ items }));
		const run = fileURLToPath(new URL("./run.mjs", import.meta.url));
		const env = { PATH: dirOf(process.execPath), TALLPINE_DATA_DIR: dir, HOME: dir };
		const r = spawnSync(process.execPath, [run, "--dry-run", "--golden", golden, "--concurrency", "5"], { encoding: "utf8", env });
		assert.equal(r.status, 0, r.stderr);
		assert.match(r.stdout, /12 runs, at most 2 concurrent, proxy delay 70 min/);
		assert.match(r.stdout, /answer  70 min after the questions are submitted/);
		assert.match(r.stdout, /approve 70 min after the plan is submitted/);
		const live = spawnSync(process.execPath, [run, "--golden", golden], { encoding: "utf8", env });
		assert.equal(live.status, 3);
		assert.equal(live.stdout, "");
		assert.match(live.stderr, /--dry-run/);
	} finally {
		rmSync(dir, { recursive: true, force: true });
	}
});

function dirOf(p) {
	return p.slice(0, p.lastIndexOf("/"));
}
