// @vitest-environment node
import { describe, expect, it } from "vitest";
import { agentsBusy } from "./idle-agents";

/** Daemon stand-in: the session list plus each session's /reviews body. */
function daemon(sessions: unknown, reviews: Record<string, unknown> = {}, listOk = true) {
	return (async (url: string) => {
		const match = /sessions\/([^/?]+)\/reviews/.exec(url);
		if (match) return { ok: match[1] in reviews, json: async () => reviews[match[1]!] };
		return { ok: listOk, json: async () => ({ sessions }) };
	}) as unknown as typeof fetch;
}

const none = { runs: [], reviews: [] };

describe("agentsBusy", () => {
	it("is idle when every live session is idle or waiting and no review runs", async () => {
		const sessions = [
			{ id: "a", activity: { state: "idle" } },
			{ id: "b", activity: { state: "waiting_input" } },
			{ id: "c", activity: { state: "active" }, isTerminated: true },
		];
		expect(await agentsBusy(1, daemon(sessions, { a: none, b: { ...none, runs: [{ status: "complete" }] } }))).toBe(false);
	});
	it("is busy while a turn is active", async () => {
		expect(await agentsBusy(1, daemon([{ id: "a", activity: { state: "active" } }]))).toBe(true);
	});
	it("is busy while a review runs on a worker waiting for the human", async () => {
		const sessions = [{ id: "a", activity: { state: "waiting_input" }, displayStatus: "Blocked" }];
		expect(await agentsBusy(1, daemon(sessions, { a: { runs: [{ status: "running" }] } }))).toBe(true);
	});
	it("is busy while the reviewer pane is actively working", async () => {
		const sessions = [{ id: "a", activity: { state: "idle" } }];
		expect(await agentsBusy(1, daemon(sessions, { a: { reviewerActivityState: "active", runs: [] } }))).toBe(true);
	});
	it("is busy when the daemon cannot be read", async () => {
		expect(await agentsBusy(null)).toBe(true);
		expect(await agentsBusy(1, daemon([], {}, false))).toBe(true);
		expect(await agentsBusy(1, daemon(undefined))).toBe(true);
		expect(await agentsBusy(1, daemon([{ id: "a", activity: { state: "idle" } }], {}))).toBe(true);
		expect(await agentsBusy(1, (async () => { throw new Error("x"); }) as unknown as typeof fetch)).toBe(true);
	});
});
