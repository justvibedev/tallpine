// @vitest-environment node
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { afterEach, describe, expect, it } from "vitest";
import { patchClaudeSyntheticPromptTurnEnd } from "./build-acp-runtime-helpers.mjs";

const temporaryDirectories = [];

function temporaryDirectory() {
	const directory = mkdtempSync(join(tmpdir(), "acp-synthetic-prompt-"));
	temporaryDirectories.push(directory);
	return directory;
}

afterEach(() => {
	for (const directory of temporaryDirectories.splice(0)) {
		rmSync(directory, { recursive: true, force: true });
	}
});

describe("patchClaudeSyntheticPromptTurnEnd", () => {
	// The end-of-turn check from claude-agent-acp 0.99.0 dist/acp-agent.js, verbatim
	// from "const abandoned" to the stuck-call failure.
	const endOfTurnCheck = `
                const abandoned = unfinished.filter((id) => !session.dispatchedToolCalls?.has(id));
                const stuck = unfinished.filter((id) => session.dispatchedToolCalls?.has(id));
                for (const toolCallId of abandoned) {
                    if (!(await closeUnfinished(toolCallId, false))) {
                        return;
                    }
                }
                if (stuck.length > 0) {
                    const message = \`Claude ended the turn without returning results for tool calls: \${stuck.join(", ")}\`;
                    for (const toolCallId of stuck) {
                        if (!(await closeUnfinished(toolCallId, true))) {
                            return;
                        }
                    }
                    await failActiveWithSessionFailure("internal_error", message, message);
                    return;
                }
                return "ended";
`;
	const fixture = `const closeToolCall = async (toolCallId, report) => {};\n${endOfTurnCheck}`;

	// Runs the check for a turn whose unfinished calls were all dispatched through
	// canUseTool, as the refusal-fallback prompt and a real hung tool both are.
	async function endTurn(source, unfinished) {
		const AsyncFunction = Object.getPrototypeOf(async function () {}).constructor;
		const start = source.indexOf("\n", source.indexOf("const closeToolCall"));
		const run = new AsyncFunction("unfinished", "session", "turn", "result", "closeToolCall", "closeUnfinished", "failActiveWithSessionFailure", "settleActive", source.slice(start));
		const closed = [];
		const failures = [];
		const turn = { settled: false };
		const session = { dispatchedToolCalls: new Set(unfinished), activeTurn: turn, cancelled: false };
		const outcome = await run(
			unfinished, session, turn, { stopReason: "end_turn" },
			async (id, report) => { closed.push([id, report.status]); },
			async (id, ran) => { closed.push([id, ran ? "failed" : "stopped"]); return true; },
			async (_kind, message) => { failures.push(message); },
			async () => { failures.push("cancelled"); },
		);
		return { outcome, closed, failures };
	}

	it("ends the turn normally when only Claude Code's refusal-fallback prompt is open", async () => {
		const confirm = "confirm_0922621e-6ed9-4c87-a3af-87abac09ea32";
		const before = await endTurn(fixture, [confirm]);
		expect(before.failures).toEqual([`Claude ended the turn without returning results for tool calls: ${confirm}`]);

		const adapterPath = join(temporaryDirectory(), "acp-agent.js");
		writeFileSync(adapterPath, fixture);
		expect(patchClaudeSyntheticPromptTurnEnd(adapterPath)).toBe(true);
		expect(patchClaudeSyntheticPromptTurnEnd(adapterPath)).toBe(false);
		const patched = readFileSync(adapterPath, "utf8");

		const after = await endTurn(patched, [confirm]);
		expect(after).toEqual({ outcome: "ended", closed: [[confirm, "completed"]], failures: [] });

		// A real tool that never returned still fails the turn.
		const real = await endTurn(patched, [confirm, "toolu_018AM8wRcU7Ud1FhD44CE8VP"]);
		expect(real.closed).toEqual([[confirm, "completed"], ["toolu_018AM8wRcU7Ud1FhD44CE8VP", "failed"]]);
		expect(real.failures).toEqual(["Claude ended the turn without returning results for tool calls: toolu_018AM8wRcU7Ud1FhD44CE8VP"]);
	});

	it("fails packaging if the pinned end-of-turn check changes", () => {
		const adapterPath = join(temporaryDirectory(), "acp-agent.js");
		writeFileSync(adapterPath, fixture.replace("const stuck =", "const hung ="));
		expect(() => patchClaudeSyntheticPromptTurnEnd(adapterPath)).toThrow(/synthetic prompt patch/);
	});

	it("applies to the pinned adapter when the runtime is installed", () => {
		const installed = join(dirname(fileURLToPath(import.meta.url)), "..", "acp-runtime", "node_modules", "@agentclientprotocol", "claude-agent-acp", "dist", "acp-agent.js");
		if (!existsSync(installed)) return;
		const copy = join(temporaryDirectory(), "acp-agent.js");
		writeFileSync(copy, readFileSync(installed));
		expect(patchClaudeSyntheticPromptTurnEnd(copy)).toBe(true);
	});
});
