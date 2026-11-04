// Main-process daemon calls over the user-only Unix socket (B170).
//
// The daemon serves its full handler chain on <dir(running.json)>/sock/daemon.sock
// (dir 0700, socket 0600, peer uid checked on accept; see
// backend/internal/daemonsock). Main's own fetches to the daemon go there
// instead of loopback TCP. Node's http over socketPath never consults
// HTTP(S)_PROXY, so the daemon token cannot leak to a proxy. When the socket
// is absent or refuses the connection (an older daemon, Windows), the call
// falls back to the original fetch over loopback TCP.

import { existsSync } from "node:fs";
import { request as httpRequest, type IncomingMessage } from "node:http";
import path from "node:path";
import { Readable } from "node:stream";

export const DAEMON_SOCKET_DIR = "sock";
export const DAEMON_SOCKET_FILE = "daemon.sock";

/** The socket for the daemon whose handshake file is `runFile`. */
export function daemonSocketPath(runFile: string): string {
	return path.join(path.dirname(runFile), DAEMON_SOCKET_DIR, DAEMON_SOCKET_FILE);
}

/** Errors meaning "no daemon on this socket": safe to retry over TCP, nothing was sent. */
const FALLBACK_CODES = new Set(["ENOENT", "ECONNREFUSED", "ENOTSOCK"]);

class SocketUnavailable extends Error {}

const NULL_BODY_STATUS = new Set([101, 204, 205, 304]);

/** fetch() for one request over a Unix socket. */
export async function socketFetch(socketPath: string, input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
	const req = new Request(input, init);
	const url = new URL(req.url);
	const body = req.method === "GET" || req.method === "HEAD" ? null : Buffer.from(await req.arrayBuffer());
	const headers: Record<string, string> = {};
	req.headers.forEach((value, name) => {
		headers[name] = value;
	});
	if (body) headers["content-length"] = String(body.length);
	return new Promise<Response>((resolve, reject) => {
		let connected = false;
		const out = httpRequest(
			{ socketPath, method: req.method, path: url.pathname + url.search, headers: { ...headers, host: url.host }, agent: false },
			(res: IncomingMessage) => {
				const resHeaders = new Headers();
				for (const [name, value] of Object.entries(res.headers)) {
					if (Array.isArray(value)) for (const v of value) resHeaders.append(name, v);
					else if (value !== undefined) resHeaders.set(name, value);
				}
				const status = res.statusCode ?? 502;
				const stream = NULL_BODY_STATUS.has(status) || req.method === "HEAD" ? null : (Readable.toWeb(res) as ReadableStream<Uint8Array>);
				if (!stream) res.resume();
				resolve(new Response(stream, { status, statusText: res.statusMessage, headers: resHeaders }));
			},
		);
		out.on("socket", (sock) => sock.once("connect", () => (connected = true)));
		out.on("error", (err: NodeJS.ErrnoException) => {
			reject(!connected && err.code && FALLBACK_CODES.has(err.code) ? new SocketUnavailable(err.message) : err);
		});
		const signal = req.signal;
		if (signal.aborted) out.destroy(signal.reason);
		else signal.addEventListener("abort", () => out.destroy(signal.reason), { once: true });
		out.end(body ?? undefined);
	});
}

/**
 * Note: the TCP fallback re-sends `input`/`init`. socketFetch has already read
 * the body by then, so a ReadableStream body or a Request input with a body
 * cannot be retried; main's daemon callers send string/JSON bodies only.
 *
 * Sends a daemon-bound request over the socket when it exists, else (or when
 * nothing listens there) through `tcpFetch`.
 */
export async function daemonFetch(
	socketPath: string | null,
	tcpFetch: typeof fetch,
	input: RequestInfo | URL,
	init?: RequestInit,
	exists: (file: string) => boolean = existsSync,
): Promise<Response> {
	if (socketPath && process.platform !== "win32" && exists(socketPath)) {
		try {
			return await socketFetch(socketPath, input, init);
		} catch (err) {
			if (!(err instanceof SocketUnavailable)) throw err;
		}
	}
	return tcpFetch(input, init);
}
