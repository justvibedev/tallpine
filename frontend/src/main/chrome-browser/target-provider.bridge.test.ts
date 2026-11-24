import { describe, expect, it } from "vitest";
import { WebSocket } from "ws";
import { AgentBrowserCDPBridge } from "../agent-browser-cdp-bridge";
import { FakeChromeConnection } from "./chrome-test-helpers";
import { ChromeTargetRegistry } from "./target-provider";

type RPCResponse = { id: number; result?: Record<string, unknown>; error?: { message: string } };

async function connect(endpoint: string): Promise<WebSocket> {
	const socket = new WebSocket(endpoint);
	await new Promise<void>((resolve, reject) => {
		socket.once("open", () => resolve());
		socket.once("error", reject);
	});
	return socket;
}

function rpc(socket: WebSocket, id: number, method: string, params?: Record<string, unknown>, sessionId?: string): Promise<RPCResponse> {
	const response = new Promise<RPCResponse>((resolve) => {
		const listener = (data: Buffer) => {
			const message = JSON.parse(data.toString()) as RPCResponse;
			if (message.id !== id) return;
			socket.off("message", listener);
			resolve(message);
		};
		socket.on("message", listener);
	});
	socket.send(JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) }));
	return response;
}

describe("Chrome page sessions behind the agent-browser bridge (a5)", () => {
	it("refuses Target and Browser methods that reach other tabs", async () => {
		const connection = new FakeChromeConnection();
		const registry = new ChromeTargetRegistry(connection);
		await registry.ready;
		const mine = await registry.forSession("a").createTarget("https://a.example/");
		await registry.forSession("b").createTarget("https://b.example/");
		connection.openPage("HAND", "https://bank.example/");
		const bridge = new AgentBrowserCDPBridge(registry.forSession("a"));
		const socket = await connect(await bridge.start());
		try {
			const attached = await rpc(socket, 1, "Target.attachToTarget", { targetId: mine.id, flatten: true });
			const session = String(attached.result?.sessionId);
			const before = connection.sent.length;
			const refused: [string, Record<string, unknown>][] = [
				["Target.getTargets", {}],
				["Target.attachToTarget", { targetId: "HAND", flatten: true }],
				["Target.createTarget", { url: "about:blank" }],
				["Target.closeTarget", { targetId: "HAND" }],
				["Target.setDiscoverTargets", { discover: true }],
				["Target.getTargetInfo", { targetId: "HAND" }],
				["Target.detachFromTarget", { sessionId: "S-HAND" }],
				["Browser.grantPermissions", { permissions: ["geolocation"] }],
				["Browser.setPermission", { permission: { name: "camera" }, setting: "granted" }],
			];
			let id = 10;
			for (const [method, params] of refused) {
				const response = await rpc(socket, id++, method, params, session);
				expect(response.error?.message, method).toContain("not permitted by Tallpine");
			}
			// None of them reached Chrome.
			expect(connection.sent.slice(before)).toEqual([]);

			for (const [method, params] of [
				["Target.setAutoAttach", { autoAttach: true, waitForDebuggerOnStart: false, flatten: true }],
				["Target.getTargetInfo", {}],
				["Browser.getVersion", {}],
				["Runtime.evaluate", { expression: "1" }],
			] as const) {
				const response = await rpc(socket, id++, method, params, session);
				expect(response.error, method).toBeUndefined();
			}
			expect(connection.sent.slice(before).map((sent) => sent.method)).toEqual([
				"Target.setAutoAttach",
				"Target.getTargetInfo",
				"Browser.getVersion",
				"Runtime.evaluate",
			]);
		} finally {
			socket.close();
			await bridge.close();
		}
	});
});
