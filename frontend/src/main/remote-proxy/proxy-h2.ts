/* HTTP/2 session forwarding for the remote proxy (B303). */
import { type IncomingHttpHeaders } from "node:http";
import { constants as http2Constants, type ClientHttp2Session, type Http2ServerRequest } from "node:http2";
import { PreviewTarget, ProxyRequest, ProxyResponse } from "./common";
import type { ProxyCore } from "./proxy-core";

export function createSessionForwarder(core: ProxyCore) {
	const { answerUpstreamFailure, forwardHeaders, relayResponse, releaseStream, sessionPath, upstream } = core;

	// Node's HTTP/2 client ends a GET, HEAD or DELETE with its headers unless
	// told otherwise, which would drop the body a DELETE can carry.
	const hasBody = (req: ProxyRequest): boolean =>
		Number(req.headers["content-length"] ?? 0) > 0 ||
		req.headers["transfer-encoding"] !== undefined ||
		(req as Partial<Http2ServerRequest>).stream?.endAfterHeaders === false;

	// The caller holds a slot for the request (acquireStream); it is freed once
	// the response headers arrive or the stream closes without them.
	const forwardOverSession = (session: ClientHttp2Session, req: ProxyRequest, res: ProxyResponse, path: string, kind: PreviewTarget["kind"] | undefined, retry?: () => void): void => {
		// Closing an EventSource or tab must also close its upstream stream.
		let stream: ReturnType<ClientHttp2Session["request"]> | undefined;
		let clientGone = false;
		res.once("close", () => {
			clientGone = !res.writableEnded;
			if (clientGone) stream?.close(http2Constants.NGHTTP2_CANCEL);
		});
		let pending = true;
		const settle = () => {
			if (!pending) return;
			pending = false;
			releaseStream();
		};
		const headers = forwardHeaders(req.headers, kind);
		delete headers.host; // HTTP/2 names the host in :authority
		try {
			stream = session.request(
				{ ...headers, ":method": req.method ?? "GET", ":path": sessionPath(session, path), ":authority": upstream.host },
				hasBody(req) ? { endStream: false } : undefined,
			);
		} catch (error) {
			settle();
			answerUpstreamFailure(res, error as Error, req.method, path, kind);
			return;
		}
		const upstreamStream = stream;
		// The host never acted on a stream it refused (RFC 9113 §8.7): the
		// request goes again, if the caller allows, instead of answering 502.
		const refused = () => retry !== undefined && upstreamStream.rstCode === http2Constants.NGHTTP2_REFUSED_STREAM;
		let responded = false;
		upstreamStream.once("close", () => {
			settle();
			// Closed before its response: the host restarted, the tunnel closed or
			// the stream was reset. Node reports a cancelled stream without "error".
			if (responded || clientGone || res.writableEnded) return;
			if (refused()) retry?.();
			else answerUpstreamFailure(res, new Error(`stream closed with code ${upstreamStream.rstCode}`), req.method, path, kind);
		});
		upstreamStream.once("response", (responseHeaders) => {
			responded = true;
			settle();
			// A cut stream ends with its session destroyed, often with code 0.
			// The stream's own destroyed flag can't tell: when a small answer's
			// close lands in the same read as its headers (as through the
			// tunnel), Node destroys the finished stream before "end".
			relayResponse(res, Number(responseHeaders[":status"]), responseHeaders as IncomingHttpHeaders, upstreamStream, req.method, path, kind,
				() => session.destroyed || upstreamStream.rstCode !== http2Constants.NGHTTP2_NO_ERROR);
		});
		upstreamStream.on("error", (error: Error) => {
			if (!refused()) answerUpstreamFailure(res, error, req.method, path, kind);
		});
		req.pipe(upstreamStream);
	};

	return { hasBody, forwardOverSession };
}
