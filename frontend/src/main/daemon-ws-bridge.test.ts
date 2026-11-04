// @vitest-environment node
import { EventEmitter } from "node:events";
import { mkdirSync, mkdtempSync, rmSync } from "node:fs";
import { createServer, type IncomingHttpHeaders } from "node:http";
import { tmpdir } from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { WebSocketServer, type WebSocket as ServerSocket } from "ws";
import type { DaemonWsEvent } from "../shared/daemon-ws";
import { DAEMON_TOKEN_HEADER } from "./daemon-auth";
import { daemonSocketPath } from "./daemon-socket-fetch";
import { bridgeAddress, bridgedUrl, DaemonWsBridge } from "./daemon-ws-bridge";

const cleanups: Array<() => void> = [];
afterEach(() => {
	while (cleanups.length) cleanups.pop()?.();
});

// A stand-in daemon: a ws server on a real Unix socket that echoes "echo:<msg>".
async function socketDaemon(): Promise<{ socket: string; upgrades: Array<{ url?: string; headers: IncomingHttpHeaders }>; clients: ServerSocket[] }> {
	const dir = mkdtempSync(path.join(tmpdir(), "dw"));
	const socket = daemonSocketPath(path.join(dir, "running.json"));
	mkdirSync(path.dirname(socket), { mode: 0o700 });
	const upgrades: Array<{ url?: string; headers: IncomingHttpHeaders }> = [];
	const clients: ServerSocket[] = [];
	const server = createServer();
	const wss = new WebSocketServer({ server });
	wss.on("connection", (ws, req) => {
		upgrades.push({ url: req.url, headers: req.headers });
		clients.push(ws);
		ws.on("message", (data) => ws.send(`echo:${String(data)}`));
	});
	await new Promise<void>((resolve) => server.listen(socket, resolve));
	cleanups.push(() => {
		for (const c of clients) c.terminate();
		wss.close();
		server.close();
		rmSync(dir, { recursive: true, force: true });
	});
	return { socket, upgrades, clients };
}

class FakeSender extends EventEmitter {
	readonly events: DaemonWsEvent[] = [];
	destroyed = false;
	constructor(readonly id: number) {
		super();
	}
	send(_channel: string, event: DaemonWsEvent): void {
		this.events.push(event);
		this.emit("event", event);
	}
	isDestroyed(): boolean {
		return this.destroyed;
	}
	next(type: DaemonWsEvent["type"]): Promise<DaemonWsEvent> {
		const seen = this.events.find((e) => e.type === type);
		if (seen) return Promise.resolve(seen);
		return new Promise((resolve) => {
			const on = (e: DaemonWsEvent) => {
				if (e.type !== type) return;
				this.off("event", on);
				resolve(e);
			};
			this.on("event", on);
		});
	}
}

describe("bridgedUrl", () => {
	it("bridges only the local daemon's /mux and event-stream routes", () => {
		expect(bridgedUrl("ws://127.0.0.1:3101/mux", 3101)?.pathname).toBe("/mux");
		expect(bridgedUrl("ws://127.0.0.1:3101/api/v1/streams", 3101)?.pathname).toBe("/api/v1/streams");
		expect(bridgedUrl("ws://127.0.0.1:3101/api/v1/projects", 3101)).toBeNull();
		expect(bridgedUrl("ws://127.0.0.1:4000/mux", 3101)).toBeNull();
		expect(bridgedUrl("ws://example.com:3101/mux", 3101)).toBeNull();
		expect(bridgedUrl("wss://127.0.0.1:3101/mux", 3101)).toBeNull();
		expect(bridgedUrl(42, 3101)).toBeNull();
		expect(bridgedUrl("ws://127.0.0.1:3101/mux", undefined)).toBeNull();
	});

	it("dials the socket when it exists, else loopback TCP", () => {
		const url = new URL("ws://127.0.0.1:3101/mux?a=1");
		if (process.platform !== "win32") expect(bridgeAddress(url, 3101, "/s/daemon.sock", () => true)).toBe("ws+unix:///s/daemon.sock:/mux?a=1");
		expect(bridgeAddress(url, 3101, "/s/daemon.sock", () => false)).toBe("ws://127.0.0.1:3101/mux?a=1");
	});
});

describe.skipIf(process.platform === "win32")("daemon WebSocket bridge (B176)", () => {
	it("opens the shell's /mux on the daemon socket with the token and relays text both ways", async () => {
		const { socket, upgrades } = await socketDaemon();
		const bridge = new DaemonWsBridge({ port: () => 3101, token: () => "secret-token", socketPath: () => socket });
		const sender = new FakeSender(7);
		const id = bridge.open(sender as never, "ws://127.0.0.1:3101/mux", "app://renderer");
		expect(id).toEqual(expect.any(Number));
		await sender.next("open");
		expect(upgrades[0]!.url).toBe("/mux");
		expect(upgrades[0]!.headers).toMatchObject({ host: "127.0.0.1:3101", origin: "app://renderer", [DAEMON_TOKEN_HEADER.toLowerCase()]: "secret-token" });

		bridge.send(7, id, "ping");
		expect(await sender.next("message")).toEqual({ id, type: "message", data: "echo:ping" });

		bridge.close(7, id);
		expect(await sender.next("close")).toMatchObject({ id, type: "close", code: 1000 });
	});

	it("refuses other routes and ignores sends and closes from another webContents", async () => {
		const { socket, clients } = await socketDaemon();
		const bridge = new DaemonWsBridge({ port: () => 3101, token: () => "t", socketPath: () => socket });
		const owner = new FakeSender(1);
		expect(bridge.open(owner as never, "ws://127.0.0.1:3101/api/v1/projects", null)).toBeNull();
		const id = bridge.open(owner as never, "ws://127.0.0.1:3101/api/v1/streams", null);
		await owner.next("open");
		bridge.send(2, id, "from-other");
		bridge.close(2, id);
		bridge.send(1, id, "from-owner");
		expect(await owner.next("message")).toMatchObject({ data: "echo:from-owner" });
		expect(owner.events.some((e) => e.type === "close")).toBe(false);
		expect(clients).toHaveLength(1);
	});

	it("drops a webContents' sockets when it is destroyed", async () => {
		const { socket, clients } = await socketDaemon();
		const bridge = new DaemonWsBridge({ port: () => 3101, token: () => "t", socketPath: () => socket });
		const sender = new FakeSender(3);
		bridge.open(sender as never, "ws://127.0.0.1:3101/mux", null);
		await sender.next("open");
		const serverClosed = new Promise<void>((resolve) => clients[0]!.once("close", () => resolve()));
		sender.destroyed = true;
		sender.emit("destroyed");
		await serverClosed;
	});

	it("hooks a webContents' destruction once however many sockets it reopens", async () => {
		const { socket } = await socketDaemon();
		const bridge = new DaemonWsBridge({ port: () => 3101, token: () => "t", socketPath: () => socket });
		const sender = new FakeSender(4);
		for (let i = 0; i < 12; i++) {
			const id = bridge.open(sender as never, "ws://127.0.0.1:3101/mux", null);
			bridge.close(4, id);
		}
		expect(sender.listenerCount("destroyed")).toBe(1);
	});
});
