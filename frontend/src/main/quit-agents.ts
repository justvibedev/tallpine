import { DAEMON_TOKEN_HEADER } from "./daemon-auth";

/**
 * A full quit (the tray's Quit, an OS logout, restart or shutdown) ends the
 * app-owned daemon, so it must not leave agents running without one. This asks
 * the daemon to stop every agent's processes before it exits; each session
 * sleeps and resumes its conversation on its next message.
 *
 * Only an app-owned daemon, the one the supervisor link stops when the app
 * goes, is asked. A keep-alive or headless `tallpine start` daemon outlives the app
 * on purpose, and so do its agents. A daemon restart or takeover never comes
 * through here: it sends a bare /shutdown, so agents survive for the next
 * daemon to adopt.
 */
export function shouldStopAgentsOnQuit(daemon: {
	ready: boolean;
	port?: number;
	linked: boolean;
	keepAlive: boolean;
}): boolean {
	return daemon.ready && Boolean(daemon.port) && daemon.linked && !daemon.keepAlive;
}

/** Sends the deliberate-quit shutdown. Resolves false on any failure. */
export async function requestStopAgents(
	port: number,
	token: string | null | undefined,
	fetchImpl: typeof fetch = fetch,
	timeoutMs = 3000,
): Promise<boolean> {
	try {
		const response = await fetchImpl(`http://127.0.0.1:${port}/shutdown`, {
			method: "POST",
			headers: {
				"content-type": "application/json",
				...(token ? { [DAEMON_TOKEN_HEADER]: token } : {}),
			},
			body: JSON.stringify({ stopAgents: true }),
			signal: AbortSignal.timeout(timeoutMs),
		});
		return response.ok;
	} catch {
		return false;
	}
}
