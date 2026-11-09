import { DAEMON_TCP_OFF_ERROR, isLocalDaemonUrlOnPort } from "../shared/daemon-url";

const APP_EXTERNAL_PROTOCOLS = new Set(["http:", "https:", "mailto:"]);

export type ExternalOpener = {
	openExternal: (url: string) => Promise<unknown>;
};

export function isAllowedAppExternalURL(rawUrl: string): boolean {
	try {
		const url = new URL(rawUrl);
		return APP_EXTERNAL_PROTOCOLS.has(url.protocol);
	} catch {
		return false;
	}
}

export type DaemonUrlGate = {
	/** The local daemon's loopback TCP port, if known. */
	port: () => number | undefined;
	/** Whether the daemon currently answers on loopback TCP. */
	tcpOpen: (port: number | undefined) => Promise<boolean>;
};

/**
 * The one choke point for opening a URL in the system browser. With loopback
 * TCP off (B176) a daemon URL is a dead link outside the app, so it is not
 * opened (B203). A refused open rejects with DAEMON_TCP_OFF_ERROR so callers never see it as success.
 */
export async function openGatedExternalURL(rawUrl: string, opener: ExternalOpener, gate: DaemonUrlGate): Promise<void> {
	if (!isAllowedAppExternalURL(rawUrl)) throw new Error("Unsupported external URL");
	const port = gate.port();
	if (isLocalDaemonUrlOnPort(rawUrl, port) && !(await gate.tcpOpen(port))) throw new Error(DAEMON_TCP_OFF_ERROR);
	await opener.openExternal(rawUrl);
}
