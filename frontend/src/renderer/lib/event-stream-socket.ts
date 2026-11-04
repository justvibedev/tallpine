import { getApiBaseUrl, hasTrustedApiBaseUrl } from "./api-client";
import { usesPreviewWorkspaceData } from "./preview-mode";
import { computeSseRetryDelayMs } from "./sse-backoff";
import { daemonWebSocketImpl } from "./daemon-websocket";

/**
 * The local daemon's SSE streams, carried over one WebSocket.
 *
 * Chromium opens at most six HTTP/1.1 connections per host, and every
 * EventSource holds one while it is open: the three app-wide streams plus the
 * viewed session's and each side chat's left one or two connections for all
 * REST traffic, so one slow response stalled every other request. WebSockets
 * do not count against that pool. The daemon serves each stream through its
 * own router (`/api/v1/streams`, backend/internal/httpd/event_streams.go), so
 * the token Electron main attaches to the upgrade covers every stream.
 *
 * `openEventStream` returns an object with EventSource's behavior (readyState,
 * open/message/error, automatic reconnect with Last-Event-ID, CLOSED on a
 * failed response), so call sites keep their EventSource logic. Other hosts,
 * the browser preview, and a daemon without the route get a real EventSource.
 */

const CONNECTING = 0;
const OPEN = 1;
const CLOSED = 2;
const DEFAULT_RECONNECT_MS = 3_000;
// Keeps the socket across a quick stream swap (switching sessions).
const IDLE_SOCKET_CLOSE_MS = 5_000;
const STREAMS_PATH = "/api/v1/streams";

type ServerMessage = {
	id: string;
	type: "open" | "event" | "error" | "end";
	event?: string;
	data?: string;
	lastEventId?: string;
	retry?: number;
	status?: number;
};

// jsdom has no EventSource: tests stub one and keep exercising it directly.
const platformHasEventSource = typeof EventSource !== "undefined";

let nextAttemptId = 0;

class StreamSocket {
	unsupported = false;
	private socket: WebSocket | undefined;
	private opened = false;
	private failures = 0;
	private idleTimer: ReturnType<typeof setTimeout> | undefined;
	private readonly attempts = new Map<string, SocketEventSource>();

	constructor(private readonly origin: string) {}

	attach(stream: SocketEventSource, attemptId: string): void {
		if (this.idleTimer !== undefined) {
			clearTimeout(this.idleTimer);
			this.idleTimer = undefined;
		}
		this.attempts.set(attemptId, stream);
		if (this.opened) this.sendOpen(attemptId, stream);
		else this.connect();
	}

	detach(attemptId: string, notifyServer: boolean): void {
		if (!this.attempts.delete(attemptId)) return;
		if (notifyServer && this.opened) this.send({ op: "close", id: attemptId });
		if (this.attempts.size === 0 && this.idleTimer === undefined) {
			this.idleTimer = setTimeout(() => {
				this.idleTimer = undefined;
				if (this.attempts.size > 0) return;
				const socket = this.socket;
				this.socket = undefined;
				this.opened = false;
				socket?.close();
			}, IDLE_SOCKET_CLOSE_MS);
		}
	}

	/** The pause before streams retry after the socket failed. */
	retryDelayMs(): number {
		return computeSseRetryDelayMs(Math.max(this.failures, 1));
	}

	private sendOpen(attemptId: string, stream: SocketEventSource): void {
		this.send({ op: "open", id: attemptId, path: stream.path, ...(stream.lastEventId ? { lastEventId: stream.lastEventId } : {}) });
	}

	private send(message: Record<string, string>): void {
		try {
			this.socket?.send(JSON.stringify(message));
		} catch {
			// A failed send surfaces as the socket's close.
		}
	}

	private connect(): void {
		if (this.socket) return;
		let socket: WebSocket;
		try {
			const url = `${this.origin.replace(/^http/i, "ws")}${STREAMS_PATH}`;
			const SocketImpl = daemonWebSocketImpl(url);
			socket = new SocketImpl(url);
		} catch {
			this.lost(false);
			return;
		}
		this.socket = socket;
		socket.onopen = () => {
			if (this.socket !== socket) return;
			this.opened = true;
			this.failures = 0;
			for (const [attemptId, stream] of this.attempts) this.sendOpen(attemptId, stream);
		};
		socket.onmessage = (event) => {
			if (this.socket !== socket || typeof event.data !== "string") return;
			let message: ServerMessage;
			try {
				message = JSON.parse(event.data) as ServerMessage;
			} catch {
				return;
			}
			this.attempts.get(message.id)?.receive(message);
		};
		socket.onclose = () => {
			if (this.socket !== socket) return;
			const wasOpen = this.opened;
			this.socket = undefined;
			this.opened = false;
			this.lost(wasOpen);
		};
	}

	private lost(wasOpen: boolean): void {
		if (!wasOpen) this.failures += 1;
		const affected = [...this.attempts.values()];
		this.attempts.clear();
		for (const stream of affected) stream.socketLost();
		// A socket that never opened is either a daemon that is down (the
		// streams retry with backoff) or one without the route: only the second
		// answers a plain GET with 404, and then real EventSources take over.
		if (!wasOpen) void this.probeRoute();
	}

	private async probeRoute(): Promise<void> {
		if (this.unsupported) return;
		try {
			const response = await fetch(`${this.origin}${STREAMS_PATH}`, { cache: "no-store" });
			if (response.status === 404 || response.status === 405) {
				this.unsupported = true;
			}
		} catch {
			// The daemon is unreachable; keep retrying the socket.
		}
	}
}

class SocketEventSource extends EventTarget {
	static readonly CONNECTING = CONNECTING;
	static readonly OPEN = OPEN;
	static readonly CLOSED = CLOSED;
	readonly CONNECTING = CONNECTING;
	readonly OPEN = OPEN;
	readonly CLOSED = CLOSED;
	readonly withCredentials = false;
	readonly path: string;
	readyState: number = CONNECTING;
	lastEventId = "";
	onopen: ((this: EventSource, event: Event) => unknown) | null = null;
	onmessage: ((this: EventSource, event: MessageEvent) => unknown) | null = null;
	onerror: ((this: EventSource, event: Event) => unknown) | null = null;
	private reconnectMs = DEFAULT_RECONNECT_MS;
	private attemptId = "";
	private reconnectTimer: ReturnType<typeof setTimeout> | undefined;
	private readonly origin: string;

	constructor(
		readonly url: string,
		private readonly socket: StreamSocket,
	) {
		super();
		const target = new URL(url);
		this.origin = target.origin;
		this.path = `${target.pathname}${target.search}`;
		this.attach();
	}

	close(): void {
		if (this.readyState === CLOSED) return;
		this.readyState = CLOSED;
		if (this.reconnectTimer !== undefined) clearTimeout(this.reconnectTimer);
		this.reconnectTimer = undefined;
		this.socket.detach(this.attemptId, true);
	}

	receive(message: ServerMessage): void {
		if (this.readyState === CLOSED) return;
		switch (message.type) {
			case "open":
				this.readyState = OPEN;
				this.fire(new Event("open"));
				return;
			case "event":
				if (message.lastEventId !== undefined) this.lastEventId = message.lastEventId;
				if (message.retry !== undefined) this.reconnectMs = message.retry;
				if (message.data === undefined) return;
				this.fire(
					new MessageEvent(message.event || "message", {
						data: message.data,
						lastEventId: this.lastEventId,
						origin: this.origin,
					}),
				);
				return;
			case "error":
				// EventSource does not retry a response that is not an event stream.
				this.socket.detach(this.attemptId, false);
				this.readyState = CLOSED;
				this.fire(new Event("error"));
				return;
			case "end":
				this.socket.detach(this.attemptId, false);
				this.reconnect(this.reconnectMs);
				return;
		}
	}

	socketLost(): void {
		if (this.readyState === CLOSED) return;
		if (this.socket.unsupported) {
			this.readyState = CLOSED;
			this.fire(new Event("error"));
			return;
		}
		this.reconnect(Math.max(this.reconnectMs, this.socket.retryDelayMs()));
	}

	private attach(): void {
		this.attemptId = String(++nextAttemptId);
		this.socket.attach(this, this.attemptId);
	}

	private reconnect(delayMs: number): void {
		this.readyState = CONNECTING;
		this.fire(new Event("error"));
		if (this.readyState !== CONNECTING || this.reconnectTimer !== undefined) return;
		this.reconnectTimer = setTimeout(() => {
			this.reconnectTimer = undefined;
			if (this.readyState !== CONNECTING) return;
			if (this.socket.unsupported) {
				this.readyState = CLOSED;
				this.fire(new Event("error"));
				return;
			}
			this.attach();
		}, delayMs);
	}

	private fire(event: Event): void {
		this.dispatchEvent(event);
		const self = this as unknown as EventSource;
		if (event.type === "open") this.onopen?.call(self, event);
		else if (event.type === "error") this.onerror?.call(self, event);
		else if (event.type === "message") this.onmessage?.call(self, event as MessageEvent);
	}
}

const sockets = new Map<string, StreamSocket>();

function socketFor(url: string): StreamSocket | undefined {
	if (!platformHasEventSource || usesPreviewWorkspaceData || typeof WebSocket === "undefined" || !hasTrustedApiBaseUrl()) {
		return undefined;
	}
	let target: URL;
	let daemon: URL;
	try {
		target = new URL(url);
		daemon = new URL(getApiBaseUrl());
	} catch {
		return undefined;
	}
	// Only the local daemon: remote hosts may run a daemon without the route.
	if (target.protocol !== "http:" || target.hostname !== "127.0.0.1" || target.origin !== daemon.origin) return undefined;
	let socket = sockets.get(target.origin);
	if (!socket) {
		socket = new StreamSocket(target.origin);
		sockets.set(target.origin, socket);
	}
	return socket.unsupported ? undefined : socket;
}

/** An EventSource for `url`, multiplexed over the daemon socket when `url` is on the local daemon. */
export function openEventStream(url: string): EventSource {
	const socket = socketFor(url);
	return socket ? (new SocketEventSource(url, socket) as unknown as EventSource) : new EventSource(url);
}

export const __testing = { SocketEventSource, StreamSocket, sockets };
