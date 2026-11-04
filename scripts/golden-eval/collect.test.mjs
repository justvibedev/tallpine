import assert from "node:assert/strict";
import { test } from "node:test";
import { buildRow, ownerWait, parsePlan, parseTranscript, parseUsage, wallToFirstHead } from "./collect.mjs";

const usage = { models: [
	{ model: "claude-haiku-5-5", inputTokens: 10, cacheCreationTokens: 0, cacheReadTokens: 0, outputTokens: 5, turns: 1 },
	{ model: "claude-sonnet-5-5", inputTokens: 100, cacheCreationTokens: 200, cacheReadTokens: 4000, outputTokens: 50, turns: 7 },
] };

test("parseUsage sums models from session usage JSON", () => {
	assert.deepEqual(parseUsage(usage), { input: 110, cacheCreation: 200, cacheRead: 4000, output: 55, turns: 8, total: 4365 });
	assert.equal(parseUsage({}).total, 0);
});

const plan = { plan: { status: "approved", startedAt: "2026-11-02T10:00:00Z", approvedAt: "2026-11-02T12:30:00Z", questions: [{ id: "q1", text: "Who pays?" }, { id: "q2", text: "When?" }] } };

test("parsePlan reads question count and plan timestamps", () => {
	const p = parsePlan(plan);
	assert.equal(p.questions, 2);
	assert.equal(p.planMs, 150 * 60000);
	assert.deepEqual(p.questionTexts, ["Who pays?", "When?"]);
	assert.equal(parsePlan({ plan: { status: "drafting" } }).planMs, null);
});

const events = [
	{ at: "2026-11-02T10:00:00Z", type: "spawned" },
	{ at: "2026-11-02T10:10:00Z", type: "questions" },
	{ at: "2026-11-02T11:20:00Z", type: "answered" },
	{ at: "2026-11-02T11:40:00Z", type: "plan" },
	{ at: "2026-11-02T12:50:00Z", type: "approved" },
	{ at: "2026-11-02T13:30:00Z", type: "head" },
];

test("ownerWait adds the question wait and the approval wait; wall time runs spawn to head", () => {
	assert.equal(ownerWait(events), 140 * 60000);
	assert.equal(wallToFirstHead(events), 210 * 60000);
	assert.equal(wallToFirstHead(events.slice(0, 3)), null);
});

const line = (content, cache) => JSON.stringify({ type: "assistant", message: { content, usage: { cache_read_input_tokens: cache } } });
const transcript = [
	line([{ type: "tool_use", name: "Read", input: { file_path: "a" } }, { type: "tool_use", name: "Grep", input: {} }], 100),
	JSON.stringify({ type: "user", message: { content: "x" } }),
	"not json",
	line([{ type: "tool_use", name: "Bash", input: { command: "ls" } }], 200),
	line([{ type: "tool_use", name: "Bash", input: { command: "git add -A && git commit -m x" } }], 300),
	line([{ type: "tool_use", name: "Read", input: {} }], 9999),
].join("\n");

test("parseTranscript counts tool calls and cache reads up to the first commit", () => {
	assert.deepEqual(parseTranscript(transcript), { toolCalls: 4, cacheRead: 600, committed: true });
	assert.equal(parseTranscript(line([{ type: "tool_use", name: "Read", input: {} }], 5)).committed, false);
});

test("parseTranscript counts a repeated message id once and skips sidechain and synthetic records", () => {
	const rec = (id, extra = {}, content = [{ type: "tool_use", name: "Read", input: {} }]) => JSON.stringify({ type: "assistant", ...extra.top, message: { id, content, usage: { cache_read_input_tokens: 100 }, ...extra.msg } });
	const t = [
		rec("m1"), rec("m1"), rec("m1"), // one turn, three content-block records
		rec("m2", { top: { isSidechain: true } }),
		rec("m3", { msg: { model: "<synthetic>" } }),
		rec("m4", {}, [{ type: "tool_use", name: "Bash", input: { command: "git commit -m x" } }]),
	].join("\n");
	assert.deepEqual(parseTranscript(t), { toolCalls: 4, cacheRead: 200, committed: true });
});

test("parseTranscript is n/a for non-Claude harnesses and empty input", () => {
	assert.equal(parseTranscript(transcript, "codex"), null);
	assert.equal(parseTranscript(""), null);
});

test("buildRow assembles one row and marks transcript metrics n/a for Codex", () => {
	const row = buildRow({ ref: "B1", arm: "in-place", rep: 1, plannerUsage: usage, implementerUsage: usage, plan, events, transcript, findings: 3 });
	assert.equal(row.tokens.total, 8730);
	assert.equal(row.toolCallsBeforeCommit, 4);
	assert.equal(row.findingsFirstHead, 3);
	const codex = buildRow({ ref: "B1", arm: "separate", rep: 1, harness: "codex", plan, events, transcript });
	assert.equal(codex.toolCallsBeforeCommit, "n/a");
	assert.equal(codex.cacheReadBeforeCommit, "n/a");
});
