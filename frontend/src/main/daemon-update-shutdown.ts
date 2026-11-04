import { DAEMON_TOKEN_HEADER } from "./daemon-auth";

/**
 * An update restart must not leave the old daemon binary running: the new app
 * would attach to it and the update would never reach the daemon. An
 * app-owned daemon is already stopped by the normal quit path. A keep-alive or
 * attached `tallpine start` daemon is not, so on an update it gets the same
 * bare /shutdown `scripts/release-local install` sends: it exits, its agents
 * survive, and the daemon the new app starts adopts them.
 */
export function shouldShutdownDaemonForUpdate(input: {
	updateRestart: boolean;
	appOwned: boolean;
	ready: boolean;
	port?: number;
}): boolean {
	return input.updateRestart && !input.appOwned && input.ready && Boolean(input.port);
}

/** Bare /shutdown, then wait until /healthz stops answering. Resolves false on failure or timeout. */
export async function shutdownDaemonForUpdate(
	port: number,
	token: string | null | undefined,
	fetchImpl: typeof fetch = fetch,
	timeoutMs = 10_000,
	pollMs = 200,
): Promise<boolean> {
	const base = `http://127.0.0.1:${port}`;
	try {
		const response = await fetchImpl(`${base}/shutdown`, {
			method: "POST",
			headers: token ? { [DAEMON_TOKEN_HEADER]: token } : {},
			signal: AbortSignal.timeout(3000),
		});
		if (!response.ok) return false;
	} catch {
		return false;
	}
	const deadline = Date.now() + timeoutMs;
	while (Date.now() < deadline) {
		try {
			await fetchImpl(`${base}/healthz`, { signal: AbortSignal.timeout(1000) });
		} catch {
			return true;
		}
		await new Promise((resolve) => setTimeout(resolve, pollMs));
	}
	return false;
}
