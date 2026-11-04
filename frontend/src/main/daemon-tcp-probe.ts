import { connect } from "node:net";

/**
 * Whether the daemon answers on loopback TCP (B180). The daemon serves its
 * socket only by default (B176), so a URL naming 127.0.0.1:<port> opens in
 * the app (main serves it over the socket) but is a dead link in an external
 * browser. A real connect is the only source of truth: the run file does not
 * record whether TALLPINE_LOOPBACK_TCP is set, and another process may own
 * the port.
 */
export function probeDaemonTcp(port: number | undefined, timeoutMs = 500): Promise<boolean> {
	if (!port) return Promise.resolve(false);
	return new Promise((resolve) => {
		const socket = connect({ host: "127.0.0.1", port });
		const done = (open: boolean) => {
			socket.destroy();
			resolve(open);
		};
		socket.setTimeout(timeoutMs, () => done(false));
		socket.once("connect", () => done(true));
		socket.once("error", () => done(false));
	});
}
