/* Split out of remote-proxy.ts (B297, P53). */

import { timingSafeEqual } from "node:crypto";
import {
type IncomingHttpHeaders,
type OutgoingHttpHeaders
} from "node:http";
import type { Readable,Writable } from "node:stream";
import { HOP_BY_HOP_HEADERS } from "../agent-viewer";

// Loopback proxy fronting one remote Tallpine daemon. It exists because the renderer
// cannot authenticate to a remote daemon itself: EventSource and WebSocket
// cannot set an Authorization header, and app://renderer has no CORS standing
// there. The proxy holds the credential in main-process memory, injects it on
// every forwarded request, and answers CORS for the renderer origin locally.
//
// Loopback is ambient authority everywhere in Tallpine, but this socket fronts a
// DIFFERENT machine — so every request must carry a 128-bit token in the URL
// path (the one place EventSource and WebSocket can both put it). The token is
// stripped before forwarding: the remote daemon and its logs never see it.
export type ActiveProxy = {
	base: string;
	listeningAddress?: string;
	previewUrl: (sessionId: string, sourceUrl: string) => string;
	resolvePreviewUrl: (sessionId: string, viewedUrl: string) => string;
	/** The host's agent browser for a session's agent tab; "" when the host has none. */
	agentBrowserUrl: (sessionId: string) => Promise<string>;
	/** Whether a URL is one of this host's agent-browser viewers (see agentBrowserUrl). */
	isAgentBrowserUrl: (url: string) => boolean;
	/** A preview's address for a browser outside Tallpine; any other URL unchanged. */
	externalUrl: (url: string) => string;
	close: () => Promise<void>;
};

// "agent" is the host's agent browser (the Chromium the session's agent drives),
// streamed through the daemon's agent-browser route.
export type PreviewTarget = { sessionId: string; kind: "static" | "app" | "artifact" | "agent"; entry: string };

// What both loopback servers hand the shared handler: HTTP/1.1 requests from
// the plain server, and HTTP/1.1 or HTTP/2 ones from the TLS server.
export type ProxyRequest = Readable & { headers: IncomingHttpHeaders; url?: string; method?: string };

export type ProxyResponse = Writable & {
	headersSent: boolean;
	writableFinished: boolean;
	writeHead(status: number, headers?: OutgoingHttpHeaders): unknown;
	flushHeaders?: () => void;
};

// The HTTP/2 session could not carry this host's requests (no HTTP/2 on the
// host or an intermediary, or the session failed); HTTP/1.1 takes over.
export class UpstreamHttp2Unavailable extends Error {}

// Cloudflare quick tunnels answer 429 past 200 requests in flight.
export const MAX_PENDING_UPSTREAM_STREAMS = 100;

// Preview requests stop this far short of it: a dev server that leaves its
// requests hanging (the daemon sets them no deadline) must not take every
// slot the renderer's own requests need.
export const RENDERER_STREAM_HEADROOM = 20;

export const HTTP2_RETRY_MS = 60_000;

export const UPSTREAM_STREAM_WINDOW = 4 << 20;

export const UPSTREAM_SESSION_WINDOW = 32 << 20;

// How long opening an upstream session may take (connect, TLS, the tunnel's
// handshake, the identity probe), and how often a live session is pinged.
export const UPSTREAM_TIMING = { openMs: 5_000, pingMs: 10_000 };

export const RENDERER_ORIGIN = "app://renderer";

// Hop-by-hop or wrong-machine headers that must not transit the proxy.
export const STRIP_REQUEST_HEADERS = [
	"host",
	"origin",
	"proxy-authorization",
	"x-ao-preview-app-authorization",
	"x-ao-preview-app-origin",
	"x-ao-expected-host-id",
	"x-vo-client",
	...HOP_BY_HOP_HEADERS,
];

// Nothing in this file may log a secret. Not the connection password, not the
// proxy token, and not `req.url` (its first segment IS the token) — only the
// post-strip path, and the upstream address, which is a machine on the user's
// own network named in their own console. That is the whole point: "the app
// can't reach my host" had no answer anywhere before this.
export function log(message: string): void {
	console.log(`[remote-proxy] ${message}`);
}

export function warn(message: string): void {
	console.warn(`[remote-proxy] ${message}`);
}

export function safeLogPath(path: string, previewKind?: PreviewTarget["kind"]): string {
	return previewKind ? `preview/${previewKind}` : path.split("?", 1)[0];
}

export function equalsToken(candidate: string, token: string): boolean {
	// Constant-time: the token is the only thing standing between a local
	// process and another machine's daemon, so don't leak it a byte at a time.
	const a = Buffer.from(candidate);
	const b = Buffer.from(token);
	return a.length === b.length && timingSafeEqual(a, b);
}
