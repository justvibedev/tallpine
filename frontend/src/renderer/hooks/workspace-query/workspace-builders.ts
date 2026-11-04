/** Builders that merge live sessions into workspace summaries. */
import { type QueryClient } from "@tanstack/react-query";
import type { components } from "../../../api/schema";
import { apiClient, hasTrustedApiBaseUrl } from "../../lib/api-client";
import { mockWorkspaces } from "../../lib/mock-data";
import { usesPreviewWorkspaceData } from "../../lib/preview-mode";
import { clientForHost } from "../../lib/host-clients";
import { applyOptimisticSessionKills } from "../optimistic-session-kills";
import { toAgentProvider, toProjectKind, type WorkspaceSession, type WorkspaceSummary, STANDALONE_PROJECT_KIND, STANDALONE_WORKSPACE_ID } from "../../types/workspace";
import { SessionView, clientFor, recheckRemoteHost, FakeAgentSeam, DaemonNotReadyError, withArtifactFiles } from "./wq-session-fetch";
import { standaloneWorkspaceName, toWorkspaceSession, placeStandaloneWorkspaceLast, toLocalWorkspaceSession } from "./session-mapping";

export type LiveSessions = {
	sessions: SessionView[];
	/** Terminated worker sessions the daemon holds, per project id ("" = standalone). */
	archivedByProject: Record<string, number>;
};

/**
 * Every live session of one daemon, plus the size of its archive. The archive
 * itself (terminated sessions) is paged on demand by useArchivedSessions: the
 * list used to ship all of history on every refetch. Workers and orchestrators
 * are two requests so the daemon's archive summary counts workers only, which is
 * what the Done/Archive surfaces show.
 */
export async function fetchLiveSessions(hostId?: string): Promise<LiveSessions> {
	const api = clientFor(hostId);
	const [workers, orchestrators] = await Promise.all([
		api.GET("/api/v1/sessions", { params: { query: { active: true, kind: "worker" } } }),
		api.GET("/api/v1/sessions", { params: { query: { active: true, kind: "orchestrator" } } }),
	]);
	for (const result of [workers, orchestrators]) {
		if (!result.error) continue;
		if (hostId && result.response) recheckRemoteHost(hostId, result.response.status);
		throw result.error;
	}
	const seen = new Set<string>();
	const sessions: SessionView[] = [];
	for (const session of [...(workers.data?.sessions ?? []), ...(orchestrators.data?.sessions ?? [])]) {
		if (seen.has(session.id)) continue;
		seen.add(session.id);
		sessions.push(session);
	}
	return { sessions, archivedByProject: workers.data?.archived?.byProject ?? {} };
}

export function buildWorkspaces(
	projects: Array<components["schemas"]["ProjectSummary"]>,
	{ sessions, archivedByProject }: LiveSessions,
	hostId?: string,
): WorkspaceSummary[] {
	const standaloneName = standaloneWorkspaceName();
	const withHost = <T extends WorkspaceSession>(session: T): T => (hostId ? { ...session, hostId } : session);
	const workspaces: WorkspaceSummary[] = projects.map((project) => ({
		...(hostId ? { hostId } : {}),
		id: project.id,
		name: project.name,
		kind: toProjectKind(project.kind),
		path: project.path,
		folderMissing: project.folderMissing,
		orchestratorAgent: project.orchestratorAgent ? toAgentProvider(project.orchestratorAgent) : undefined,
		archivedCount: archivedByProject[project.id] ?? 0,
		sessions: sessions
			.filter((session) => session.projectId === project.id)
			.map((session) => withHost(toWorkspaceSession(session, project))),
	}));
	const standaloneSessions = sessions.filter((session) => !session.projectId);
	const standaloneArchived = archivedByProject[""] ?? 0;
	if (standaloneSessions.length === 0 && standaloneArchived === 0) return workspaces;
	return placeStandaloneWorkspaceLast([
		...workspaces,
		{
			...(hostId ? { hostId } : {}),
			id: STANDALONE_WORKSPACE_ID,
			name: standaloneName,
			kind: STANDALONE_PROJECT_KIND,
			path: "Not attached to a project",
			archivedCount: standaloneArchived,
			sessions: standaloneSessions.map((session) =>
				hostId
					? withHost(toWorkspaceSession(session, { id: STANDALONE_WORKSPACE_ID, name: standaloneName }))
					: toLocalWorkspaceSession(session, STANDALONE_WORKSPACE_ID, standaloneName),
			),
		},
	]);
}

export async function fetchWorkspaces(client: QueryClient): Promise<WorkspaceSummary[]> {
	if (usesPreviewWorkspaceData) {
		const fake =
			typeof window !== "undefined"
				? (window as unknown as { __tallpineFakeAgent?: FakeAgentSeam }).__tallpineFakeAgent
				: undefined;
		const snapshot = fake ? fake.snapshot() : mockWorkspaces;
		return applyOptimisticSessionKills(snapshot) ?? snapshot;
	}
	if (!hasTrustedApiBaseUrl()) {
		throw new DaemonNotReadyError();
	}

	const [{ data: projectsData, error: projectsError }, live] = await Promise.all([
		apiClient.GET("/api/v1/projects"),
		fetchLiveSessions(),
	]);
	if (projectsError) {
		throw projectsError;
	}
	const sessions = await withArtifactFiles(client, undefined, live.sessions);
	const workspaces = buildWorkspaces(projectsData?.projects ?? [], { ...live, sessions });
	// Pending optimistic kills must survive CDC/refetch while the daemon kill is in flight.
	return applyOptimisticSessionKills(workspaces) ?? workspaces;
}

export async function fetchRemoteProjects(hostId: string) {
	const { data, error, response } = await clientForHost(hostId).GET("/api/v1/projects");
	if (error) {
		recheckRemoteHost(hostId, response.status);
		throw error;
	}
	return data?.projects ?? [];
}

export async function fetchRemoteSessions(client: QueryClient, hostId: string): Promise<LiveSessions> {
	const live = await fetchLiveSessions(hostId);
	return { ...live, sessions: await withArtifactFiles(client, hostId, live.sessions) };
}

export function toRemoteWorkspaces(
	hostId: string,
	remoteProjects: Awaited<ReturnType<typeof fetchRemoteProjects>>,
	live: Awaited<ReturnType<typeof fetchRemoteSessions>>,
): WorkspaceSummary[] {
	const projects = buildWorkspaces(remoteProjects, live, hostId);
	return applyOptimisticSessionKills(projects) ?? projects;
}

export async function fetchRemoteWorkspaces(client: QueryClient, hostId: string): Promise<WorkspaceSummary[]> {
	const [projects, sessions] = await Promise.all([fetchRemoteProjects(hostId), fetchRemoteSessions(client, hostId)]);
	return toRemoteWorkspaces(hostId, projects, sessions);
}
