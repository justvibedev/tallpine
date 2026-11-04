// @vitest-environment node
import { mkdtempSync, rmSync } from "node:fs";
import { createServer, type Server } from "node:http";
import { tmpdir } from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { DAEMON_TOKEN_HEADER, withDaemonToken } from "./daemon-auth";
import { daemonFetch, daemonSocketPath } from "./daemon-socket-fetch";

const cleanups: Array<() => void> = [];
afterEach(() => {
	while (cleanups.length) cleanups.pop()?.();
});

async function socketServer(): Promise<{ socket: string; seen: Array<{ method?: string; url?: string; host?: string; token?: string; body: string }> }> {
	const dir = mkdtempSync(path.join(tmpdir(), "dsf"));
	const socket = daemonSocketPath(path.join(dir, "running.json"));
	const { mkdirSync } = await import("node:fs");
	mkdirSync(path.dirname(socket), { mode: 0o700 });
	const seen: Array<{ method?: string; url?: string; host?: string; token?: string; body: string }> = [];
	const server: Server = createServer((req, res) => {
		let body = "";
		req.on("data", (c) => (body += c));
		req.on("end", () => {
			seen.push({ method: req.method, url: req.url, host: req.headers.host, token: req.headers[DAEMON_TOKEN_HEADER.toLowerCase()] as string | undefined, body });
			res.writeHead(200, { "content-type": "application/json" });
			res.end(JSON.stringify({ ok: true }));
		});
	});
	await new Promise<void>((resolve) => server.listen(socket, resolve));
	cleanups.push(() => {
		server.close();
		rmSync(dir, { recursive: true, force: true });
	});
	return { socket, seen };
}

describe.skipIf(process.platform === "win32")("daemon socket fetch (B170)", () => {
	it("sends main's daemon calls with the token over the socket, not TCP", async () => {
		const { socket, seen } = await socketServer();
		const tcp = vi.fn<typeof fetch>(async () => new Response("tcp"));
		const wrapped = withDaemonToken(tcp, { port: () => 3101, token: () => "secret-token", socketPath: () => socket });

		const res = await wrapped("http://127.0.0.1:3101/shutdown?x=1", { method: "POST", body: '{"stopAgents":true}', headers: { "content-type": "application/json" } });
		expect(await res.json()).toEqual({ ok: true });
		expect(tcp).not.toHaveBeenCalled();
		expect(seen[0]).toMatchObject({ method: "POST", url: "/shutdown?x=1", host: "127.0.0.1:3101", token: "secret-token", body: '{"stopAgents":true}' });

		// Non-daemon URLs never go to the socket.
		await wrapped("https://api.example.com/x");
		expect(tcp).toHaveBeenCalledTimes(1);
		expect(seen).toHaveLength(1);
	});

	it("falls back to TCP when the daemon serves no socket", async () => {
		const tcp = vi.fn<typeof fetch>(async () => new Response("tcp"));
		const missing = path.join(tmpdir(), "no-such-dir", "daemon.sock");
		expect(await (await daemonFetch(missing, tcp, "http://127.0.0.1:3101/healthz")).text()).toBe("tcp");
		// A stale socket file nobody listens on also falls back.
		const { socket } = await socketServer();
		cleanups.pop()?.();
		const stale = vi.fn<typeof fetch>(async () => new Response("tcp"));
		await daemonFetch(socket, stale, "http://127.0.0.1:3101/healthz", undefined, () => true);
		expect(stale).toHaveBeenCalledTimes(1);
	});
});
