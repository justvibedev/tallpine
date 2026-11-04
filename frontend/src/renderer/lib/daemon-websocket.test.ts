import { afterEach, describe, expect, it, vi } from "vitest";
import type { DaemonWsEvent } from "../../shared/daemon-ws";
import { BridgedWebSocket, daemonWebSocketImpl, isBridgedDaemonUrl, resetDaemonWebSocketsForTest } from "./daemon-websocket";
import { setApiBaseUrl } from "./api-client";
import { createTerminalMux } from "./terminal-mux";

function fakeApi(openResult: number | null = 5) {
	let listener: ((event: DaemonWsEvent) => void) | undefined;
	let resolveOpen: ((id: number | null) => void) | undefined;
	const api = {
		open: vi.fn((_url: string) => new Promise<number | null>((resolve) => (resolveOpen = resolve))),
		send: vi.fn(),
		close: vi.fn(),
		onEvent: vi.fn((next: (event: DaemonWsEvent) => void) => {
			listener = next;
			return () => (listener = undefined);
		}),
	};
	return {
		api,
		emit: (event: DaemonWsEvent) => listener?.(event),
		resolveOpen: (id: number | null = openResult) => resolveOpen?.(id),
	};
}

afterEach(() => {
	resetDaemonWebSocketsForTest();
	setApiBaseUrl(null);
	delete (window.tallpine as { daemonSocket?: unknown }).daemonSocket;
});

describe("BridgedWebSocket (B176)", () => {
	it("opens through main, keeps an open main reported before the id arrived, and relays frames", async () => {
		const { api, emit, resolveOpen } = fakeApi();
		const socket = new BridgedWebSocket("ws://127.0.0.1:3101/mux", api);
		const opened = vi.fn();
		const messages: string[] = [];
		socket.addEventListener("open", opened);
		socket.onmessage = (event) => messages.push(event.data as string);

		emit({ id: 5, type: "open" }); // before open() resolved
		expect(socket.readyState).toBe(BridgedWebSocket.CONNECTING);
		resolveOpen();
		await Promise.resolve();
		await Promise.resolve();
		expect(api.open).toHaveBeenCalledWith("ws://127.0.0.1:3101/mux");
		expect(opened).toHaveBeenCalledTimes(1);
		expect(socket.readyState).toBe(BridgedWebSocket.OPEN);

		socket.send('{"ch":"system"}');
		expect(api.send).toHaveBeenCalledWith(5, '{"ch":"system"}');
		emit({ id: 5, type: "message", data: "hello" });
		emit({ id: 6, type: "message", data: "someone else's" });
		expect(messages).toEqual(["hello"]);

		const closed = vi.fn();
		socket.onclose = closed;
		socket.close();
		expect(api.close).toHaveBeenCalledWith(5);
		emit({ id: 5, type: "close", code: 1000, reason: "" });
		expect(closed).toHaveBeenCalledTimes(1);
		expect(socket.readyState).toBe(BridgedWebSocket.CLOSED);
	});

	it("errors and closes when main refuses the socket", async () => {
		const { api, resolveOpen } = fakeApi(null);
		const socket = new BridgedWebSocket("ws://127.0.0.1:3101/mux", api);
		const order: string[] = [];
		socket.onerror = () => order.push("error");
		socket.onclose = () => order.push("close");
		resolveOpen();
		await Promise.resolve();
		await Promise.resolve();
		expect(order).toEqual(["error", "close"]);
		expect(() => socket.send("x")).toThrow();
	});

	it("bridges only the local daemon's mux and stream routes", () => {
		expect(isBridgedDaemonUrl("ws://127.0.0.1:3101/mux", "http://127.0.0.1:3101")).toBe(true);
		expect(isBridgedDaemonUrl("ws://127.0.0.1:3101/api/v1/streams", "http://127.0.0.1:3101")).toBe(true);
		// A remote host's proxy is also on loopback, on its own port and path.
		expect(isBridgedDaemonUrl("ws://127.0.0.1:52000/tok/mux", "http://127.0.0.1:3101")).toBe(false);
		expect(isBridgedDaemonUrl("ws://127.0.0.1:52000/mux", "http://127.0.0.1:3101")).toBe(false);
		expect(isBridgedDaemonUrl("ws://127.0.0.1:3101/mux", "")).toBe(false);
	});

	it("is what the terminal mux uses for the local daemon when the app has the bridge", () => {
		setApiBaseUrl("http://127.0.0.1:3101");
		expect(daemonWebSocketImpl("ws://127.0.0.1:3101/mux")).toBe(WebSocket);
		const { api } = fakeApi();
		(window.tallpine as { daemonSocket?: unknown }).daemonSocket = api;
		expect(daemonWebSocketImpl("ws://127.0.0.1:3101/mux")).toBe(BridgedWebSocket);
		expect(daemonWebSocketImpl("ws://127.0.0.1:52000/tok/mux")).toBe(WebSocket);

		const mux = createTerminalMux("ws://127.0.0.1:3101/mux");
		expect(api.open).toHaveBeenCalledWith("ws://127.0.0.1:3101/mux");
		mux.dispose();
	});
});
