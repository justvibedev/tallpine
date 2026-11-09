import { describe, expect, it } from "vitest";
import { attachProcessRecovery } from "./process-recovery";

function setup(over: { destroyed?: boolean; drafts?: { v: boolean } } = {}) {
	const queue: Array<() => void> = [];
	const handlers = new Map<string, (...a: any[]) => void>();
	const appHandlers = new Map<string, (...a: any[]) => void>();
	let reloads = 0;
	const logs: string[] = [];
	let t = 0;
	const contents: any = {
		isDestroyed: () => over.destroyed ?? false,
		reload: () => void reloads++,
		on: (e: string, l: any) => void handlers.set(e, l),
		removeListener: (e: string) => void handlers.delete(e),
	};
	const app: any = { on: (e: string, l: any) => void appHandlers.set(e, l), removeListener: (e: string) => void appHandlers.delete(e) };
	const dispose = attachProcessRecovery({ app, contents, log: (m) => logs.push(m), now: () => t, schedule: (fn) => void queue.push(fn), hasUnsavedDrafts: () => over.drafts?.v ?? false });
	const flush = () => { for (const f of queue.splice(0)) f(); };
	return { flush, queue, handlers, appHandlers, logs, dispose, reloads: () => reloads, tick: (n: number) => void (t += n), contents };
}

describe("attachProcessRecovery", () => {
	it("reloads the shell after a renderer crash and logs it", () => {
		const s = setup();
		s.handlers.get("render-process-gone")!({}, { reason: "crashed", exitCode: 11 });
		s.flush();
		expect(s.reloads()).toBe(1);
		expect(s.logs.join("\n")).toContain("reason=crashed");
	});
	it("does not reload on clean exit", () => {
		const s = setup();
		s.handlers.get("render-process-gone")!({}, { reason: "clean-exit", exitCode: 0 });
		expect(s.reloads()).toBe(0);
	});
	it("reloads after a GPU process crash, ignores other child processes", () => {
		const s = setup();
		s.appHandlers.get("child-process-gone")!({}, { type: "Utility", reason: "crashed", exitCode: 1 });
		expect(s.reloads()).toBe(0);
		s.appHandlers.get("child-process-gone")!({}, { type: "GPU", reason: "crashed", exitCode: 1 });
		s.flush();
		expect(s.reloads()).toBe(1);
	});
	it("stops reloading in a crash loop, then resumes after the window", () => {
		const s = setup();
		for (let i = 0; i < 3; i++) { s.handlers.get("render-process-gone")!({}, { reason: "crashed", exitCode: 1 }); s.flush(); }
		expect(s.reloads()).toBe(3);
		s.handlers.get("render-process-gone")!({}, { reason: "oom", exitCode: 1 });
		s.flush();
		expect(s.reloads()).toBe(3);
		s.tick(61_000);
		s.flush();
		expect(s.reloads()).toBe(4);
	});
	it("skips a destroyed window and detaches on dispose", () => {
		const s = setup({ destroyed: true });
		s.handlers.get("render-process-gone")!({}, { reason: "crashed", exitCode: 1 });
		s.flush();
		expect(s.reloads()).toBe(0);
		s.dispose();
		expect(s.appHandlers.size).toBe(0);
		expect(s.handlers.size).toBe(0);
	});
	it("defers a GPU reload while a chat draft is unsaved, then runs it", () => {
		const drafts = { v: true };
		const s = setup({ drafts });
		s.appHandlers.get("child-process-gone")!({}, { type: "GPU", reason: "crashed", exitCode: 1 });
		s.flush();
		expect(s.reloads()).toBe(0);
		expect(s.logs.join()).toContain("deferred: unsaved chat draft");
		drafts.v = false;
		s.flush();
		expect(s.reloads()).toBe(1);
	});
	it("a renderer loss overrides a draft deferral", () => {
		const s = setup({ drafts: { v: true } });
		s.appHandlers.get("child-process-gone")!({}, { type: "GPU", reason: "crashed", exitCode: 1 });
		s.flush();
		s.handlers.get("render-process-gone")!({}, { reason: "crashed", exitCode: 1 });
		s.flush();
		expect(s.reloads()).toBe(1);
	});
	it("bounds the draft deferral and logs it once", () => {
		const s = setup({ drafts: { v: true } });
		s.appHandlers.get("child-process-gone")!({}, { type: "GPU", reason: "crashed", exitCode: 1 });
		s.flush();
		s.flush();
		expect(s.reloads()).toBe(0);
		expect(s.logs.filter((l) => l.includes("unsaved chat draft"))).toHaveLength(1);
		s.tick(46_000);
		s.flush();
		expect(s.reloads()).toBe(1);
	});
	it("coalesces a GPU plus renderer loss into one reload", () => {
		const s = setup();
		s.appHandlers.get("child-process-gone")!({}, { type: "GPU", reason: "crashed", exitCode: 1 });
		s.handlers.get("render-process-gone")!({}, { reason: "crashed", exitCode: 1 });
		s.flush();
		expect(s.reloads()).toBe(1);
	});
	it("logs unresponsive", () => {
		const s = setup();
		s.handlers.get("unresponsive")!();
		expect(s.logs.join()).toContain("unresponsive");
	});
});
