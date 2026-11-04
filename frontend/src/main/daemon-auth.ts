// Loopback daemon authentication for the Electron main process.
//
// The daemon requires its per-install token (X-VO-Token) on every loopback
// request (backend/internal/httpd/daemon_auth.go, docs/adr/0007). The token
// lives in daemon.token next to running.json, readable only by this user.
// Main reads it and attaches it in two places, so the renderer never holds
// it and renderer XSS cannot exfiltrate it:
//   - session.webRequest.onBeforeSendHeaders for the shell's own REST, SSE,
//     image, and /mux WebSocket requests, and main's own net.fetch calls;
//   - a wrapper around Node's global fetch for main-process callers.
// Agent pages framed in the chat (renders, HTML artifacts) never get the
// token, except for requests that stay on their own files.

import { readFileSync } from "node:fs";
import path from "node:path";
import { isAgentPageUrl, isOwnPageRequest } from "../shared/agent-page-url";
import { daemonFetch } from "./daemon-socket-fetch";

export const DAEMON_TOKEN_HEADER = "X-VO-Token";
export const DAEMON_TOKEN_FILE = "daemon.token";

const LOOPBACK_NAMES = new Set(["127.0.0.1", "localhost", "[::1]"]);

/** The token file for the daemon whose handshake file is `runFile`. */
export function daemonTokenPath(runFile: string): string {
	return path.join(path.dirname(runFile), DAEMON_TOKEN_FILE);
}

/**
 * Reads the token lazily and caches it once found. The daemon creates the
 * file on its first start, so a miss is retried on the next use; refresh()
 * re-reads after a daemon (re)start in case the daemon rotated it.
 */
export class DaemonTokenSource {
	private cached: string | null = null;

	constructor(
		private readonly tokenPath: () => string | null,
		private readonly readFile: (file: string) => string = (file) => readFileSync(file, "utf8"),
	) {}

	token(): string | null {
		if (this.cached === null) this.cached = this.read();
		return this.cached;
	}

	refresh(): void {
		this.cached = this.read();
	}

	private read(): string | null {
		const file = this.tokenPath();
		if (!file) return null;
		try {
			return this.readFile(file).trim() || null;
		} catch {
			return null;
		}
	}
}

/** Whether `url` addresses the local daemon's loopback listener on `port`. */
export function isDaemonUrl(url: string | URL, port: number | undefined): boolean {
	if (!port) return false;
	let parsed: URL;
	try {
		parsed = url instanceof URL ? url : new URL(url);
	} catch {
		return false;
	}
	if (parsed.protocol !== "http:" && parsed.protocol !== "ws:") return false;
	return LOOPBACK_NAMES.has(parsed.hostname) && parsed.port === String(port);
}

export type DaemonRequest = {
	url: string;
	resourceType?: string;
	/** URLs of the requesting frame and its ancestors, nearest first. */
	frameUrls: () => string[];
};

/**
 * Whether a shell request to the daemon may carry the token. The app's own
 * requests do, including loading an agent page into a frame (the daemon
 * serves render documents without it anyway). A request made by an agent page
 * only does when it stays on that page's own files, mirroring the shell's
 * agent-page request guard in main.ts.
 */
export function shellRequestGetsToken(request: DaemonRequest): boolean {
	if (request.resourceType === "subFrame" && isAgentPageUrl(request.url)) return true;
	let page: string | undefined;
	try {
		page = request.frameUrls().find((url) => isAgentPageUrl(url));
	} catch {
		return false;
	}
	if (!page) return true;
	try {
		return isOwnPageRequest(new URL(page), new URL(request.url));
	} catch {
		return false;
	}
}

type FrameLike = { url: string; parent: FrameLike | null } | null | undefined;

/** Frame URLs from `frame` up to the top, nearest first. */
export function frameChainUrls(frame: FrameLike): string[] {
	const urls: string[] = [];
	for (let current = frame; current; current = current.parent) urls.push(current.url);
	return urls;
}

type HeaderDetails = {
	url: string;
	resourceType: string;
	webContentsId?: number;
	frame?: FrameLike;
	requestHeaders: Record<string, string>;
};

type HeaderSession = {
	webRequest: {
		onBeforeSendHeaders(
			listener: (details: HeaderDetails, callback: (response: { requestHeaders: Record<string, string> }) => void) => void,
		): void;
	};
};

export type DaemonAuthOptions = {
	port: () => number | undefined;
	token: () => string | null;
	/** The shell's webContents ids; other windows on the session never get the token. */
	isTrustedWebContents: (id: number) => boolean;
};

/**
 * Attaches the token to daemon-bound requests from trusted shell webContents
 * and from main itself (net.fetch has no webContents). Electron allows one
 * onBeforeSendHeaders listener per session, so call this once per session.
 */
export function installDaemonAuthHeaders(session: HeaderSession, options: DaemonAuthOptions): void {
	session.webRequest.onBeforeSendHeaders((details, callback) => {
		const requestHeaders = details.requestHeaders;
		if (daemonRequestGetsToken(details, options)) {
			const token = options.token();
			if (token) requestHeaders[DAEMON_TOKEN_HEADER] = token;
		}
		callback({ requestHeaders });
	});
}

export function daemonRequestGetsToken(details: Omit<HeaderDetails, "requestHeaders">, options: DaemonAuthOptions): boolean {
	if (!isDaemonUrl(details.url, options.port())) return false;
	if (details.webContentsId === undefined) return true;
	if (!options.isTrustedWebContents(details.webContentsId)) return false;
	return shellRequestGetsToken({
		url: details.url,
		resourceType: details.resourceType,
		frameUrls: () => frameChainUrls(details.frame),
	});
}

/**
 * Wraps a fetch so main-process requests to the daemon carry the token. An
 * explicit X-VO-Token from the caller wins; other URLs pass through untouched.
 */
export function withDaemonToken(
	fetchImpl: typeof fetch,
	options: Pick<DaemonAuthOptions, "port" | "token"> & { socketPath?: () => string | null },
): typeof fetch {
	return (input, init) => {
		const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
		if (!isDaemonUrl(url, options.port())) return fetchImpl(input, init);
		// Daemon-bound: over the user-only socket when the daemon serves one (B170).
		const send = (sendInit?: RequestInit) => daemonFetch(options.socketPath?.() ?? null, fetchImpl, input, sendInit);
		const token = options.token();
		if (!token) return send(init);
		const headers = new Headers(init?.headers ?? (input instanceof Request ? input.headers : undefined));
		if (!headers.has(DAEMON_TOKEN_HEADER)) headers.set(DAEMON_TOKEN_HEADER, token);
		return send({ ...init, headers });
	};
}
