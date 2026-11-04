/* Request forwarding for the remote proxy (B303). */
import type { ProxyCore } from "./proxy-core";
import { createSessionForwarder } from "./proxy-h2";
import { createUpgradeHandler } from "./proxy-upgrade";
import { type ClientHttp2Session, type Http2ServerRequest } from "node:http2";
import { PreviewTarget, ProxyRequest, ProxyResponse } from "./common";

export function createForwarders(core: ProxyCore) {
	const { acquireStream, answerIdentityFailure, answerUpstreamFailure, corsHeaders, forwardHeaders, hostname, pinned, previewPath, relayResponse, releaseStream, sessionForRequest, stripToken, upstreamPort, upstreamRequest, validControlOrigin, validPreviewOrigin, verifyUpstream } = core;
	const { hasBody, forwardOverSession } = createSessionForwarder(core);
	const handleUpgrade = createUpgradeHandler(core);

	const forwardOverHttp1 = (req: ProxyRequest, res: ProxyResponse, path: string, kind?: PreviewTarget["kind"]): void => {
		const proxied = upstreamRequest(
			{
				host: hostname,
				port: upstreamPort,
				method: req.method,
				path,
				headers: forwardHeaders(req.headers, kind),
				...pinned,
			},
			(upstreamRes) => relayResponse(res, upstreamRes.statusCode ?? 502, upstreamRes.headers, upstreamRes, req.method, path, kind),
		);
		proxied.setTimeout(0);
		proxied.on("error", (error: Error) => answerUpstreamFailure(res, error, req.method, path, kind));
		// Closing an EventSource or tab must also close its upstream stream.
		// Otherwise the remote daemon keeps the SSE request alive after the
		// renderer is gone, and proxy shutdown can leave orphaned connections.
		// writableEnded, not writableFinished: an HTTP/2 response reports
		// finished once its stream closes, a browser's reset included.
		res.once("close", () => {
			if (!res.writableEnded) proxied.destroy();
		});
		req.pipe(proxied);
	};

	const handleRequest = async (req: ProxyRequest, res: ProxyResponse, scheme: "http" | "https"): Promise<void> => {
		// HTTP/2 carries the host in :authority rather than a Host header.
		const host = req.headers.host ?? (req as Http2ServerRequest).authority;
		const preview = previewPath(host, req.url, req.method, scheme);
		const path = preview?.path ?? stripToken(req.url);
		if (path === null) {
			res.writeHead(404, {
				"content-type": "application/json",
				...corsHeaders,
			});
			res.end('{"error":"unknown path"}');
			return;
		}
		if ((!preview && !validControlOrigin(req)) || !validPreviewOrigin(scheme, host, req.headers.origin, preview?.kind)) {
			res.writeHead(403, { "content-type": "application/json" });
			res.end('{"error":"preview origin mismatch"}');
			return;
		}
		if (req.method === "OPTIONS") {
			res.writeHead(204, {
				...corsHeaders,
				"access-control-allow-headers": String(req.headers["access-control-request-headers"] ?? "content-type"),
				...(req.headers["access-control-request-private-network"] === "true"
					? { "access-control-allow-private-network": "true" }
					: {}),
				"access-control-max-age": "600",
			});
			res.end();
			return;
		}
		// The browser may leave while the slot, session or identity check is
		// pending; an HTTP/2 response has no "destroyed" to say so afterwards.
		let left = false;
		res.once("close", () => { left = true; });
		const forward = async (mayRetry: boolean): Promise<void> => {
			// The slot before the session: one taken before a long wait for a slot
			// may have closed (a GOAWAY) by the time it ends.
			if (!(await acquireStream(preview !== null, res))) return;
			let session: ClientHttp2Session | null = null;
			try {
				session = await sessionForRequest();
				if (!session) await verifyUpstream();
			} catch (error) {
				answerIdentityFailure(res, error, req.method, path, preview?.kind);
			}
			// Only a request sent over the session keeps its slot.
			if (!session || left || res.writableEnded) releaseStream();
			if (left || res.writableEnded) return;
			if (session) forwardOverSession(session, req, res, path, preview?.kind, mayRetry && !hasBody(req) ? () => void forward(false) : undefined);
			else forwardOverHttp1(req, res, path, preview?.kind);
		};
		await forward(true);
	};

	return { handleRequest, handleUpgrade };
}
