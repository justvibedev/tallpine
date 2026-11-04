import { describe, expect, it, vi } from "vitest";
import { DAEMON_TOKEN_HEADER } from "./daemon-auth";
import { requestStopAgents, shouldStopAgentsOnQuit } from "./quit-agents";

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
