import assert from "node:assert/strict";
import { test } from "node:test";
import { aggregate, gradingSheet, renderReport } from "./report.mjs";

const row = (arm, rep, over = {}) => ({ ref: "B1", arm, rep, questions: 2, questionTexts: [`Q${rep} one`, `Q${rep} two`], ownerWaitMs: 60000, tokens: { total: 100 }, wallToFirstHeadMs: 120000, toolCallsBeforeCommit: 10, cacheReadBeforeCommit: 1000, findingsFirstHead: 1, ...over });
const rows = [row("in-place", 1, { questions: 2 }), row("in-place", 2, { questions: 4 }), row("separate", 1, { questions: 6, toolCallsBeforeCommit: "n/a" }), row("separate", 2, { questions: 6 })];

test("aggregate gives mean and spread per arm and skips n/a values", () => {
	const a = aggregate(rows);
	assert.equal(a["in-place"].questions.mean, 3);
	assert.equal(a["in-place"].questions.min, 2);
	assert.equal(a["in-place"].questions.max, 4);
	assert.equal(a["in-place"].questions.sd, 1);
	assert.equal(a.separate.toolCalls.n, 1);
});

test("renderReport lists both arms with n/a for missing data", () => {
	const md = renderReport([row("in-place", 1), row("separate", 1, { findingsFirstHead: null })]);
	assert.match(md, /in-place mean/);
	assert.match(md, /Questions asked \| 2 \(2–2, 0\)/);
	assert.match(md, /Findings on first head \| 1 \(1–1, 0\) n=1 \| n\/a/);
});

test("the grading sheet hides arm labels and the key maps ids back", () => {
	const { sheet, key } = gradingSheet(rows, 7);
	assert.doesNotMatch(sheet, /in-place|separate/);
	assert.deepEqual(Object.keys(key).sort(), ["S01", "S02", "S03", "S04"]);
	assert.deepEqual(Object.values(key).map((k) => `${k.arm}${k.rep}`).sort(), ["in-place1", "in-place2", "separate1", "separate2"]);
	assert.equal(gradingSheet(rows, 7).sheet, sheet);
	assert.notEqual(gradingSheet(rows, 8).sheet, sheet);
});
