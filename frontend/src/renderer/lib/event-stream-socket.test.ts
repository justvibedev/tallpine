import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { __testing } from "./event-stream-socket";

const { SocketEventSource, StreamSocket } = __testing;
const ORIGIN = "http://127.0.0.1:3101";

class FakeWebSocket {
	static instances: FakeWebSocket[] = [];
	sent: Array<Record<string, string>> = [];
	closed = false;
	onopen: (() => void) | null = null;
	onmessage: ((event: { data: string }) => void) | null = null;
	onclose: (() => void) | null = null;
	constructor(readonly url: string) {
		FakeWebSocket.instances.push(this);
	}
	send(data: string) {
		this.sent.push(JSON.parse(data) as Record<string, string>);
	}
	close() {
		if (this.closed) return;
		this.closed = true;
		this.onclose?.();
	}
	serverOpen() {
		this.onopen?.();
	}
	serverSend(message: Record<string, unknown>) {
		this.onmessage?.({ data: JSON.stringify(message) });
	}
}

function latestSocket(): FakeWebSocket {
	const socket = FakeWebSocket.instances.at(-1);
	if (!socket) throw new Error("no socket");
	return socket;
}

function opened(socket: FakeWebSocket, path: string): string {
	const open = socket.sent.filter((message) => message.op === "open" && message.path === path).at(-1);
	if (!open) throw new Error(`no open for ${path}`);
	return open.id;
}

beforeEach(() => {
	FakeWebSocket.instances = [];
	vi.useFakeTimers();
	vi.stubGlobal("WebSocket", FakeWebSocket);
});

afterEach(() => {
	vi.useRealTimers();
	vi.unstubAllGlobals();
});

describe("SocketEventSource", () => {
	it("carries several streams over one socket with EventSource events", () => {
		const socket = new StreamSocket(ORIGIN);
		const events = new SocketEventSource(`${ORIGIN}/api/v1/events?after=latest`, socket);
		const notifications = new SocketEventSource(`${ORIGIN}/api/v1/notifications/stream`, socket);
		expect(FakeWebSocket.instances).toHaveLength(1);
		const ws = latestSocket();
		expect(ws.url).toBe("ws://127.0.0.1:3101/api/v1/streams");
		ws.serverOpen();
		const eventsId = opened(ws, "/api/v1/events?after=latest");
		const notificationsId = opened(ws, "/api/v1/notifications/stream");
		expect(eventsId).not.toBe(notificationsId);

		const onopen = vi.fn();
		const named = vi.fn();
		const unnamed = vi.fn();
		events.onopen = onopen;
		events.addEventListener("session_updated", named);
		events.onmessage = unnamed;
		expect(events.readyState).toBe(0);
		ws.serverSend({ id: eventsId, type: "open" });
		expect(events.readyState).toBe(1);
		expect(onopen).toHaveBeenCalledTimes(1);
		ws.serverSend({ id: eventsId, type: "event", event: "session_updated", data: '{"id":"s-1"}', lastEventId: "41" });
		ws.serverSend({ id: eventsId, type: "event", data: "plain" });
		// An id-only frame moves the cursor without dispatching.
		ws.serverSend({ id: eventsId, type: "event", lastEventId: "42" });
		expect(named).toHaveBeenCalledTimes(1);
		expect((named.mock.calls[0][0] as MessageEvent).data).toBe('{"id":"s-1"}');
		expect((named.mock.calls[0][0] as MessageEvent).lastEventId).toBe("41");
		expect(unnamed).toHaveBeenCalledTimes(1);
		expect(events.lastEventId).toBe("42");
		expect(notifications.readyState).toBe(0);
	});

	it("reconnects a stream the server ended, resuming from the last event id", () => {
		const socket = new StreamSocket(ORIGIN);
		const stream = new SocketEventSource(`${ORIGIN}/api/v1/events`, socket);
		const ws = latestSocket();
		ws.serverOpen();
		const first = opened(ws, "/api/v1/events");
		ws.serverSend({ id: first, type: "open" });
		ws.serverSend({ id: first, type: "event", event: "x", data: "1", lastEventId: "7", retry: 1500 });
		const onerror = vi.fn();
		stream.onerror = onerror;
		ws.serverSend({ id: first, type: "end" });
		expect(stream.readyState).toBe(0);
		expect(onerror).toHaveBeenCalledTimes(1);
		vi.advanceTimersByTime(1499);
		expect(ws.sent.filter((m) => m.op === "open")).toHaveLength(1);
		vi.advanceTimersByTime(1);
		const reopen = ws.sent.at(-1);
		expect(reopen).toMatchObject({ op: "open", path: "/api/v1/events", lastEventId: "7" });
		expect(reopen?.id).not.toBe(first);
		// Late messages for the ended attempt are ignored.
		ws.serverSend({ id: first, type: "open" });
		expect(stream.readyState).toBe(0);
		ws.serverSend({ id: reopen?.id, type: "open" });
		expect(stream.readyState).toBe(1);
	});

	it("closes for good when the response is not an event stream", () => {
		const socket = new StreamSocket(ORIGIN);
		const stream = new SocketEventSource(`${ORIGIN}/api/v1/sessions/s-1/workspace/events`, socket);
		const ws = latestSocket();
		ws.serverOpen();
		const onerror = vi.fn();
		stream.onerror = onerror;
		ws.serverSend({ id: opened(ws, "/api/v1/sessions/s-1/workspace/events"), type: "error", status: 404 });
		expect(stream.readyState).toBe(2);
		expect(onerror).toHaveBeenCalledTimes(1);
		vi.advanceTimersByTime(60_000);
		expect(ws.sent.filter((m) => m.op === "open")).toHaveLength(1);
	});

	it("tells the daemon when a stream closes and drops an idle socket", () => {
		const socket = new StreamSocket(ORIGIN);
		const stream = new SocketEventSource(`${ORIGIN}/api/v1/notifications/stream`, socket);
		const ws = latestSocket();
		ws.serverOpen();
		const id = opened(ws, "/api/v1/notifications/stream");
		stream.close();
		expect(stream.readyState).toBe(2);
		expect(ws.sent.at(-1)).toEqual({ op: "close", id });
		expect(ws.closed).toBe(false);
		vi.advanceTimersByTime(5_000);
		expect(ws.closed).toBe(true);
	});

	it("reopens every stream on a new socket after the socket drops", () => {
		vi.spyOn(Math, "random").mockReturnValue(0);
		const socket = new StreamSocket(ORIGIN);
		const a = new SocketEventSource(`${ORIGIN}/api/v1/events`, socket);
		const b = new SocketEventSource(`${ORIGIN}/api/v1/notifications/stream`, socket);
		const ws = latestSocket();
		ws.serverOpen();
		ws.serverSend({ id: opened(ws, "/api/v1/events"), type: "open" });
		ws.serverSend({ id: opened(ws, "/api/v1/notifications/stream"), type: "open" });
		const errors = vi.fn();
		a.addEventListener("error", errors);
		b.addEventListener("error", errors);
		ws.close();
		expect(a.readyState).toBe(0);
		expect(b.readyState).toBe(0);
		expect(errors).toHaveBeenCalledTimes(2);
		vi.advanceTimersByTime(3_000);
		expect(FakeWebSocket.instances).toHaveLength(2);
		const next = latestSocket();
		next.serverOpen();
		expect(next.sent.map((m) => m.path).sort()).toEqual(["/api/v1/events", "/api/v1/notifications/stream"]);
	});

	it("falls back to EventSource when the daemon has no stream route", async () => {
		vi.stubGlobal("fetch", vi.fn(async () => new Response("{}", { status: 404 })));
		const socket = new StreamSocket(ORIGIN);
		const stream = new SocketEventSource(`${ORIGIN}/api/v1/events`, socket);
		const onerror = vi.fn();
		stream.onerror = onerror;
		latestSocket().close();
		await vi.waitFor(() => expect(socket.unsupported).toBe(true));
		await vi.advanceTimersByTimeAsync(5_000);
		expect(stream.readyState).toBe(2);
		expect(onerror).toHaveBeenCalled();
		expect(FakeWebSocket.instances).toHaveLength(1);
	});

	it("keeps retrying the socket while the daemon is unreachable", async () => {
		vi.stubGlobal("fetch", vi.fn(async () => {
			throw new TypeError("Failed to fetch");
		}));
		const socket = new StreamSocket(ORIGIN);
		const stream = new SocketEventSource(`${ORIGIN}/api/v1/events`, socket);
		latestSocket().close();
		await vi.advanceTimersByTimeAsync(5_000);
		expect(socket.unsupported).toBe(false);
		expect(stream.readyState).toBe(0);
		expect(FakeWebSocket.instances).toHaveLength(2);
	});
});
