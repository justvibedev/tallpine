import type { OrchestratorLink, SessionMode } from "./api";

type LaunchConductor = (projectId: string, clean?: boolean, mode?: SessionMode, hostId?: string, lane?: string) => Promise<OrchestratorLink>;

/**
 * `launchConductor` pinned to one machine, for a screen scoped to a host. It
 * keeps the full app signature (a caller such as the launcher passes the host
 * id in fourth place) and always uses the scope's host, never the passed one.
 */
export function scopeLaunchConductor(launch: LaunchConductor, hostId: string): LaunchConductor {
	return (projectId, clean, mode, _hostId, lane) => launch(projectId, clean, mode, hostId, lane);
}
