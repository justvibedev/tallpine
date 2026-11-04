import { type DaemonProbe, parseDaemonProbe } from "../../shared/daemon-attach";
import { type DaemonLaunchSpec, bundledDaemonIdentityError } from "../../shared/daemon-launch";
import { canonicalPathInside, sameCanonicalPath } from "../../shared/path-identity";

export const DAEMON_PROBE_TIMEOUT_MS = 2_000;

export function processAlive(pid: number): boolean {
	if (!pid) return false;
	try {
		process.kill(pid, 0);
		return true;
	} catch {
		return false;
	}
}

export async function readDaemonProbe(
	port: number,
	endpoint: "healthz" | "readyz",
	fetchImpl: typeof fetch = fetch,
): Promise<DaemonProbe | null> {
	const controller = new AbortController();
	const timer = setTimeout(() => controller.abort(), DAEMON_PROBE_TIMEOUT_MS);
	try {
		// Electron net.fetch adds browser metadata without Origin, which the daemon rejects.
		const response = await fetchImpl(`http://127.0.0.1:${port}/${endpoint}`, { signal: controller.signal });
		if (!response.ok) return null;
		return parseDaemonProbe(endpoint, await response.json());
	} catch {
		return null;
	} finally {
		clearTimeout(timer);
	}
}

export function daemonIdentityError(
	launch: DaemonLaunchSpec,
	probe: DaemonProbe,
	productName: string,
	appImage: string | undefined,
): string | null {
	if (launch.source === "dev") {
		const cwdMatches = probe.workingDirectory ? sameCanonicalPath(probe.workingDirectory, launch.cwd) : false;
		const startupCwdMatches = probe.startupWorkingDirectory
			? sameCanonicalPath(probe.startupWorkingDirectory, launch.cwd)
			: false;
		const executableMatches = probe.executablePath ? canonicalPathInside(probe.executablePath, launch.cwd) : false;
		if (!probe.workingDirectory && !probe.startupWorkingDirectory && !probe.executablePath) {
			return `An older ${productName} daemon is already running, but it does not report its checkout identity. Stop it and restart this app.`;
		}
		if (!cwdMatches && !startupCwdMatches && !executableMatches) {
			const actual =
				probe.startupWorkingDirectory ?? probe.workingDirectory ?? probe.executablePath ?? "an unknown location";
			return `Another ${productName} daemon is already running from ${actual}; expected this checkout at ${launch.cwd}. Stop the other daemon before using this checkout.`;
		}
		return null;
	}

	if (launch.source === "bundled") {
		return bundledDaemonIdentityError(probe, launch.command, appImage, sameCanonicalPath);
	}
	return null;
}
