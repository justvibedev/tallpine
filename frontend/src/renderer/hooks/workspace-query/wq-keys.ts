/** Workspace query keys. */
import { LOCAL_HOST } from "../../lib/hosts";

export const workspaceQueryKey = ["workspaces"] as const;
export const remoteWorkspaceQueryKey = (hostId: string) => ["remote-workspaces", hostId] as const;
export const workspaceQueryKeyForHost = (hostId?: string) => hostId ? remoteWorkspaceQueryKey(hostId) : workspaceQueryKey;
export const remoteProjectsQueryKey = (hostId: string) => [...remoteWorkspaceQueryKey(hostId), "projects"] as const;
export const remoteSessionsQueryKey = (hostId: string) => [...remoteWorkspaceQueryKey(hostId), "sessions"] as const;

/**
 * Per-session artifact listing. The daemon's session list carries only
 * `hasArtifacts`; the files come from the single-session read and are cached
 * for ARTIFACT_FILES_STALE_MS, so a full workspace refresh reuses them instead
 * of walking every artifact directory again.
 */
export const sessionArtifactsQueryKey = (sessionId: string, hostId?: string) =>
	["session-artifacts", hostId ?? LOCAL_HOST, sessionId] as const;
