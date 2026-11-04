// Serves daemon URLs through main over the user-only socket (B176).
//
// The daemon no longer listens on loopback TCP by default. Pages keep using
// the same URLs (http://127.0.0.1:<port>/... for the API, renders and
// artifacts; http://<label>.<id>.localhost:<port>/... for previews), and this
// handler, installed for "http" on every session, sends them to
// <dir(running.json)>/sock/daemon.sock with node:http. Other http URLs pass
// through to the network unchanged. HTTPS is not intercepted at all.
//
// WebSockets never reach protocol handlers; the shell's /mux and event-stream
// sockets use the IPC bridge in daemon-ws-bridge.ts instead.
//
// Token: requests intercepted here skip webRequest.onBeforeSendHeaders, so the
// handler attaches X-VO-Token itself, only on sessions given a token source
// (the shell's) and only for the API host. onBeforeRequest still runs first,
// so the shell's agent-page guard (main.ts) has already cancelled any agent
// page request that leaves the page's own files. Cross-origin fetches from a
// page carry Origin: the token goes only with the app's own origins, never
// with an agent page's opaque "null" or any other origin.

import { existsSync } from "node:fs";
import { socketFetch } from "./daemon-socket-fetch";
import { DAEMON_TOKEN_HEADER, isDaemonUrl } from "./daemon-auth";

export type DaemonProtocolOptions = {
	/** The attached daemon's origin port; undefined while none is ready. */
	port: () => number | undefined;
	/** The daemon socket, or null when there is none (Windows, older daemon). */
	socketPath: () => string | null;
	/** Token source for the shell's session; omitted on every other session. */
	token?: () => string | null;
	/** Origins whose requests may carry the token (the shell renderer's). */
	trustedOrigins?: () => ReadonlySet<string>;
	/** The network path for everything else, bypassing this handler. */
	passthrough: (request: Request) => Promise<Response>;
	exists?: (file: string) => boolean;
};

/** Whether `url` is served by the daemon: its API host or a preview host, on its port. */
export function isDaemonOriginUrl(url: URL, port: number | undefined): boolean {
	if (!port || url.protocol !== "http:" || url.port !== String(port)) return false;
	const host = url.hostname.toLowerCase();
	return host === "127.0.0.1" || host === "localhost" || host === "[::1]" || host.endsWith(".localhost");
}

/** Whether a daemon-bound request from this session may carry the token. */
export function requestGetsToken(request: Request, port: number | undefined, trustedOrigins: ReadonlySet<string>): boolean {
	if (!isDaemonUrl(request.url, port)) return false;
	const origin = request.headers.get("origin");
	return origin === null || trustedOrigins.has(origin);
}

export async function handleDaemonRequest(request: Request, options: DaemonProtocolOptions): Promise<Response> {
	let url: URL;
	try {
		url = new URL(request.url);
	} catch {
		return options.passthrough(request);
	}
	const port = options.port();
	if (!isDaemonOriginUrl(url, port)) return options.passthrough(request);

	const headers = new Headers(request.headers);
	headers.delete(DAEMON_TOKEN_HEADER);
	// A body this handler returns reaches the page as is: Chromium does not
	// decode a Content-Encoding on it. Compression buys nothing on a local
	// socket, so ask the daemon for none.
	headers.delete("accept-encoding");
	const token = options.token?.();
	if (token && requestGetsToken(request, port, options.trustedOrigins?.() ?? new Set())) {
		headers.set(DAEMON_TOKEN_HEADER, token);
	}
	const hasBody = request.method !== "GET" && request.method !== "HEAD";
	const init: RequestInit & { duplex?: "half" } = {
		method: request.method,
		headers,
		body: hasBody ? await request.arrayBuffer() : undefined,
		signal: request.signal,
		redirect: "manual",
	};

	const socket = options.socketPath();
	const exists = options.exists ?? existsSync;
	if (socket && process.platform !== "win32" && exists(socket)) {
		try {
			return await socketFetch(socket, request.url, init);
		} catch (error) {
			return new Response(`daemon unavailable: ${error instanceof Error ? error.message : String(error)}`, {
				status: 502,
				headers: { "content-type": "text/plain; charset=utf-8" },
			});
		}
	}
	// No socket: a daemon still on loopback TCP (Windows, an older daemon, or
	// TALLPINE_LOOPBACK_TCP without a socket).
	return options.passthrough(new Request(request.url, init));
}

type ProtocolSession = {
	protocol: { handle(scheme: string, handler: (request: Request) => Promise<Response> | Response): void };
	fetch(input: Request, init?: RequestInit & { bypassCustomProtocolHandlers?: boolean }): Promise<Response>;
};

const installed = new WeakSet<object>();

/**
 * Routes this session's daemon URLs over the socket. Idempotent per session;
 * the first install decides whether the session gets a token.
 */
export function installDaemonProtocol(session: ProtocolSession, options: Omit<DaemonProtocolOptions, "passthrough">): void {
	if (installed.has(session)) return;
	installed.add(session);
	// A refused or unreachable address is a network error for the page, as
	// without this handler; rejecting would also log it on every failure.
	const passthrough = (request: Request) =>
		session.fetch(request, { bypassCustomProtocolHandlers: true }).catch(() => Response.error());
	session.protocol.handle("http", (request) => handleDaemonRequest(request, { ...options, passthrough }));
}
