// Report: aggregates result rows per arm (mean and spread) into a markdown
// report, and builds a blind grading sheet of the question sets with the arm
// labels in a separate key file so the owner grades without knowing the arm.
import { readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { mean, rng, spread } from "./stats.mjs";

const METRICS = [
	["questions", "Questions asked", (r) => r.questions],
	["ownerWaitMin", "Owner wait (min)", (r) => (r.ownerWaitMs == null ? null : r.ownerWaitMs / 60000)],
	["tokens", "Tokens, planner + implementer", (r) => r.tokens?.total],
	["wallMin", "Wall time to first head (min)", (r) => (r.wallToFirstHeadMs == null ? null : r.wallToFirstHeadMs / 60000)],
	["toolCalls", "Tool calls before first commit", (r) => (typeof r.toolCallsBeforeCommit === "number" ? r.toolCallsBeforeCommit : null)],
	["cacheRead", "Cache-read tokens before first commit", (r) => (typeof r.cacheReadBeforeCommit === "number" ? r.cacheReadBeforeCommit : null)],
	["findings", "Findings on first head", (r) => r.findingsFirstHead],
];

/** Per-arm aggregate of every metric: {arm: {metric: {mean, n, min, max, sd}}}. */
export function aggregate(rows) {
	const out = {};
	for (const arm of [...new Set(rows.map((r) => r.arm))].sort()) {
		out[arm] = { runs: rows.filter((r) => r.arm === arm).length };
		for (const [key, , pick] of METRICS) {
			const xs = rows.filter((r) => r.arm === arm).map(pick).filter((x) => x != null);
			out[arm][key] = { mean: mean(xs), ...spread(xs) };
		}
	}
	return out;
}

const fmt = (x) => (x == null ? "n/a" : Number.isInteger(x) ? String(x) : x.toFixed(1));

/** Markdown report of the aggregates; n/a where a metric has no data. */
export function renderReport(rows) {
	const agg = aggregate(rows);
	const arms = Object.keys(agg);
	const lines = ["# Golden-eval report", "", `Runs: ${arms.map((a) => `${a} ${agg[a].runs}`).join(", ")}`, ""];
	lines.push(`| Metric | ${arms.map((a) => `${a} mean (min–max, sd)`).join(" | ")} |`, `|---|${arms.map(() => "---|").join("")}`);
	for (const [key, label] of METRICS) {
		const cells = arms.map((a) => {
			const m = agg[a][key];
			return m.n ? `${fmt(m.mean)} (${fmt(m.min)}–${fmt(m.max)}, ${fmt(m.sd)}) n=${m.n}` : "n/a";
		});
		lines.push(`| ${label} | ${cells.join(" | ")} |`);
	}
	return lines.join("\n") + "\n";
}

/**
 * Blind grading sheet: every run's question set under an anonymous id in a
 * seeded shuffled order, plus a key mapping ids back to arm, item and rep.
 */
export function gradingSheet(rows, seed = 1) {
	const rand = rng(seed);
	const order = rows.map((r, i) => ({ r, k: rand(), i })).sort((a, b) => a.k - b.k || a.i - b.i).map((x) => x.r);
	const key = {};
	const sheet = ["# Question sets to grade", "", "Grade each set 1–5 for how well it asks only what the owner must decide. Arm labels are hidden.", ""];
	order.forEach((r, n) => {
		const id = `S${String(n + 1).padStart(2, "0")}`;
		key[id] = { ref: r.ref, arm: r.arm, rep: r.rep };
		sheet.push(`## ${id}`, "", ...(r.questionTexts?.length ? r.questionTexts.map((q, i) => `${i + 1}. ${q}`) : ["(no questions)"]), "", "Grade: ", "");
	});
	return { sheet: sheet.join("\n"), key };
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
	const [rowsFile, outPrefix = "golden-eval", seed] = process.argv.slice(2);
	if (!rowsFile) {
		console.error("usage: report.mjs rows.jsonl [out-prefix] [seed]  -> <prefix>-report.md, <prefix>-grading.md, <prefix>-grading-key.json");
		process.exit(2);
	}
	const rows = readFileSync(rowsFile, "utf8").split("\n").filter(Boolean).map((l) => JSON.parse(l));
	const { sheet, key } = gradingSheet(rows, seed ? Number(seed) : 1);
	writeFileSync(`${outPrefix}-report.md`, renderReport(rows));
	writeFileSync(`${outPrefix}-grading.md`, sheet);
	writeFileSync(`${outPrefix}-grading-key.json`, JSON.stringify(key, null, 2) + "\n");
}
