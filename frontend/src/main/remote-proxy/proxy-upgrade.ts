/* WebSocket upgrade forwarding for the remote proxy (B303). */
import { type IncomingMessage } from "node:http";
import { type Socket } from "node:net";
import { safeLogPath, warn } from "./common";
import type { ProxyCore } from "./proxy-core";

export function createUpgradeHandler(core: ProxyCore) {
	const { dialUpstream, forwardHeaders, previewPath, stripToken, tunnels, upstream, validControlOrigin, validPreviewOrigin, verifyUpstream } = core;

	const handleUpgrade = (req: IncomingMessage, socket: Socket, head: Buffer, scheme: "http" | "https"): void => {
		const preview = previewPath(req.headers.host, req.url, req.method, scheme);
		// Static preview hosts never tunnel sockets. In particular a path
		// containing the control token must not fall back to the daemon mux.
		if (preview && preview.kind !== "app" && preview.kind !== "agent") {
			socket.destroy();
			return;
		}
		const path = preview ? preview.path : stripToken(req.url);
		if (path === null) {
			socket.destroy();
			return;
		}
		if ((!preview && !validControlOrigin(req)) || !validPreviewOrigin(scheme, req.headers.host, req.headers.origin, preview?.kind)) {
			socket.destroy();
			return;
		}
		const stopPending = () => {
			tunnels.delete(stopPending);
			socket.destroy();
		};
		tunnels.add(stopPending);
		socket.once("close", stopPending);
		void verifyUpstream().then(() => {
			tunnels.delete(stopPending);
			socket.off("close", stopPending);
			if (socket.destroyed) return;
			const upstreamSocket = dialUpstream(() => {
				const headers = forwardHeaders(req.headers, preview?.kind);
				if (!preview) {
					delete headers["sec-fetch-site"];
					delete headers["sec-fetch-mode"];
					delete headers["sec-fetch-dest"];
				}
				const lines = [`${req.method} ${path} HTTP/1.1`];
				// The WebSocket-specific headers were stripped as hop-by-hop; restore
				// the two the handshake requires, with the credential injected above.
				headers.connection = "Upgrade";
				headers.upgrade = String(req.headers.upgrade ?? "websocket");
				for (const [name, value] of Object.entries(headers)) {
					for (const v of Array.isArray(value) ? value : [value]) lines.push(`${name}: ${v}`);
				}
				upstreamSocket.write(lines.join("\r\n") + "\r\n\r\n");
				if (head.length > 0) upstreamSocket.write(head);
				socket.pipe(upstreamSocket);
				upstreamSocket.pipe(socket);
			});
			upstreamSocket.on("error", (error: Error) => {
				warn(`upstream ${upstream.host} tunnel failed on ${safeLogPath(path, preview?.kind)} (${error.message})`);
			});
			const drop = () => {
				tunnels.delete(drop);
				socket.destroy();
				upstreamSocket.destroy();
			};
			tunnels.add(drop);
			// http.Server keeps upgraded sockets half-open (allowHalfOpen), so a peer
			// that only sends FIN never triggers "close" — tear the pair down on
			// "end" too or every closed terminal leaks a socket to the remote host.
			for (const event of ["error", "end", "close"] as const) {
				upstreamSocket.on(event, drop);
				socket.on(event, drop);
			}
		}).catch((error: Error) => {
			warn(`upstream ${upstream.host} identity check failed on ${safeLogPath(path, preview?.kind)} (${error.message})`);
			stopPending();
		});
	};

	return handleUpgrade;
}
