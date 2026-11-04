/** Single-session read, artifact listing and remote health recheck for workspace queries. */
import { PRODUCT_NAME } from "../../../shared/brand";
import { type QueryClient } from "@tanstack/react-query";
import type { components } from "../../../api/schema";
import { apiClient } from "../../lib/api-client";
import { tallpineBridge } from "../../lib/bridge";
import { clientForHost } from "../../lib/host-clients";
import { requestRemoteHostsRefresh } from "../useRemoteHosts";
import { type WorkspaceSummary } from "../../types/workspace";
import { sessionArtifactsQueryKey } from "./wq-keys";

export const lastRemoteHealthRecheck = new Map<string, number>();

export function recheckRemoteHost(hostId: string, status: number): void {
	if (![401, 403, 426, 502, 503].includes(status)) return;
	const now = Date.now();
	const last = lastRemoteHealthRecheck.get(hostId);
	if (last !== undefined && now >= last && now - last < 15_000) return;
	lastRemoteHealthRecheck.set(hostId, now);
	requestRemoteHostsRefresh();
}

// e2e seam (dev:web only): the Playwright fake-agent harness injects
// `window.__tallpineFakeAgent` (see e2e/support/fake-bridge.ts) to drive a
// deterministic, mutable session timeline off the SSE refetch path. Compiled
// out of the packaged build — the packaged renderer never sets VITE_NO_ELECTRON
// and always hits the real daemon.
export type FakeAgentSeam = { snapshot: () => WorkspaceSummary[] };

export class DaemonNotReadyError extends Error {
	constructor() {
		super(`${PRODUCT_NAME} daemon API is not ready`);
	}
}

export type SessionView = components["schemas"]["SessionView"];

export const clientFor = (hostId?: string) => (hostId ? clientForHost(hostId) : apiClient);

export const ARTIFACT_FILES_STALE_MS = 10_000;

export async function remoteArtifactFiles(hostId: string | undefined, sessionId: string, files: SessionView["artifactFiles"]) {
	if (!hostId) return files;
	return Promise.all((files ?? []).map(async (artifact) => ({
		...artifact,
		rawUrl: artifact.rawUrl ? await tallpineBridge.remotes.previewUrl(hostId, sessionId, artifact.rawUrl) : undefined,
		// The remote daemon's inline origin is a localhost name, which here
		// would mean this computer; a remote chat shows no frame anyway.
		inlineUrl: undefined,
	})));
}

/** One session through the daemon's single-session read (which lists its artifact files). */
export async function fetchSessionView(hostId: string | undefined, sessionId: string): Promise<SessionView | undefined> {
	const { data, error, response } = await clientFor(hostId).GET("/api/v1/sessions/{sessionId}", {
		params: { path: { sessionId } },
	});
	if (error) {
		if (hostId && response) recheckRemoteHost(hostId, response.status);
		throw error;
	}
	const session = data?.session;
	if (!session) return undefined;
	return hostId ? { ...session, artifactFiles: await remoteArtifactFiles(hostId, session.id, session.artifactFiles) } : session;
}

export async function withArtifactFiles(client: QueryClient, hostId: string | undefined, sessions: SessionView[]): Promise<SessionView[]> {
	return Promise.all(sessions.map(async (session) => {
		// A daemon from before the list dropped its file listing still sends it.
		if (session.artifactFiles) return hostId ? { ...session, artifactFiles: await remoteArtifactFiles(hostId, session.id, session.artifactFiles) } : session;
		if (!session.hasArtifacts || session.isTerminated) return session;
		const queryKey = sessionArtifactsQueryKey(session.id, hostId);
		try {
			const artifactFiles = await client.fetchQuery({
				queryKey,
				queryFn: async () => (await fetchSessionView(hostId, session.id))?.artifactFiles ?? [],
				staleTime: ARTIFACT_FILES_STALE_MS,
			});
			return { ...session, artifactFiles };
		} catch {
			const stale = client.getQueryData<SessionView["artifactFiles"]>(queryKey);
			return stale ? { ...session, artifactFiles: stale } : session;
		}
	}));
}

/**
 * Drop a cached artifact listing so the next read of the session lists its
 * directory again (a file written moments ago is not in a cached listing).
 */
export function forgetSessionArtifactFiles(client: QueryClient, sessionId: string, hostId?: string): void {
	client.removeQueries({ queryKey: sessionArtifactsQueryKey(sessionId, hostId) });
}
