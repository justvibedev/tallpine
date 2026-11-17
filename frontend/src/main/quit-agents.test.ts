import { describe, expect, it, vi } from "vitest";
import { DAEMON_TOKEN_HEADER } from "./daemon-auth";
import {
	askQuitAgentsChoice,
	confirmAndStopAllAgents,
	fetchRunningAgentCount,
	parksAgentsOnQuit,
	requestStopAgents,
	requestStopAllAgents,
	shouldStopAgentsOnQuit,
} from "./quit-agents";
import { trayText } from "./tray-text";

describe("stopping agents on a full quit", () => {
	it("asks only an app-owned daemon the app is linked to", () => {
		const owned = { ready: true, port: 3101, linked: true, keepAlive: false };
		expect(shouldStopAgentsOnQuit(owned)).toBe(true);
		expect(shouldStopAgentsOnQuit({ ...owned, linked: false })).toBe(false);
		expect(shouldStopAgentsOnQuit({ ...owned, keepAlive: true })).toBe(false);
		expect(shouldStopAgentsOnQuit({ ...owned, ready: false })).toBe(false);
		expect(shouldStopAgentsOnQuit({ ...owned, port: undefined })).toBe(false);
	});

	it("posts stopAgents with the daemon token", async () => {
		const fetchImpl = vi.fn(async () => new Response("{}", { status: 202 }));
		await expect(requestStopAgents(3101, "secret", fetchImpl as unknown as typeof fetch)).resolves.toBe(true);
		const [url, init] = fetchImpl.mock.calls[0] as unknown as [string, RequestInit];
		expect(url).toBe("http://127.0.0.1:3101/shutdown");
		expect(init.method).toBe("POST");
		expect(JSON.parse(String(init.body))).toEqual({ stopAgents: true });
		expect((init.headers as Record<string, string>)[DAEMON_TOKEN_HEADER]).toBe("secret");
	});

	it("never throws when the daemon is already gone", async () => {
		const fetchImpl = vi.fn(async () => {
			throw new Error("ECONNREFUSED");
		});
		await expect(requestStopAgents(3101, undefined, fetchImpl as unknown as typeof fetch)).resolves.toBe(false);
	});
});

describe("B753: what a quit does with the agents still running", () => {
	const owned = { ready: true, port: 3101, linked: true, keepAlive: false };
	const persistent = { ...owned, linked: false, keepAlive: true };

	it("parks as the owner chose, and by default only for an app-owned daemon", () => {
		expect(parksAgentsOnQuit(undefined, owned)).toBe(true);
		expect(parksAgentsOnQuit(undefined, persistent)).toBe(false);
		expect(parksAgentsOnQuit("keep", owned)).toBe(false);
		expect(parksAgentsOnQuit("pause", persistent)).toBe(true);
		expect(parksAgentsOnQuit("pause", { ...persistent, ready: false })).toBe(false);
		expect(parksAgentsOnQuit("stop", owned)).toBe(true);
		expect(parksAgentsOnQuit("stop", persistent)).toBe(false);
	});

	it("counts the running agents, and returns null when the daemon cannot say", async () => {
		const ok = vi.fn(async () => Response.json({ sessionIds: ["a-1", "a-2"] }));
		await expect(fetchRunningAgentCount(3101, "secret", ok as unknown as typeof fetch)).resolves.toBe(2);
		const [url, init] = ok.mock.calls[0] as unknown as [string, RequestInit];
		expect(url).toBe("http://127.0.0.1:3101/api/v1/sessions/running-agents");
		expect((init.headers as Record<string, string>)[DAEMON_TOKEN_HEADER]).toBe("secret");
		const missing = vi.fn(async () => new Response("", { status: 404 }));
		await expect(fetchRunningAgentCount(3101, null, missing as unknown as typeof fetch)).resolves.toBeNull();
		const gone = vi.fn(async () => { throw new Error("ECONNREFUSED"); });
		await expect(fetchRunningAgentCount(3101, null, gone as unknown as typeof fetch)).resolves.toBeNull();
	});

	it("stops every agent and reports the ones that would not stop", async () => {
		const fetchImpl = vi.fn(async () => Response.json({ ok: false, stopped: ["a-1"], failures: ["a-2: busy"] }));
		await expect(requestStopAllAgents(3101, "secret", fetchImpl as unknown as typeof fetch)).resolves.toEqual({
			stopped: 1,
			failures: ["a-2: busy"],
		});
		const [url, init] = fetchImpl.mock.calls[0] as unknown as [string, RequestInit];
		expect(url).toBe("http://127.0.0.1:3101/api/v1/sessions/running-agents/stop");
		expect(init.method).toBe("POST");
	});

	it("asks with the count and maps each button to its choice", async () => {
		const text = trayText("en");
		const answers: Array<[number, string]> = [[0, "pause"], [1, "stop"], [2, "keep"], [3, "cancel"]];
		for (const [response, choice] of answers) {
			const show = vi.fn(async (_options: object) => ({ response }));
			await expect(askQuitAgentsChoice(3, text, show)).resolves.toBe(choice);
			expect(show.mock.calls[0][0]).toMatchObject({
				message: "3 sessions are still running",
				buttons: ["Pause Agents", "Stop Agents", "Keep Running", "Cancel"],
				cancelId: 3,
			});
		}
	});

	it("says Pause quits a daemon that outlives the app, and defaults to Keep Running there", async () => {
		const text = trayText("en");
		const owned = vi.fn(async (_options: object) => ({ response: 0 }));
		await askQuitAgentsChoice(2, text, owned);
		expect(owned.mock.calls[0][0]).toMatchObject({ defaultId: 0 });
		expect((owned.mock.calls[0][0] as { detail: string }).detail).not.toContain("background service");

		const outlives = vi.fn(async (_options: object) => ({ response: 2 }));
		await expect(askQuitAgentsChoice(2, text, outlives, true)).resolves.toBe("keep");
		const shown = outlives.mock.calls[0][0] as { detail: string; defaultId: number };
		expect(shown.defaultId).toBe(2);
		expect(shown.detail).toContain("Pause also quits the Tallpine background service");
		// Pause there still parks through the daemon's shutdown, which the copy now says.
		expect(parksAgentsOnQuit("pause", { ready: true, port: 3101, linked: false, keepAlive: true })).toBe(true);
	});

	it("tray Stop All Agents says when the background service cannot be reached", async () => {
		const show = vi.fn(async (_options: object) => ({ response: 0 }));
		const stopAll = vi.fn(async () => null);
		await confirmAndStopAllAgents({ countRunning: async () => null, stopAll, text: trayText("en"), showMessageBox: show });
		expect(show.mock.calls[0][0]).toMatchObject({ message: "Tallpine could not reach its background service" });
		expect(stopAll).not.toHaveBeenCalled();
	});

	it("tray Stop All Agents asks first and stops only when confirmed", async () => {
		const text = trayText("en");
		const stopAll = vi.fn(async () => ({ stopped: 2, failures: [] }));
		const cancelled = vi.fn(async (_options: object) => ({ response: 1 }));
		await confirmAndStopAllAgents({ countRunning: async () => 2, stopAll, text, showMessageBox: cancelled });
		expect(cancelled.mock.calls[0][0]).toMatchObject({ message: "Stop 2 running agents?" });
		expect(stopAll).not.toHaveBeenCalled();

		const confirmed = vi.fn(async () => ({ response: 0 }));
		await confirmAndStopAllAgents({ countRunning: async () => 2, stopAll, text, showMessageBox: confirmed });
		expect(stopAll).toHaveBeenCalledOnce();
		expect(confirmed).toHaveBeenCalledOnce();
	});

	it("tray Stop All Agents says when nothing runs and when some would not stop", async () => {
		const text = trayText("en");
		const show = vi.fn(async (_options: object) => ({ response: 0 }));
		const stopAll = vi.fn(async () => ({ stopped: 1, failures: ["a-2: busy"] }));
		await confirmAndStopAllAgents({ countRunning: async () => 0, stopAll, text, showMessageBox: show });
		expect(show.mock.calls[0][0]).toMatchObject({ message: "No agents are running" });
		expect(stopAll).not.toHaveBeenCalled();

		show.mockClear();
		await confirmAndStopAllAgents({ countRunning: async () => 2, stopAll, text, showMessageBox: show });
		expect(show.mock.calls[1][0]).toMatchObject({ message: "Some agents could not be stopped", detail: "a-2: busy" });
	});
});
