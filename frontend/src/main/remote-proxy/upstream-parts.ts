/* Pieces of the upstream session: identity proof, stream budget, GET over a session (B303). */
import { constants as http2Constants, type ClientHttp2Session } from "node:http2";
import { readRemoteIdentity } from "../remote-request";
import { MAX_PENDING_UPSTREAM_STREAMS, ProxyResponse, RENDERER_STREAM_HEADROOM, UpstreamHttp2Unavailable } from "./common";
import type { RemoteEntry } from "../remotes-store";

export function createIdentityVerifier(entry: RemoteEntry) {
	// Addresses can be reassigned while the desktop stays open. Check before
	// HTTP/1.1 requests and WebSockets carry the credential, not only when the
	// proxy is first created. Requests that arrive together share one probe.
	let identityProbe: Promise<void> | null = null;
	const verifyUpstream = (): Promise<void> => {
		if (!entry.hostId) return Promise.resolve();
		identityProbe ??= readRemoteIdentity(entry)
			.then((actual) => {
				if (actual !== entry.hostId) throw new Error("remote host identity changed");
			})
			.finally(() => {
				identityProbe = null;
			});
		return identityProbe;
	};
	return verifyUpstream;
}

export function createStreamBudget() {
	let pendingStreams = 0;
	const streamWaiters = new Set<{ preview: boolean; start: () => void }>();
	const streamLimit = (preview: boolean): number => MAX_PENDING_UPSTREAM_STREAMS - (preview ? RENDERER_STREAM_HEADROOM : 0);
	// Resolves false, holding no slot, when the client leaves while it waits.
	const acquireStream = (preview: boolean, res: ProxyResponse): Promise<boolean> => {
		if (pendingStreams < streamLimit(preview)) {
			pendingStreams++;
			return Promise.resolve(true);
		}
		return new Promise((resolve) => {
			const leave = () => {
				streamWaiters.delete(waiter);
				resolve(false);
			};
			const waiter = { preview, start: () => { res.off("close", leave); resolve(true); } };
			streamWaiters.add(waiter);
			res.once("close", leave);
		});
	};
	const releaseStream = (): void => {
		pendingStreams--;
		for (const waiter of streamWaiters) {
			if (pendingStreams >= streamLimit(waiter.preview)) continue;
			streamWaiters.delete(waiter);
			pendingStreams++;
			waiter.start();
			return;
		}
	};
	return { acquireStream, releaseStream };
}

export const getOverSession = (sessionPath: (session: ClientHttp2Session, path: string) => string, authority: string, session: ClientHttp2Session, href: string): Promise<Response> =>
	new Promise((resolve, reject) => {
		const url = new URL(href);
		const stream = session.request({ ":method": "GET", ":path": sessionPath(session, `${url.pathname}${url.search}`), ":authority": authority }, { endStream: true });
		stream.setTimeout(5_000, () => stream.close(http2Constants.NGHTTP2_CANCEL));
		const chunks: Buffer[] = [];
		let status = 0;
		stream.on("response", (headers) => {
			status = Number(headers[":status"]);
		});
		stream.on("data", (chunk: Buffer) => chunks.push(chunk));
		stream.on("end", () => {
			try {
				resolve(new Response(Buffer.concat(chunks), { status }));
			} catch (error) {
				// No status, or one that can't carry a body (204, 304).
				reject(new UpstreamHttp2Unavailable((error as Error).message));
			}
		});
		stream.on("error", (error: Error) => reject(new UpstreamHttp2Unavailable(error.message)));
		stream.on("close", () => reject(new UpstreamHttp2Unavailable("session closed")));
	});

// A path can go silent without a FIN or RST (a VPN or network change), and
// the session would then carry every request into nothing. A ping still
// unanswered when the next is due, with nothing else heard from the host in
// between, retires it: its streams fail, and the next request opens a fresh
// session. Anything else counts because a ping waits behind the data already
// on the wire: over a slow link the host's window updates for an upload (or
// the download itself) come back long before the ping's answer.
export const keepAlive = (session: ClientHttp2Session, pingMs: number): void => {
	let awaitingPong = false;
	// Only the socket's byte count: reading from the socket is the session's alone.
	let received = 0;
	const timer = setInterval(() => {
		if (session.destroyed) return;
		const quiet = session.socket.bytesRead === received;
		received = session.socket.bytesRead;
		if (awaitingPong && quiet) return void session.destroy(new Error("upstream stopped answering pings"));
		if (!awaitingPong) awaitingPong = session.ping(() => { awaitingPong = false; });
	}, pingMs);
	timer.unref();
	session.once("close", () => clearInterval(timer));
};
