// Pure helpers of the golden eval: the run plan, the commands of each arm, the
// schedule and the owner-proxy answer matcher. No I/O, so every module may import it.
export const DELAY_MIN = 70;
export const MAX_CONCURRENCY = 2;
export const ARMS = ["in-place", "separate"];

/** The runs to execute: every item x arm x repetition, in a stable order. */
export function planRuns(items, { arms = ARMS, reps = 2 } = {}) {
	const runs = [];
	for (const rep of Array.from({ length: reps }, (_, i) => i + 1)) {
		for (const item of items) for (const arm of arms) runs.push({ ref: item.ref, arm, rep });
	}
	return runs;
}

/**
 * The `tallpine` calls of one run, in two phases. `plan` runs first and is the
 * arm's first step (the `spawned` event). `build(childRefs)` runs only after
 * the proxy approved the plan: the in-place arm builds in its own session, so it
 * has nothing to add; the separate arm spawns the implementer, on the item for a
 * single PR or on child 1 of a split (`spawn --backlog <parent>` is refused with
 * BACKLOG_ITEM_IS_PARENT once the plan split it; children come from the item's
 * `plan show --json` prSplit and its child items). Only the first head is
 * measured, so later children are not spawned.
 */
export function armCommands(run, project) {
	const p = ["--project", project];
	if (run.arm === "in-place") {
		return { plan: [["spawn", ...p, "--backlog", run.ref, "--plan", "in-place", "--name", `eval-${run.ref}-ip${run.rep}`]], build: () => [] };
	}
	return {
		plan: [["plan", "start", ...p, run.ref]],
		build: (childRefs = []) => [["spawn", ...p, "--backlog", childRefs[0] ?? run.ref, "--plan", "separate", "--name", `eval-${run.ref}-sp${run.rep}`]],
	};
}

/**
 * Schedule with at most `concurrency` runs in flight. A run lasts its waits (two
 * proxy delays) plus an unknown build time, so slots are assigned in order and
 * a slot frees only when its run ends.
 */
export function buildSchedule(runs, { concurrency = MAX_CONCURRENCY, delayMin = DELAY_MIN } = {}) {
	const slots = Array.from({ length: Math.min(concurrency, MAX_CONCURRENCY) }, () => 0);
	return runs.map((run, i) => {
		const slot = slots.indexOf(Math.min(...slots));
		const startMin = slots[slot];
		slots[slot] = startMin + 2 * delayMin; // lower bound: the two proxy waits
		return { ...run, order: i + 1, slot: slot + 1, startAfterMin: startMin, answerAfterMin: delayMin, approveAfterMin: delayMin };
	});
}

/** Dry-run text: one line per step, with the delays and the concurrency cap stated. */
export function renderSchedule(schedule, { delayMin = DELAY_MIN, concurrency = MAX_CONCURRENCY } = {}) {
	const lines = [`golden-eval schedule: ${schedule.length} runs, at most ${Math.min(concurrency, MAX_CONCURRENCY)} concurrent, proxy delay ${delayMin} min`, "start times are lower-bound estimates: they count only the two proxy waits, not planning or build time"];
	for (const s of schedule) {
		lines.push(
			`#${s.order} slot ${s.slot} ${s.ref} arm=${s.arm} rep=${s.rep}`,
			`  spawn   at +${s.startAfterMin} min (slot free)`,
			`  answer  ${delayMin} min after the questions are submitted`,
			`  approve ${delayMin} min after the plan is submitted`,
		);
	}
	return lines.join("\n") + "\n";
}

/** Maps the plan's questions to the golden item's recorded answers by word overlap; unmatched questions take the recommended option. */
export function matchAnswers(questions, golden) {
	const words = (s) => new Set(String(s).toLowerCase().match(/[a-z0-9]{3,}/g) ?? []);
	const out = {};
	for (const q of questions) {
		const qw = words(q.text);
		let best = null;
		let bestScore = 0;
		for (const g of golden ?? []) {
			const score = [...words(g.question)].filter((w) => qw.has(w)).length;
			if (score > bestScore) [best, bestScore] = [g, score];
		}
		const wanted = best ? String(best.answer).toLowerCase() : "";
		const opt = (q.options ?? []).find((o) => o.id === best?.answer || String(o.label).toLowerCase() === wanted);
		out[q.id] = { option: opt ? opt.id : q.recommended };
	}
	return out;
}
