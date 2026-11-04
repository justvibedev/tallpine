import { describe, expect, it } from "vitest";
import { cachedBy, latestWorkerUpdateKeys, piggybackedUpdates, workerUpdatesOf, type WorkerItem } from "./worker-updates";

const base = { id: "m1", createdAt: "2026-11-01T00:00:00Z" };
const msg = (text: string, extra: Partial<WorkerItem> = {}): WorkerItem => ({ ...base, kind: "message", role: "user", origin: "automation", text, ...extra });
const REPORT = (id: string, state: string) => `Reports since your previous turn:\n\n[${state}] [W](tallpine://sessions/p/${id})\nbody`;

describe("workerUpdatesOf", () => {
	it("reads a direct message by its stored sender, falling back to the [from] prefix", () => {
		expect(workerUpdatesOf(msg("hi", { senderSessionId: "app-1", senderDisplayName: "Fix" }))).toMatchObject([{ name: "Fix", sessionId: "app-1", body: "hi" }]);
		expect(workerUpdatesOf(msg('[from worker app-2 "Docs"] hello'))).toMatchObject([{ name: "Docs", sessionId: "app-2", body: "hello" }]);
	});
	it("shows an orchestrator's framed message as a clean body, marked fromOrchestrator", () => {
		const framed = "<<orchestrator:agent-orchestrator-738 n=09855c70>>\nplease check X\n<</orchestrator n=09855c70>>";
		for (const m of [msg(framed, { senderSessionId: "agent-orchestrator-738" }), msg(framed)]) {
			const [u] = workerUpdatesOf(m);
			expect(u).toMatchObject({ sessionId: "agent-orchestrator-738", body: "please check X", fromOrchestrator: true });
			expect(u!.body).not.toContain("<<");
		}
	});
	it("reads a steer activity only when it names a sender", () => {
		const steer = (senderSessionId?: string): WorkerItem => ({ ...base, kind: "activity", summary: "s", detail: { event: "steer", senderSessionId, text: "[from app-3] go" } });
		expect(workerUpdatesOf(steer("app-3"))).toMatchObject([{ sessionId: "app-3", body: "go" }]);
		expect(workerUpdatesOf(steer(undefined))).toEqual([]);
	});
	it("ignores human messages and reads report batches", () => {
		expect(workerUpdatesOf(msg("hi", { origin: "human" }))).toEqual([]);
		expect(workerUpdatesOf(msg(REPORT("app-1", "done")))).toMatchObject([{ state: "done", sessionId: "app-1" }]);
	});
});

describe("piggybackedUpdates and latestWorkerUpdateKeys", () => {
	it("reads reports folded into a human message", () => {
		const text = `hi\n\n<ao-worker-reports>\n${REPORT("app-1", "stuck")}\n</ao-worker-reports>`;
		expect(piggybackedUpdates(msg(text, { origin: "human" }))).toMatchObject([{ state: "stuck", key: "m1:reports:0" }]);
		expect(piggybackedUpdates(msg("hi", { origin: "human" }))).toEqual([]);
	});
	it("keeps only each worker's newest key", () => {
		const items = [msg(REPORT("app-1", "needs_input"), { id: "a" }), msg(REPORT("app-1", "done"), { id: "b" }), msg(REPORT("app-2", "done"), { id: "c" })];
		expect([...latestWorkerUpdateKeys(items)].sort()).toEqual(["b:0", "c:0"]);
	});
});

describe("cachedBy", () => {
	it("reads each item once", () => {
		let reads = 0;
		const read = cachedBy((item: object) => { reads += 1; return item; });
		const item = {};
		read(item); read(item);
		expect(reads).toBe(1);
	});
});
