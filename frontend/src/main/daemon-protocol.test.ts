// @vitest-environment node
import { mkdirSync, mkdtempSync, rmSync } from "node:fs";
import { createServer, type Server } from "node:http";
import { tmpdir } from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { DAEMON_TOKEN_HEADER } from "./daemon-auth";
import { handleDaemonRequest, isDaemonOriginUrl, type DaemonProtocolOptions } from "./daemon-protocol";
import { daemonSocketPath } from "./daemon-socket-fetch";

type Seen = { method?: string; url?: string; host?: string; token?: string; body: string; acceptEncoding?: string };

const cleanups: Array<() => void> = [];
afterEach(() => {
	while (cleanups.length) cleanups.pop()?.();
});

// A stand-in daemon on a real Unix socket. /sse writes one event, then waits
// for /release before the second, so a buffered response would hang the test.
async function socketDaemon(): Promise<{ socket: string; seen: Seen[] }> {
	const dir = mkdtempSync(path.join(tmpdir(), "dp"));
	const socket = daemonSocketPath(path.join(dir, "running.json"));
	mkdirSync(path.dirname(socket), { mode: 0o700 });
	const seen: Seen[] = [];
	let release: (() => void) | undefined;
	const server: Server = createServer((req, res) => {
		let body = "";
		req.on("data", (c) => (body += c));
		req.on("end", () => {
			seen.push({ method: req.method, url: req.url, host: req.headers.host, token: req.headers[DAEMON_TOKEN_HEADER.toLowerCase()] as string | undefined, body, acceptEncoding: req.headers["accept-encoding"] });
			if (req.url === "/sse") {
				res.writeHead(200, { "content-type": "text/event-stream" });
				res.write("data: one\n\n");
				release = () => res.end("data: two\n\n");
				return;
			}
			res.writeHead(200, { "content-type": "text/plain" });
			res.end(`served ${req.url}`);
		});
	});
	await new Promise<void>((resolve) => server.listen(socket, resolve));
	cleanups.push(() => {
		release?.();
		server.closeAllConnections();
		server.close();
		rmSync(dir, { recursive: true, force: true });
	});
	return { socket, seen };
}

function options(socket: string | null, extra: Partial<DaemonProtocolOptions> = {}): DaemonProtocolOptions & { passthrough: ReturnType<typeof vi.fn> } {
	const passthrough = vi.fn(async (request: Request) => new Response(`network ${request.url}`));
	return {
		port: () => 3101,
		socketPath: () => socket,
		token: () => "secret-token",
		trustedOrigins: () => new Set(["app://renderer"]),
		passthrough,
		...extra,
	} as DaemonProtocolOptions & { passthrough: ReturnType<typeof vi.fn> };
}

describe("isDaemonOriginUrl", () => {
	it("matches the API host and preview hosts on the daemon port only", () => {
		expect(isDaemonOriginUrl(new URL("http://127.0.0.1:3101/api/v1/projects"), 3101)).toBe(true);
		expect(isDaemonOriginUrl(new URL("http://localhost:3101/"), 3101)).toBe(true);
		expect(isDaemonOriginUrl(new URL("http://ao-preview-abc.s1.localhost:3101/index.html"), 3101)).toBe(true);
		expect(isDaemonOriginUrl(new URL("http://127.0.0.1:5173/"), 3101)).toBe(false);
		expect(isDaemonOriginUrl(new URL("http://example.com:3101/"), 3101)).toBe(false);
		expect(isDaemonOriginUrl(new URL("https://127.0.0.1:3101/"), 3101)).toBe(false);
		expect(isDaemonOriginUrl(new URL("http://127.0.0.1:3101/"), undefined)).toBe(false);
	});
});

describe.skipIf(process.platform === "win32")("daemon protocol over the socket (B176)", () => {
	it("serves the shell's API request over the socket with the token, never the network", async () => {
		const { socket, seen } = await socketDaemon();
		const opts = options(socket);
		const res = await handleDaemonRequest(new Request("http://127.0.0.1:3101/api/v1/projects?x=1"), opts);
		expect(await res.text()).toBe("served /api/v1/projects?x=1");
		expect(seen).toEqual([expect.objectContaining({ method: "GET", url: "/api/v1/projects?x=1", host: "127.0.0.1:3101", token: "secret-token" })]);
		expect(opts.passthrough).not.toHaveBeenCalled();
	});

	it("forwards a POST body and keeps the renderer origin's token", async () => {
		const { socket, seen } = await socketDaemon();
		const res = await handleDaemonRequest(
			new Request("http://127.0.0.1:3101/api/v1/x", { method: "POST", body: '{"a":1}', headers: { origin: "app://renderer", "content-type": "application/json" } }),
			options(socket),
		);
		expect(res.status).toBe(200);
		expect(seen[0]).toMatchObject({ method: "POST", body: '{"a":1}', token: "secret-token" });
	});

	it("never gives the token to another origin, an agent page's null origin, or a tokenless session", async () => {
		const { socket, seen } = await socketDaemon();
		await handleDaemonRequest(new Request("http://127.0.0.1:3101/a", { headers: { origin: "null" } }), options(socket));
		await handleDaemonRequest(new Request("http://127.0.0.1:3101/b", { headers: { origin: "http://evil.localhost:3101" } }), options(socket));
		await handleDaemonRequest(new Request("http://127.0.0.1:3101/c"), options(socket, { token: undefined }));
		// A page cannot smuggle its own token header through either.
		await handleDaemonRequest(new Request("http://127.0.0.1:3101/d", { headers: { origin: "null", [DAEMON_TOKEN_HEADER]: "forged" } }), options(socket));
		expect(seen.map((s) => s.token)).toEqual([undefined, undefined, undefined, undefined]);
	});

	it("serves preview hosts over the socket without the token, keeping their Host", async () => {
		const { socket, seen } = await socketDaemon();
		const res = await handleDaemonRequest(new Request("http://ao-preview-abc.s1.localhost:3101/index.html"), options(socket));
		expect(await res.text()).toBe("served /index.html");
		expect(seen[0]).toMatchObject({ host: "ao-preview-abc.s1.localhost:3101", token: undefined });
	});

	it("asks the daemon for an uncompressed body, which Chromium would not decode", async () => {
		const { socket, seen } = await socketDaemon();
		await handleDaemonRequest(new Request("http://ao-preview-abc.s1.localhost:3101/app.js", { headers: { "accept-encoding": "gzip, deflate, br" } }), options(socket));
		expect(seen[0]!.acceptEncoding).toBeUndefined();
	});

	it("streams the response (SSE) instead of buffering it", async () => {
		const { socket } = await socketDaemon();
		const res = await handleDaemonRequest(new Request("http://127.0.0.1:3101/sse"), options(socket));
		const reader = res.body!.getReader();
		const first = await reader.read();
		expect(new TextDecoder().decode(first.value)).toBe("data: one\n\n");
		await reader.cancel();
	});

	it("passes other http URLs to the network untouched", async () => {
		const { socket, seen } = await socketDaemon();
		const opts = options(socket);
		const res = await handleDaemonRequest(new Request("http://127.0.0.1:5173/"), opts);
		expect(await res.text()).toBe("network http://127.0.0.1:5173/");
		expect(seen).toEqual([]);
	});

	it("falls back to the network (loopback TCP) with the token when the daemon has no socket", async () => {
		const opts = options(null);
		await handleDaemonRequest(new Request("http://127.0.0.1:3101/api/v1/projects"), opts);
		const sent = opts.passthrough.mock.calls[0]![0] as Request;
		expect(sent.url).toBe("http://127.0.0.1:3101/api/v1/projects");
		expect(sent.headers.get(DAEMON_TOKEN_HEADER)).toBe("secret-token");
	});

	it("answers 502 instead of trying TCP when the socket refuses", async () => {
		const dir = mkdtempSync(path.join(tmpdir(), "dp"));
		cleanups.push(() => rmSync(dir, { recursive: true, force: true }));
		const opts = options(path.join(dir, "gone.sock"), { exists: () => true });
		const res = await handleDaemonRequest(new Request("http://127.0.0.1:3101/healthz"), opts);
		expect(res.status).toBe(502);
		expect(opts.passthrough).not.toHaveBeenCalled();
	});
});
