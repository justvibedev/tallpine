/* Split out of remote-proxy/proxy.ts (B297, P53). */
import {
connect as http2Connect,
type ClientHttp2Session
} from "node:http2";
import { isIP } from "node:net";
import type { Duplex } from "node:stream";
import { WebSocket, createWebSocketStream } from "ws";
import { readRemoteIdentity } from "../remote-request";
import { pinnedTlsOptions } from "../remote-tls";
import { HTTP2_RETRY_MS, UPSTREAM_SESSION_WINDOW, UPSTREAM_STREAM_WINDOW, UpstreamHttp2Unavailable } from "./common";

import { createIdentityVerifier, createStreamBudget, getOverSession, keepAlive } from "./upstream-parts";
import type { RemoteEntry } from "../remotes-store";
import type { UPSTREAM_TIMING } from "./common";

/** What the multiplexed upstream connection needs from the proxy instance. */
export interface UpstreamSessionDeps {
	entry: RemoteEntry;
	timing: typeof UPSTREAM_TIMING;
	secure: boolean;
	upstream: URL;
	prefix: string;
	hostname: string;
	pinned: ReturnType<typeof pinnedTlsOptions> | Record<string, never>;
}

/**
 * The proxy's connection to the remote host: one verified HTTP/2 session (or an
 * HTTP/1.1 fallback), its stream budget and the host identity proof.
 */
export function createUpstreamSession({ entry, timing, secure, upstream, prefix, hostname, pinned }: UpstreamSessionDeps) {

		// Requests go to the host over one multiplexed HTTP/2 session when it can
		// carry them. A preview's hundreds of module requests then share one
		// connection instead of each paying a round trip for its own identity probe.
		// The session proves the host's identity on itself before it carries the
		// credential, so the proof covers every request it carries. A host or
		// intermediary without HTTP/2 gets HTTP/1.1 for a minute, then a retry.
		let upstreamSession: Promise<ClientHttp2Session> | null = null;
		let http2RetryAt = 0;
		// Over HTTPS the session rides inside one WebSocket to the host's tunnel
		// (backend lan_tunnel.go): a Cloudflare quick tunnel answers 429 past 200
		// plain requests in flight and holds back small streamed writes, but
		// carries a WebSocket unchanged. A host without the tunnel gets plain
		// HTTP/2. On a LAN address plain HTTP/2 is already direct.
		let tunnelRetryAt = secure ? 0 : Number.POSITIVE_INFINITY;
		const openTunnel = async (): Promise<Duplex> => {
			// The upgrade itself carries the credential: prove the host first, as
			// HTTP/1.1 requests do.
			await verifyUpstream();
			const socket = new WebSocket(`wss://${upstream.host}${prefix}/api/v1/tunnel`, {
				headers: { authorization: `Bearer ${entry.password}`, ...(entry.hostId ? { "x-ao-expected-host-id": entry.hostId } : {}) },
				perMessageDeflate: false,
				// An upgrade nobody answers would hold every request waiting on this session.
				handshakeTimeout: timing.openMs,
				...(isIP(hostname) ? {} : { servername: hostname }),
			});
			// Wrap before "open": the host's first HTTP/2 frame can arrive with it.
			const stream = createWebSocketStream(socket);
			// The stream re-emits socket errors. Before "open" they are the upgrade's,
			// reported below; after, the session sees the close and is replaced.
			// Unheard, either would crash the main process.
			stream.on("error", () => undefined);
			try {
				await new Promise<void>((resolve, reject) => {
					socket.once("open", () => resolve());
					socket.once("error", (error: Error) => reject(new UpstreamHttp2Unavailable(`tunnel: ${error.message}`)));
				});
			} catch (error) {
				stream.destroy();
				throw error;
			}
			return stream;
		};
		// The tunnel ends at the daemon's own handler, past any reverse proxy, so a
		// path sent inside it drops the prefix that proxy serves the daemon at
		// (every path starts with it). Over any other connection the prefix stays.
		const tunneled = new WeakSet<ClientHttp2Session>();
		const verifyUpstream = createIdentityVerifier(entry);
		const { acquireStream, releaseStream } = createStreamBudget();
		const sessionPath = (session: ClientHttp2Session, path: string): string =>
			tunneled.has(session) ? path.slice(prefix.length) : path;
		const openUpstreamSession = (tunnel?: Duplex): Promise<ClientHttp2Session> => {
			// Node's default 64 KiB receive window would cap a preview at 64 KiB per
			// round trip; a few hundred modules need megabytes in flight.
			const session = http2Connect(`${upstream.protocol}//${upstream.host}`, {
				settings: { initialWindowSize: UPSTREAM_STREAM_WINDOW },
				...(tunnel ? { createConnection: () => tunnel } : {}),
				...(secure && !isIP(hostname) ? { servername: hostname } : {}),
				...pinned,
			});
			if (tunnel) tunneled.add(session);
			let deadline: ReturnType<typeof setTimeout> | undefined;
			const opened = Promise.race([
				new Promise<ClientHttp2Session>((resolve, reject) => {
					session.once("connect", () => {
						session.setLocalWindowSize(UPSTREAM_SESSION_WINDOW);
						keepAlive(session, timing.pingMs);
						resolve(session);
					});
					session.on("error", (error: Error) => reject(new UpstreamHttp2Unavailable(error.message)));
					session.once("close", () => reject(new UpstreamHttp2Unavailable("session closed")));
				}).then(async (connected) => {
					// Always one unauthenticated probe: it also proves the session works.
					const identityPath = `${upstream.origin}${prefix}/api/v1/identity`;
					if (!entry.hostId) {
						await getOverSession(sessionPath, upstream.host, connected, identityPath);
						return connected;
					}
					const actual = await readRemoteIdentity({ url: `${upstream.origin}${prefix}` }, (url) => getOverSession(sessionPath, upstream.host, connected, String(url)));
					if (actual !== entry.hostId) throw new Error("remote host identity changed");
					return connected;
				}),
				// Every request waits on this session: an address that never answers
				// (a peer gone offline drops packets) would hold them all for the OS
				// connect timeout.
				new Promise<never>((_, reject) => {
					deadline = setTimeout(() => reject(new UpstreamHttp2Unavailable("session open timed out")), timing.openMs);
					deadline.unref();
				}),
			]).finally(() => clearTimeout(deadline));
			opened.catch(() => session.destroy());
			return opened;
		};
		// The verified session for the next request, or null to use HTTP/1.1.
		const openPreferredSession = async (): Promise<ClientHttp2Session> => {
			if (Date.now() >= tunnelRetryAt) {
				try {
					return await openUpstreamSession(await openTunnel());
				} catch (error) {
					if (!(error instanceof UpstreamHttp2Unavailable)) throw error;
					tunnelRetryAt = Date.now() + HTTP2_RETRY_MS;
				}
			}
			return openUpstreamSession();
		};
		const sessionForRequest = async (): Promise<ClientHttp2Session | null> => {
			// A session that closed or received GOAWAY since it was cached is
			// replaced once; a second failure in a row falls back to HTTP/1.1.
			for (let attempt = 0; attempt < 2 && Date.now() >= http2RetryAt; attempt++) {
				const pending = (upstreamSession ??= openPreferredSession());
				try {
					const session = await pending;
					if (!session.closed && !session.destroyed) return session;
				} catch (error) {
					if (upstreamSession === pending) upstreamSession = null;
					if (!(error instanceof UpstreamHttp2Unavailable)) throw error;
					http2RetryAt = Date.now() + HTTP2_RETRY_MS;
					return null;
				}
				if (upstreamSession === pending) upstreamSession = null;
			}
			return null;
		};
		// Bounds requests waiting for their response headers, not open streams, so
		// long-lived event streams never starve new requests. A preview request
		// waits once RENDERER_STREAM_HEADROOM slots are all that is left.

	return { releaseStream, sessionPath, acquireStream, sessionForRequest, verifyUpstream, getUpstreamSession: () => upstreamSession };
}
