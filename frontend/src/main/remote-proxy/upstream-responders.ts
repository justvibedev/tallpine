/* Split out of remote-proxy.ts (B297, P53). */
import type { IncomingHttpHeaders,OutgoingHttpHeaders } from "node:http";
import type { Readable } from "node:stream";
import { HOP_BY_HOP_HEADERS } from "../agent-viewer";
import { IncompatibleRemoteVersionError } from "../remote-request";
import type { PreviewTarget,ProxyResponse } from "./common";
import { safeLogPath, warn } from "./common";

/** How the proxy answers a client when the remote host fails, and relays a good upstream response. */
export function createUpstreamResponders(upstream: URL, corsHeaders: Record<string, string>) {
	const answerIdentityFailure = (res: ProxyResponse, error: unknown, method: string | undefined, path: string, kind?: PreviewTarget["kind"]): void => {
		warn(`upstream ${upstream.host} identity check failed on ${method} ${safeLogPath(path, kind)} (${(error as Error).message})`);
		const incompatible = error instanceof IncompatibleRemoteVersionError;
		res.writeHead(incompatible ? 426 : 502, { "content-type": "application/json", ...corsHeaders });
		res.end(incompatible
			? JSON.stringify({ error: "incompatible", code: "HOST_API_INCOMPATIBLE", message: (error as Error).message })
			: '{"error":"remote host identity not verified","code":"HOST_IDENTITY_UNVERIFIED","message":"remote host identity not verified"}');
	};

	const answerUpstreamFailure = (res: ProxyResponse, error: Error, method: string | undefined, path: string, kind?: PreviewTarget["kind"]): void => {
		warn(`upstream ${upstream.host} failed on ${method} ${safeLogPath(path, kind)} (${error.message}); answering 502`);
		if (res.headersSent) {
			// With an error, as in relayResponse.
			res.destroy(error);
			return;
		}
		res.writeHead(502, {
			"content-type": "application/json",
			...corsHeaders,
		});
		res.end('{"error":"remote daemon unreachable","code":"UPSTREAM_UNAVAILABLE","message":"remote daemon unreachable"}');
	};

	const relayResponse = (
		res: ProxyResponse,
		status: number,
		upstreamHeaders: IncomingHttpHeaders,
		body: Readable,
		method: string | undefined,
		path: string,
		kind?: PreviewTarget["kind"],
		// For an HTTP/2 body: whether its stream was cut off rather than finished.
		cutOff?: () => boolean,
	): void => {
		// HTTP/2 carries only 200-599 as a final status: writeHead would throw on
		// the rest (a dev server's 999, say), so those are the host's failure.
		if (!(status >= 200 && status <= 599)) {
			body.resume();
			answerUpstreamFailure(res, new Error(`invalid status ${status}`), method, path, kind);
			return;
		}
		if (kind === "app" && status === 409 && upstreamHeaders["x-ao-preview-managed-required"] === "1") {
			body.resume();
			res.writeHead(409, { "content-type": "text/html; charset=utf-8", "cache-control": "no-store" });
			res.end("<main style='font:16px system-ui;padding:24px'><h1>Remote preview needs a managed server</h1><p>Run <code>tallpine preview start</code> in this session on the host.</p></main>");
			return;
		}
		let dropped = false;
		const closeDroppedStream = () => {
			if (dropped) return;
			dropped = true;
			warn(`upstream ${upstream.host} stream ended on ${method} ${safeLogPath(path, kind)}`);
			// Destroyed without an error, an HTTP/2 response resets its stream with
			// NO_ERROR, which a browser takes as a complete body.
			res.destroy(new Error("upstream stream ended"));
		};
		body.on("aborted", closeDroppedStream);
		body.on("error", closeDroppedStream);
		const headers: OutgoingHttpHeaders = {};
		for (const [name, value] of Object.entries(upstreamHeaders)) {
			if (!name.startsWith(":") && !HOP_BY_HOP_HEADERS.includes(name)) headers[name] = value;
		}
		// The host confines legacy API documents with this final CSP policy.
		// Only our separately routed preview origins can shed that API-origin
		// sandbox. Keep every upstream app policy, including its own sandbox.
		if (kind !== undefined && upstreamHeaders["x-ao-preview-api-sandbox"] === "1") {
			const policy = headers["content-security-policy"];
			const sandbox = "sandbox allow-scripts allow-forms";
			if (typeof policy === "string") {
				if (policy === sandbox) delete headers["content-security-policy"];
				else if (policy.endsWith(`, ${sandbox}`)) headers["content-security-policy"] = policy.slice(0, -sandbox.length - 2);
			}
		}
		delete headers["x-ao-preview-api-sandbox"];
		Object.assign(headers, corsHeaders);
		if (kind !== undefined && kind !== "app") delete headers["set-cookie"];
		res.writeHead(status, headers);
		// Node holds the header block until the first body byte or an explicit
		// flush. An SSE upstream (GET /api/v1/events) can go arbitrarily long
		// before its first byte, so without this the client's EventSource sits
		// in CONNECTING forever — flush headers on their own, then pipe.
		// (An HTTP/2 response sends its headers in writeHead.)
		res.flushHeaders?.();
		// pipe streams SSE chunk-by-chunk once headers are already on the wire.
		if (!cutOff) {
			body.pipe(res);
			return;
		}
		// An HTTP/2 stream cut off by its session (the host restarted, the
		// tunnel closed) just ends, with no "aborted" or "error". Ending the
		// response then would hand the browser a truncated body as complete.
		body.pipe(res, { end: false });
		body.once("end", () => (cutOff() ? closeDroppedStream() : res.end()));
		body.once("close", () => {
			if (!res.writableEnded) closeDroppedStream();
		});
	};

	return { answerIdentityFailure, answerUpstreamFailure, relayResponse };
}
