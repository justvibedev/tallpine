// Collector: turns the raw material of one replay run into one result row.
// Event log: `spawned` is the arm's first step (the in-place spawn, or the
// separate arm's `plan start`), so wall time to first head includes planning
// in both arms.
// Sources: `tallpine session usage --json` (planner and implementer), the
// `tallpine plan show --json` record, the driver's event log, and, for Claude
// Code sessions only, the session transcript (JSONL).
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

/** Sums a `session usage --json` document into token totals. */
export function parseUsage(doc) {
	const t = { input: 0, cacheCreation: 0, cacheRead: 0, output: 0, turns: 0 };
	for (const m of doc?.models ?? []) {
		t.input += m.inputTokens ?? 0;
		t.cacheCreation += m.cacheCreationTokens ?? 0;
		t.cacheRead += m.cacheReadTokens ?? 0;
		t.output += m.outputTokens ?? 0;
		t.turns += m.turns ?? 0;
	}
	t.total = t.input + t.cacheCreation + t.cacheRead + t.output;
	return t;
}

/** Question count and the plan's own timestamps from a `plan show --json` document. */
export function parsePlan(doc) {
	const p = doc?.plan ?? doc ?? {};
	const ms = (s) => (s ? Date.parse(s) : NaN);
	const started = ms(p.startedAt);
	const approved = ms(p.approvedAt);
	return {
		questions: Array.isArray(p.questions) ? p.questions.length : 0,
		startedAt: p.startedAt ?? null,
		approvedAt: p.approvedAt ?? null,
		planMs: Number.isFinite(started) && Number.isFinite(approved) ? approved - started : null,
		questionTexts: Array.isArray(p.questions) ? p.questions.map((q) => q.text) : [],
	};
}

/**
 * Owner wait from the driver's event log: the time between a plan waiting on
 * the owner (questions or plan submitted) and the proxy's answer or approval.
 * events: [{at: ISO, type: "questions"|"answered"|"plan"|"approved"|"spawned"|"head"}].
 */
export function ownerWait(events) {
	const at = (t) => events.filter((e) => e.type === t).map((e) => Date.parse(e.at));
	let total = 0;
	for (const [from, to] of [["questions", "answered"], ["plan", "approved"]]) {
		const a = at(from);
		const b = at(to);
		if (a.length && b.length) total += Math.max(0, b[b.length - 1] - a[0]);
	}
	return total;
}

/** Wall time from spawn to the first PR head, or null while there is none. */
export function wallToFirstHead(events) {
	const spawn = events.find((e) => e.type === "spawned");
	const head = events.find((e) => e.type === "head");
	return spawn && head ? Date.parse(head.at) - Date.parse(spawn.at) : null;
}

const isCommit = (block) => block?.type === "tool_use" && /\bgit\s+commit\b/.test(String(block.input?.command ?? ""));

/**
 * Re-exploration measures from a Claude Code transcript: tool calls and
 * cache-read tokens up to the first commit. Claude Code writes one record per
 * content block and repeats message.id and message.usage on each, so usage is
 * counted once per message id; sidechain (subagent) and synthetic records are
 * skipped, as in backend/internal/observe/usage/parser_claude.go. Returns null
 * (n/a) for any other harness or an unreadable transcript.
 */
export function parseTranscript(jsonl, harness = "claude-code") {
	if (harness !== "claude-code" || !jsonl) return null;
	let toolCalls = 0;
	let cacheRead = 0;
	let sawAssistant = false;
	const seen = new Set();
	for (const line of jsonl.split("\n")) {
		if (!line.trim()) continue;
		let rec;
		try {
			rec = JSON.parse(line);
		} catch {
			continue;
		}
		const msg = rec.message;
		if (rec.type !== "assistant" || !msg || rec.isSidechain || msg.model === "<synthetic>") continue;
		sawAssistant = true;
		if (!msg.id || !seen.has(msg.id)) cacheRead += msg.usage?.cache_read_input_tokens ?? 0;
		if (msg.id) seen.add(msg.id);
		const blocks = Array.isArray(msg.content) ? msg.content : [];
		const tools = blocks.filter((b) => b?.type === "tool_use");
		toolCalls += tools.length;
		if (tools.some(isCommit)) return { toolCalls, cacheRead, committed: true };
	}
	return sawAssistant ? { toolCalls, cacheRead, committed: false } : null;
}

/** One result row. Inputs are already-read documents; absent ones give null. */
export function buildRow({ ref, arm, rep, harness = "claude-code", plannerUsage, implementerUsage, plan, events = [], transcript, findings }) {
	const pu = plannerUsage ? parseUsage(plannerUsage) : null;
	const iu = implementerUsage ? parseUsage(implementerUsage) : null;
	const pl = plan ? parsePlan(plan) : null;
	const tr = parseTranscript(transcript, harness);
	return {
		ref,
		arm,
		rep,
		questions: pl ? pl.questions : null,
		questionTexts: pl ? pl.questionTexts : [],
		ownerWaitMs: events.length ? ownerWait(events) : null,
		planMs: pl ? pl.planMs : null,
		tokens: { planner: pu?.total ?? 0, implementer: iu?.total ?? 0, total: (pu?.total ?? 0) + (iu?.total ?? 0) },
		wallToFirstHeadMs: wallToFirstHead(events),
		toolCallsBeforeCommit: tr ? tr.toolCalls : "n/a",
		cacheReadBeforeCommit: tr ? tr.cacheRead : "n/a",
		findingsFirstHead: Number.isFinite(findings) ? findings : null,
	};
}

const readJSON = (p) => (p ? JSON.parse(readFileSync(p, "utf8")) : undefined);
const readText = (p) => (p ? readFileSync(p, "utf8") : undefined);

if (process.argv[1] === fileURLToPath(import.meta.url)) {
	const a = new Map();
	for (let i = 2; i < process.argv.length; i += 2) a.set(process.argv[i].replace(/^--/, ""), process.argv[i + 1]);
	if (!a.get("ref") || !a.get("arm")) {
		console.error("usage: collect.mjs --ref B1 --arm in-place|separate --rep 1 [--harness claude-code] [--planner-usage f.json] [--implementer-usage f.json] [--plan f.json] [--events f.json] [--transcript f.jsonl] [--findings N]");
		process.exit(2);
	}
	const row = buildRow({
		ref: a.get("ref"), arm: a.get("arm"), rep: Number(a.get("rep") ?? 1), harness: a.get("harness") ?? "claude-code",
		plannerUsage: readJSON(a.get("planner-usage")), implementerUsage: readJSON(a.get("implementer-usage")),
		plan: readJSON(a.get("plan")), events: readJSON(a.get("events")) ?? [], transcript: readText(a.get("transcript")),
		findings: a.has("findings") ? Number(a.get("findings")) : undefined,
	});
	console.log(JSON.stringify(row));
}
