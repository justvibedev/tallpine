/* Split out of remote-proxy.ts (B297, P53). */
import { createUpstreamResponders } from "./upstream-responders";
import { createUpstreamSession } from "./upstream-session";

import { createHmac, randomBytes } from "node:crypto";
import {
	createServer,
	request as httpRequest,
	type IncomingMessage,
	type Server,
} from "node:http";
import { createSecureServer } from "node:http2";
import { request as httpsRequest } from "node:https";
import { isIP, connect as netConnect, type AddressInfo, type Socket } from "node:net";
import { connect as tlsConnect, type TLSSocket } from "node:tls";
import { listenOnStablePort } from "../agent-viewer";
import { previewCertificate } from "../preview-certificate";
import { pinnedTlsOptions } from "../remote-tls";
import type { RemoteEntry } from "../remotes-store";
import { ActiveProxy, PreviewTarget, RENDERER_ORIGIN, UPSTREAM_TIMING, log } from "./common";
import { createRequestRules } from "./proxy-rules";
import type { ProxyCore } from "./proxy-core";
import { createForwarders } from "./proxy-forward";
import { createPreviewApi } from "./proxy-preview";

export async function startRemoteProxy(entry: RemoteEntry, rendererOrigin = RENDERER_ORIGIN, timing = UPSTREAM_TIMING): Promise<ActiveProxy> {
	const token = randomBytes(16).toString("hex");
	const corsHeaders: Record<string, string> = {
		"access-control-allow-origin": rendererOrigin,
		"access-control-allow-methods": "GET, POST, PUT, PATCH, DELETE, OPTIONS",
		vary: "origin",
	};
	const upstream = new URL(entry.url.includes("://") ? entry.url : `http://${entry.url}`);
	if (!["http:", "https:"].includes(upstream.protocol) || upstream.username || upstream.password)
		throw new Error("remote host must use an HTTP(S) URL without embedded credentials");
	const secure = upstream.protocol === "https:";
	// A preview's origin is stable per host, session and kind, and unguessable
	// without the host's password: a dev-server restart or a reconnect keeps
	// the app's cookies and storage (a random origin per start logged you out).
	const originKey = entry.password || randomBytes(32).toString("hex");
	const originMac = (subject: string) => createHmac("sha256", originKey).update(`${upstream.host}\n${subject}`).digest();
	const upstreamPort = Number(upstream.port || (secure ? 443 : 80));
	const hostname = upstream.hostname.replace(/^\[|\]$/g, "");
	// An https host must be spoken to over TLS. Forwarding it as cleartext puts
	// the connection password on the wire in the clear — and the address bar
	// already accepts https:// (AddRemoteHostDialog), so this is reachable by
	// typing a Tailscale Serve or reverse-proxy URL.
	const pinned = secure ? pinnedTlsOptions(entry.cert) : {};
	const upstreamRequest = secure ? httpsRequest : httpRequest;
	const dialUpstream = (onReady: () => void): Socket =>
		secure
			? tlsConnect(upstreamPort, hostname, { servername: isIP(hostname) ? undefined : hostname, ...pinned }, onReady)
			: netConnect(upstreamPort, hostname, onReady);
	// A host may be a daemon behind a reverse proxy at a path ("http://box/tallpine").
	// The renderer's base is the proxy's own root, so the upstream prefix has to
	// be restored here — without it every forwarded request, credential and all,
	// lands on whatever else that vhost serves at /api/v1.
	const prefix = upstream.pathname.replace(/\/+$/, "");
	const server: Server = createServer();
	const previewHosts = new Map<string, PreviewTarget>(); // by hostname: see previewPath
	const artifactHosts = new Map<string, { host: string; sourceHref: string }>();
	const previewSessions = new Map<string, { host: string; sourceUrl: string; sourceHref: string; url: string }>();
	const tunnels = new Set<() => void>();
	// Allow slow uploads; SSE response timeouts are disabled on the upstream request below.
	server.requestTimeout = 0;

	const { stripToken, previewPath, forwardHeaders, validControlOrigin, validPreviewOrigin } = createRequestRules({ entry, prefix, previewHosts, rendererOrigin, token, upstream });

	const { releaseStream, sessionPath, acquireStream, sessionForRequest, verifyUpstream, getUpstreamSession } = createUpstreamSession({ entry, timing, secure, upstream, prefix, hostname, pinned });

	const { answerIdentityFailure, answerUpstreamFailure, relayResponse } = createUpstreamResponders(upstream, corsHeaders);

	const core: ProxyCore = {
		releaseStream, sessionPath, acquireStream, sessionForRequest, verifyUpstream, getUpstreamSession,
		answerIdentityFailure, answerUpstreamFailure, relayResponse,
		token, corsHeaders, pinned, tunnels, upstream, hostname, upstreamPort, upstreamRequest, dialUpstream,
		stripToken, previewPath, forwardHeaders, validControlOrigin, validPreviewOrigin,
	};
	const { handleRequest, handleUpgrade } = createForwarders(core);

	server.on("request", (req, res) => void handleRequest(req, res, "http"));
	server.on("upgrade", (req, socket: Socket, head: Buffer) => handleUpgrade(req, socket, head, "http"));

	// Previews are served over HTTPS too, so the browser panel speaks HTTP/2 to
	// them (see preview-certificate.ts). HTTP/1.1 stays on for WebSockets.
	const { cert, key } = previewCertificate();
	const secureServer = createSecureServer({ cert, key, allowHTTP1: true });
	const secureSockets = new Set<TLSSocket>();
	secureServer.on("secureConnection", (socket: TLSSocket) => {
		secureSockets.add(socket);
		socket.once("close", () => secureSockets.delete(socket));
	});
	secureServer.on("request", (req, res) => void handleRequest(req, res, "https"));
	secureServer.on("upgrade", (req: IncomingMessage, socket: Socket, head: Buffer) => handleUpgrade(req, socket, head, "https"));
	const securePort = await listenOnStablePort(secureServer, originMac("port"));

	await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
	const { address: listeningAddress, port } = server.address() as AddressInfo;
	log(`started on 127.0.0.1:${port} (previews on ${securePort}) for ${upstream.host}`);
	return {
		base: `http://127.0.0.1:${port}/${token}`,
		listeningAddress,
		...createPreviewApi({ previewHosts, artifactHosts, previewSessions, originMac, token, port, securePort }),
		close: () =>
			new Promise((resolve) => {
				// close() alone waits on keep-alive and tunnelled sockets forever;
				// a deactivated proxy must actually stop serving.
				for (const drop of tunnels) drop();
				server.closeAllConnections();
				for (const socket of secureSockets) socket.destroy();
				void getUpstreamSession()?.then((session) => session.destroy(), () => undefined);
				let open = 2;
				const closed = () => {
					if (--open > 0) return;
					log(`stopped on 127.0.0.1:${port} for ${upstream.host}`);
					resolve();
				};
				server.close(closed);
				secureServer.close(closed);
			}),
	};
}
