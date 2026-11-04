// The shell's daemon WebSockets, carried by main over the user-only socket (B176).
//
// Chromium's WebSocket never reaches protocol handlers, so with the daemon off
// loopback TCP the renderer's /mux and event-stream sockets cannot dial it.
// The renderer asks main over IPC instead (shell only: ipc-guard), and main
// opens the WebSocket on the daemon socket with the token, so the renderer
// still never holds it. Only the two routes the shell uses are bridged.
// Text frames only: both protocols are JSON.

import { WebSocket } from "ws";
import { existsSync } from "node:fs";
import { DAEMON_TOKEN_HEADER } from "./daemon-auth";
import { DAEMON_WS_EVENT_CHANNEL, type DaemonWsEvent } from "../shared/daemon-ws";

const BRIDGED_PATHS = new Set(["/mux", "/api/v1/streams"]);

export type DaemonWsBridgeOptions = {
	port: () => number | undefined;
	socketPath: () => string | null;
	token: () => string | null;
	exists?: (file: string) => boolean;
	/** Test seam; the real one is ws's WebSocket. */
	createSocket?: (address: string, options: { headers: Record<string, string> }) => BridgeSocket;
};

export type BridgeSocket = {
	readyState: number;
	on(event: "open", listener: () => void): unknown;
	on(event: "message", listener: (data: unknown, isBinary: boolean) => void): unknown;
	on(event: "error", listener: (error: Error) => void): unknown;
	on(event: "close", listener: (code: number, reason: Buffer) => void): unknown;
	send(data: string): void;
	close(code?: number): void;
	terminate(): void;
};

type Sender = {
	id: number;
	send(channel: string, event: DaemonWsEvent): void;
	isDestroyed(): boolean;
	once(event: "destroyed", listener: () => void): unknown;
};

/** Where a bridged URL goes: the socket when the daemon serves one, else loopback TCP. */
export function bridgeAddress(url: URL, port: number, socketPath: string | null, exists: (file: string) => boolean): string {
	const pathAndQuery = `${url.pathname}${url.search}`;
	if (socketPath && process.platform !== "win32" && exists(socketPath)) return `ws+unix://${socketPath}:${pathAndQuery}`;
	return `ws://127.0.0.1:${port}${pathAndQuery}`;
}

/** The bridged daemon route `rawUrl` names, or null when it is not one. */
export function bridgedUrl(rawUrl: unknown, port: number | undefined): URL | null {
	if (typeof rawUrl !== "string" || !port) return null;
	let url: URL;
	try {
		url = new URL(rawUrl);
	} catch {
		return null;
	}
	if (url.protocol !== "ws:" || url.port !== String(port)) return null;
	if (!["127.0.0.1", "localhost", "[::1]"].includes(url.hostname)) return null;
	return BRIDGED_PATHS.has(url.pathname) ? url : null;
}

export class DaemonWsBridge {
	private nextId = 1;
	private readonly sockets = new Map<number, { socket: BridgeSocket; owner: number }>();
	// Senders whose "destroyed" hook is set: one per webContents, not per
	// socket, since the shell reopens its sockets on every reconnect.
	private readonly watched = new Set<number>();

	constructor(private readonly options: DaemonWsBridgeOptions) {}

	/** Opens a bridged socket for `sender`; returns its id, or null when refused. */
	open(sender: Sender, rawUrl: unknown, origin: string | null): number | null {
		const port = this.options.port();
		const url = bridgedUrl(rawUrl, port);
		if (!url || !port) return null;
		const headers: Record<string, string> = { Host: `127.0.0.1:${port}` };
		if (origin) headers.Origin = origin;
		const token = this.options.token();
		if (token) headers[DAEMON_TOKEN_HEADER] = token;
		const address = bridgeAddress(url, port, this.options.socketPath(), this.options.exists ?? existsSync);
		const create = this.options.createSocket ?? ((target, opts) => new WebSocket(target, opts) as unknown as BridgeSocket);
		const socket = create(address, { headers });
		const id = this.nextId++;
		this.sockets.set(id, { socket, owner: sender.id });
		const emit = (event: DaemonWsEvent) => {
			if (!sender.isDestroyed()) sender.send(DAEMON_WS_EVENT_CHANNEL, event);
		};
		socket.on("open", () => emit({ id, type: "open" }));
		socket.on("message", (data, isBinary) => {
			if (!isBinary) emit({ id, type: "message", data: String(data) });
		});
		socket.on("error", () => emit({ id, type: "error" }));
		socket.on("close", (code, reason) => {
			this.sockets.delete(id);
			emit({ id, type: "close", code, reason: reason.toString() });
		});
		if (!this.watched.has(sender.id)) {
			this.watched.add(sender.id);
			sender.once("destroyed", () => this.closeOwnedBy(sender.id));
		}
		return id;
	}

	send(senderId: number, id: unknown, data: unknown): void {
		const entry = typeof id === "number" ? this.sockets.get(id) : undefined;
		if (!entry || entry.owner !== senderId || typeof data !== "string") return;
		if (entry.socket.readyState === WebSocket.OPEN) entry.socket.send(data);
	}

	close(senderId: number, id: unknown): void {
		const entry = typeof id === "number" ? this.sockets.get(id) : undefined;
		if (!entry || entry.owner !== senderId) return;
		entry.socket.close(1000);
	}

	closeOwnedBy(senderId: number): void {
		this.watched.delete(senderId);
		for (const [id, entry] of this.sockets) {
			if (entry.owner !== senderId) continue;
			this.sockets.delete(id);
			entry.socket.terminate();
		}
	}

	closeAll(): void {
		for (const entry of this.sockets.values()) entry.socket.terminate();
		this.sockets.clear();
	}
}
