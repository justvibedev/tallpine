import type { DaemonWsEvent } from "../../shared/daemon-ws";
import { getApiBaseUrl } from "./api-client";

/**
 * The local daemon's WebSockets, opened by Electron main over the daemon's
 * user-only socket (B176). The daemon no longer listens on loopback TCP, and a
 * page's WebSocket cannot reach a Unix socket, so main dials it and relays
 * frames over IPC (main/daemon-ws-bridge.ts). BridgedWebSocket has the part of
 * WebSocket's interface the terminal mux and the event-stream socket use.
 * Remote hosts, the browser preview and an app without the bridge keep the
 * native WebSocket.
 */

type DaemonSocketApi = {
	open(url: string): Promise<number | null>;
	send(id: number, data: string): void;
	close(id: number): void;
	onEvent(listener: (event: DaemonWsEvent) => void): () => void;
};

const BRIDGED_PATHS = new Set(["/mux", "/api/v1/streams"]);

function bridge(): DaemonSocketApi | undefined {
	return (window as unknown as { tallpine?: { daemonSocket?: DaemonSocketApi } }).tallpine?.daemonSocket;
}

const live = new Map<number, BridgedWebSocket>();
// Main may report "open" before the renderer has the id from open().
const early = new Map<number, DaemonWsEvent[]>();
let unsubscribe: (() => void) | undefined;

function listen(api: DaemonSocketApi): void {
	if (unsubscribe) return;
	unsubscribe = api.onEvent((event) => {
		const socket = live.get(event.id);
		if (socket) socket.receive(event);
		else early.set(event.id, [...(early.get(event.id) ?? []), event]);
	});
}

export class BridgedWebSocket extends EventTarget {
	static readonly CONNECTING = 0;
	static readonly OPEN = 1;
	static readonly CLOSING = 2;
	static readonly CLOSED = 3;
	readonly CONNECTING = 0;
	readonly OPEN = 1;
	readonly CLOSING = 2;
	readonly CLOSED = 3;

	readyState = BridgedWebSocket.CONNECTING;
	onopen: ((event: Event) => void) | null = null;
	onmessage: ((event: MessageEvent) => void) | null = null;
	onerror: ((event: Event) => void) | null = null;
	onclose: ((event: CloseEvent | Event) => void) | null = null;
	private id: number | null = null;
	private closeRequested = false;

	constructor(
		readonly url: string,
		private readonly api: DaemonSocketApi | undefined = bridge(),
	) {
		super();
		if (!this.api) {
			queueMicrotask(() => this.finish(1006, ""));
			return;
		}
		listen(this.api);
		this.api.open(url).then(
			(id) => this.attach(id),
			() => this.attach(null),
		);
	}

	send(data: string): void {
		if (this.readyState !== BridgedWebSocket.OPEN || this.id === null) {
			throw new DOMException("WebSocket is not open", "InvalidStateError");
		}
		this.api?.send(this.id, data);
	}

	close(): void {
		if (this.readyState === BridgedWebSocket.CLOSED || this.readyState === BridgedWebSocket.CLOSING) return;
		this.readyState = BridgedWebSocket.CLOSING;
		this.closeRequested = true;
		if (this.id !== null) this.api?.close(this.id);
	}

	/** Events main relays for this socket's id. */
	receive(event: DaemonWsEvent): void {
		if (this.readyState === BridgedWebSocket.CLOSED) return;
		switch (event.type) {
			case "open":
				if (this.readyState !== BridgedWebSocket.CONNECTING) return;
				this.readyState = BridgedWebSocket.OPEN;
				this.emit("open", new Event("open"));
				return;
			case "message":
				this.emit("message", new MessageEvent("message", { data: event.data }));
				return;
			case "error":
				this.emit("error", new Event("error"));
				return;
			case "close":
				this.finish(event.code, event.reason);
		}
	}

	private attach(id: number | null): void {
		if (id === null) {
			this.emit("error", new Event("error"));
			this.finish(1006, "");
			return;
		}
		this.id = id;
		live.set(id, this);
		if (this.closeRequested) this.api?.close(id);
		const pending = early.get(id) ?? [];
		early.delete(id);
		for (const event of pending) this.receive(event);
	}

	private finish(code: number, reason: string): void {
		if (this.readyState === BridgedWebSocket.CLOSED) return;
		this.readyState = BridgedWebSocket.CLOSED;
		if (this.id !== null) live.delete(this.id);
		const event = typeof CloseEvent === "function" ? new CloseEvent("close", { code, reason }) : new Event("close");
		this.emit("close", event);
	}

	private emit(type: "open" | "message" | "error" | "close", event: Event): void {
		const handler = this[`on${type}`] as ((event: Event) => void) | null;
		handler?.call(this, event);
		this.dispatchEvent(event);
	}
}

/** Whether `url` is one of the local daemon's bridged WebSocket routes. */
export function isBridgedDaemonUrl(url: string, apiBaseUrl = getApiBaseUrl()): boolean {
	if (!apiBaseUrl) return false;
	let target: URL;
	let base: URL;
	try {
		target = new URL(url);
		base = new URL(apiBaseUrl);
	} catch {
		return false;
	}
	return (
		target.protocol === "ws:" &&
		target.hostname === base.hostname &&
		target.port === base.port &&
		BRIDGED_PATHS.has(target.pathname)
	);
}

/** The WebSocket constructor for `url`: the bridge for the local daemon when the app has one. */
export function daemonWebSocketImpl(url: string): typeof WebSocket {
	if (bridge() && isBridgedDaemonUrl(url)) return BridgedWebSocket as unknown as typeof WebSocket;
	return WebSocket;
}

/** Test seam: forget bridged sockets between tests. */
export function resetDaemonWebSocketsForTest(): void {
	unsubscribe?.();
	unsubscribe = undefined;
	live.clear();
	early.clear();
}
