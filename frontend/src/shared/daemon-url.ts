/** Message of the error main raises when a daemon URL is refused because loopback TCP is off (B203). */
export const DAEMON_TCP_OFF_ERROR = "daemon-loopback-tcp-off";

/**
 * Whether a URL is served by the local daemon (its API host or a preview host,
 * on its port). With loopback TCP off (B176) such a URL works inside the app
 * but is a dead link in the system browser (B180). Shared by main and renderer.
 */
export function isLocalDaemonUrlOnPort(rawUrl: string, port: number | string | undefined): boolean {
	if (port === undefined || port === "") return false;
	try {
		const url = new URL(rawUrl);
		if (url.protocol !== "http:" || url.port !== String(port)) return false;
		const host = url.hostname.toLowerCase();
		return host === "127.0.0.1" || host === "localhost" || host === "[::1]" || host.endsWith(".localhost");
	} catch {
		return false;
	}
}
