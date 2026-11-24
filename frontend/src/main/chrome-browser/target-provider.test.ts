import { describe, expect, it, vi } from "vitest";
import { FakeChromeConnection } from "./chrome-test-helpers";
import { ChromeTargetRegistry } from "./target-provider";

async function setup() {
	const connection = new FakeChromeConnection();
	const registry = new ChromeTargetRegistry(connection);
	await registry.ready;
	return { connection, registry, a: registry.forSession("a"), b: registry.forSession("b") };
}

const ids = (targets: { id: string }[]) => targets.map((target) => target.id);

describe("ChromeSessionTargets", () => {
	it("lists only the session's own tabs, never another session's or hand-opened ones", async () => {
		const { connection, a, b } = await setup();
		const mine = await a.createTarget("https://a.example/");
		const theirs = await b.createTarget("https://b.example/");
		connection.openPage("HAND", "https://bank.example/");
		expect(ids(a.listTargets())).toEqual([mine.id]);
		expect(ids(b.listTargets())).toEqual([theirs.id]);
		expect(a.listTargets()[0].url).toBe("https://a.example/");
		await expect(a.closeTarget(theirs.id)).rejects.toThrow("outside this Tallpine worker");
		await expect(a.activateTarget("HAND")).rejects.toThrow("outside this Tallpine worker");
		expect(() => a.debuggerFor("HAND")).toThrow("outside this Tallpine worker");
	});

	it("adopts a popup whose opener the session owns, and no other", async () => {
		const { connection, registry, a, b } = await setup();
		const adopted = vi.fn();
		registry.onChange((sessionId, change, targetId) => change === "adopted" && adopted(sessionId, targetId));
		const opener = await a.createTarget("https://a.example/");
		connection.openPage("POPUP", "https://accounts.example/", opener.id);
		connection.openPage("HANDPOPUP", "https://x.example/", "HAND");
		expect(ids(a.listTargets())).toEqual([opener.id, "POPUP"]);
		expect(ids(b.listTargets())).toEqual([]);
		expect(adopted).toHaveBeenCalledWith("a", "POPUP");
		expect(adopted).toHaveBeenCalledTimes(1);
	});

	it("opens the session's first tab in a new window and later ones in it", async () => {
		const { connection, a } = await setup();
		await a.createTarget("about:blank");
		await a.createTarget("about:blank");
		const creates = connection.sent.filter((entry) => entry.method === "Target.createTarget").map((entry) => entry.params.newWindow);
		expect(creates).toEqual([true, false]);
		expect(connection.methods()[0]).toBe("Target.setDiscoverTargets");
	});

	it("opens a new window again after the person closed the session's window", async () => {
		const { connection, a } = await setup();
		const first = await a.createTarget("about:blank");
		// The person closes the Chrome window; on macOS Chrome keeps running with
		// no window, so joining one would fail with "no browser is open".
		connection.emit({ method: "Target.targetDestroyed", params: { targetId: first.id } });
		await a.createTarget("https://analytics.example/");
		const creates = connection.sent.filter((entry) => entry.method === "Target.createTarget").map((entry) => entry.params.newWindow);
		expect(creates).toEqual([true, true]);
	});

	it("retries in a new window when Chrome has no window to join", async () => {
		const { connection, a, b } = await setup();
		await a.createTarget("about:blank");
		const create = connection.handlers["Target.createTarget"];
		connection.handlers["Target.createTarget"] = (params, sessionId) => {
			if (params.newWindow === false) throw new Error("Failed to open new tab - no browser is open");
			return create(params, sessionId);
		};
		const tab = await a.createTarget("https://a.example/");
		expect(ids(a.listTargets())).toContain(tab.id);
		const creates = connection.sent.filter((entry) => entry.method === "Target.createTarget").map((entry) => entry.params.newWindow);
		expect(creates).toEqual([true, false, true]);
		connection.handlers["Target.createTarget"] = () => {
			throw new Error("Target crashed");
		};
		await expect(b.createTarget("about:blank")).rejects.toThrow("Target crashed");
	});

	it("forgets closed tabs and closes every tab of an ended session", async () => {
		const { connection, registry, a, b } = await setup();
		await a.createTarget("about:blank");
		await a.createTarget("about:blank");
		const other = await b.createTarget("about:blank");
		await registry.closeSession("a");
		expect(a.listTargets()).toEqual([]);
		expect(ids(b.listTargets())).toEqual([other.id]);
		expect(connection.sent.filter((entry) => entry.method === "Target.closeTarget").map((entry) => entry.params.targetId)).toEqual(["T1", "T2"]);
	});
});

describe("ChromeTargetDebugger", () => {
	it("attaches a flattened session and routes commands and events through it", async () => {
		const { connection, a } = await setup();
		const target = await a.createTarget("about:blank");
		const debug = a.debuggerFor(target.id);
		const messages: unknown[][] = [];
		debug.on("message", (...args: unknown[]) => messages.push(args.slice(1)));
		debug.attach();
		expect(debug.isAttached()).toBe(true);
		await debug.sendCommand("Page.enable");
		expect(connection.sent.find((entry) => entry.method === "Target.attachToTarget")?.params).toEqual({ targetId: "T1", flatten: true });
		expect(connection.sent.at(-1)).toEqual({ method: "Page.enable", params: {}, sessionId: "S-T1" });
		connection.emit({ method: "Page.loadEventFired", params: {}, sessionId: "S-T1" });
		connection.emit({ method: "Page.loadEventFired", params: {}, sessionId: "S-OTHER" });
		connection.emit({ method: "Target.attachedToTarget", params: { sessionId: "CHILD" }, sessionId: "S-T1" });
		connection.emit({ method: "Runtime.consoleAPICalled", params: {}, sessionId: "CHILD" });
		expect(messages).toEqual([
			["Page.loadEventFired", {}, undefined],
			["Target.attachedToTarget", { sessionId: "CHILD" }, undefined],
			["Runtime.consoleAPICalled", {}, "CHILD"],
		]);
		await debug.sendCommand("Runtime.enable", {}, "CHILD");
		expect(connection.sent.at(-1)?.sessionId).toBe("CHILD");
		await expect(debug.sendCommand("Runtime.enable", {}, "NOT-MINE")).rejects.toThrow("Unknown child target session");
	});

	it("emits detach when the tab closes", async () => {
		const { a } = await setup();
		const target = await a.createTarget("about:blank");
		const debug = a.debuggerFor(target.id);
		const detached = vi.fn();
		debug.on("detach", detached);
		debug.attach();
		await debug.sendCommand("Page.enable");
		await a.closeTarget(target.id);
		expect(detached).toHaveBeenCalledTimes(1);
		expect(debug.isAttached()).toBe(false);
	});
});
