import { isWaitingOnOrchestrator } from "../lib/session-presentation";
import { useQueries, useQuery, useQueryClient, type Query, type QueryClient } from "@tanstack/react-query";
import type { TraySessionEntry } from "../../shared/tray";
import { useEffect, useMemo
} from "react";
import { apiErrorCode } from "../lib/api-client";
import { useConnectedHosts } from "./useHostConnection";
import { newestActiveOrchestrator, attentionZone, workerSessions, type WorkspaceSession, type WorkspaceSummary, STANDALONE_WORKSPACE_ID } from "../types/workspace";
import { workspaceQueryKey, remoteProjectsQueryKey, remoteSessionsQueryKey, remoteWorkspaceQueryKey, workspaceQueryKeyForHost } from "./workspace-query/wq-keys";
import { fetchWorkspaces, fetchRemoteProjects, fetchRemoteSessions, toRemoteWorkspaces, fetchRemoteWorkspaces } from "./workspace-query/workspace-builders";
import { DaemonNotReadyError, fetchSessionView } from "./workspace-query/wq-session-fetch";
import { retainingStructuralSharing, retainWorkspaceSession } from "./workspace-query/retained-sessions";
import { standaloneWorkspaceName, toWorkspaceSession } from "./workspace-query/session-mapping";

// Public workspace query API; the implementations live in ./workspace-query/.
export { workspaceQueryKey, remoteWorkspaceQueryKey, workspaceQueryKeyForHost, sessionArtifactsQueryKey } from "./workspace-query/wq-keys";
export { fetchSessionView, forgetSessionArtifactFiles } from "./workspace-query/wq-session-fetch";
export { toWorkspaceSession } from "./workspace-query/session-mapping";
export { retainWorkspaceSession, retainingStructuralSharing } from "./workspace-query/retained-sessions";
export { refreshWorkspaceSession } from "./workspace-query/session-patches";

export function workspaceStatusesChecking(workspaces: WorkspaceSummary[] | undefined): boolean {
	return (
		workspaces?.some((workspace) => workspace.sessions.some((session) => session.statusReadiness === "checking")) ??
		false
	);
}
// Shared so route loaders can prefetch via queryClient.ensureQueryData (paired
// with the router's defaultPreload: "intent") and the hook reads the same cache.
// No refetchInterval here: every observer that carries one runs its own timer,
// and timers started at different mount times do not dedupe. With ~18
// subscribers that meant several /projects + /sessions fetches per tick.
// useWorkspacePolling() is the single owner of the interval.
export const workspaceQueryOptions = {
	queryKey: workspaceQueryKey,
	queryFn: ({ client }: { client: QueryClient }) => fetchWorkspaces(client),
	// Retrying before the daemon is up only parks the query in a 1s backoff.
	// Fetches started meanwhile (the ready-time invalidation, the shell's startup
	// fetch) join that backoff instead of running, which held the first
	// /projects + /sessions about 0.75s past "daemon listening" on cold launch.
	// Fail fast instead; the daemon status change refetches.
	retry: (failureCount: number, error: Error) => !(error instanceof DaemonNotReadyError) && failureCount < 1,
	staleTime: 10_000,
	structuralSharing: retainingStructuralSharing,
};

// While sessions are "checking" the daemon pushes `status_readiness_changed`
// over SSE when they settle, so this poll is only a safety net for a missed
// frame. Keep it slow: at 300 ms it refetched the whole session list ~3x/s.
export const WORKSPACE_CHECKING_POLL_MS = 2_000;

export const workspaceRefetchInterval = (query: Query<WorkspaceSummary[]>) =>
	workspaceStatusesChecking(query.state.data) ? WORKSPACE_CHECKING_POLL_MS : 15_000;

/**
 * Drives the workspace poll from one place. Mounted once by the shell; it never
 * re-renders its host (notifyOnChangeProps: []), it only keeps the timer.
 */
export function useWorkspacePolling(enabled = true) {
	useQuery({ ...workspaceQueryOptions, refetchInterval: workspaceRefetchInterval, notifyOnChangeProps: [], enabled });
}

type WorkspaceSubscriptionOptions = {
	subscribed?: boolean;
	enabled?: boolean;
};

export function useRemoteWorkspaces(options: WorkspaceSubscriptionOptions = {}) {
	const connected = useConnectedHosts();
	const projects = useQueries({
		queries: connected.map((hostId) => ({
			queryKey: remoteProjectsQueryKey(hostId),
			queryFn: () => fetchRemoteProjects(hostId),
			retry: 1,
			refetchInterval: 15_000,
			subscribed: options.subscribed,
		})),
	});
	const sessions = useQueries({
		queries: connected.map((hostId) => ({
			queryKey: remoteSessionsQueryKey(hostId),
			queryFn: ({ client }: { client: QueryClient }) => fetchRemoteSessions(client, hostId),
			retry: 1,
			refetchInterval: 15_000,
			subscribed: options.subscribed,
		})),
	});
	return {
		data: connected.flatMap((hostId, index) => projects[index]?.data
			? toRemoteWorkspaces(hostId, projects[index].data, sessions[index]?.data ?? { sessions: [], archivedByProject: {} })
			: []),
		loadedProjectHostIds: connected.filter((_, index) => projects[index]?.data !== undefined),
		loadedSessionHostIds: connected.filter((_, index) => sessions[index]?.data !== undefined),
		failedHostIds: connected.filter((_, index) => projects[index]?.isError || sessions[index]?.isError),
		refetch: () => Promise.all([...projects, ...sessions].map((query) => query.refetch())),
	};
}

/** A host-qualified project detail query for the board. It shares the remote
 * session query's key, so session mutations refresh both detail surfaces. */
export function useRemoteProjectQuery(hostId: string, projectId: string) {
	return useQuery({
		queryKey: remoteWorkspaceQueryKey(hostId),
		queryFn: ({ client }: { client: QueryClient }) => fetchRemoteWorkspaces(client, hostId),
		select: (workspaces) => workspaces.find((workspace) => workspace.id === projectId),
		enabled: Boolean(hostId && projectId),
		structuralSharing: retainingStructuralSharing,
		retry: 1,
	});
}


export function useWorkspaceQuery(options: WorkspaceSubscriptionOptions = {}) {
	return useQuery({ ...workspaceQueryOptions, subscribed: options.subscribed, enabled: options.enabled });
}

/**
 * Subscribe a detail surface to one session instead of the complete workspace
 * tree. TanStack Query applies structural sharing to the selected value, so an
 * activity update elsewhere no longer redraws the open session workspace.
 */
export function useWorkspaceSession(sessionId: string, hostId?: string, localLookupEnabledOption = true) {
	const queryClient = useQueryClient();
	// The flag gates the local lookup only; a remote session is always looked up.
	const localLookupEnabled = hostId ? true : localLookupEnabledOption;
	const selectSession = useMemo(
		() => (workspaces: WorkspaceSummary[]) => workspaces.flatMap((workspace) => workspace.sessions).find((session) => session.id === sessionId),
		[sessionId],
	);
	const remote = useQuery({
		queryKey: remoteWorkspaceQueryKey(hostId ?? ""),
		queryFn: ({ client }: { client: QueryClient }) => fetchRemoteWorkspaces(client, hostId ?? ""),
		select: selectSession,
		enabled: Boolean(hostId && sessionId),
		structuralSharing: retainingStructuralSharing,
	});
	const local = useQuery({ ...workspaceQueryOptions, select: selectSession, enabled: localLookupEnabled && !hostId });
	const listed = useQuery({ ...workspaceQueryOptions, subscribed: false, enabled: !hostId && localLookupEnabled && Boolean(sessionId) });
	// The live list holds no terminated sessions, so a session reached by URL,
	// notification or link (restore prompts, finished work) is read by id.
	const inList = hostId ? remote.data !== undefined : local.data !== undefined;
	const direct = useQuery({
		queryKey: hostId ? ["session", hostId, sessionId] : ["session", sessionId],
		enabled: localLookupEnabled && Boolean(sessionId) && !inList && (!hostId || remote.isSuccess),
		retry: (attempt, error) => apiErrorCode(error) === "SESSION_NOT_FOUND" && attempt < 4,
		retryDelay: 250,
		queryFn: async () => {
			const session = await fetchSessionView(hostId, sessionId);
			if (!session) return undefined;
			const workspaces = hostId ? queryClient.getQueryData<WorkspaceSummary[]>(workspaceQueryKeyForHost(hostId)) : listed.data;
			const project = session.projectId
				? (workspaces?.find((workspace) => workspace.id === session.projectId) ??
					({ id: session.projectId, name: "" } satisfies Pick<WorkspaceSummary, "id" | "name">))
				: ({
						id: STANDALONE_WORKSPACE_ID,
						name: standaloneWorkspaceName(),
					} satisfies Pick<WorkspaceSummary, "id" | "name">);
			const converted = toWorkspaceSession(session, project);
			return hostId ? { ...converted, hostId } : converted;
		},
	});
	const resolvedDirectSession = useMemo(() => {
		if (!direct.data) return undefined;
		const workspace = listed.data?.find((candidate) => candidate.id === direct.data?.workspaceId);
		if (!workspace || direct.data.workspaceName === workspace.name) return direct.data;
		return { ...direct.data, workspaceName: workspace.name };
	}, [direct.data, listed.data]);
	useEffect(() => {
		if (!localLookupEnabled) return;
		if (!resolvedDirectSession) return;
		retainWorkspaceSession(resolvedDirectSession.id, hostId);
		queryClient.setQueryData<WorkspaceSummary[]>(workspaceQueryKeyForHost(hostId), (current) => {
			if (!current) return current;
			let changed = false;
			const next = current.map((workspace) => {
				if (workspace.id !== resolvedDirectSession.workspaceId) return workspace;
				if (workspace.sessions.some((session) => session.id === resolvedDirectSession.id)) return workspace;
				changed = true;
				return {
					...workspace,
					sessions: [...workspace.sessions, resolvedDirectSession],
				};
			});
			return changed ? next : current;
		});
	}, [queryClient, resolvedDirectSession, hostId, localLookupEnabled]);
	const base = hostId ? remote : local;
	return {
		...base,
		data: base.data ?? resolvedDirectSession,
		isLoading: base.isLoading || direct.isLoading,
	};
}

/**
 * The kind of project a remote session belongs to. It reads the same host list
 * as useWorkspaceSession, from the same moment, so it adds no request.
 */
export function useRemoteSessionProjectKind(sessionId: string, hostId?: string): WorkspaceSummary["kind"] {
	const selectKind = useMemo(
		() => (workspaces: WorkspaceSummary[]) => workspaces.find((workspace) => workspace.sessions.some((session) => session.id === sessionId))?.kind,
		[sessionId],
	);
	return useQuery({
		queryKey: remoteWorkspaceQueryKey(hostId ?? ""),
		queryFn: ({ client }: { client: QueryClient }) => fetchRemoteWorkspaces(client, hostId ?? ""),
		select: selectKind,
		enabled: Boolean(hostId && sessionId),
		structuralSharing: retainingStructuralSharing,
	}).data;
}

export type WorkspaceScope = {
	project?: Pick<WorkspaceSummary, "id" | "kind" | "name" | "orchestratorAgent">;
	hasWorkerSessions: boolean;
	session?: WorkspaceSession;
	orchestrator?: WorkspaceSession;
};

function selectWorkspaceScope(
	workspaces: WorkspaceSummary[],
	projectId: string | undefined,
	sessionId: string | undefined,
): WorkspaceScope {
	const session = sessionId
		? workspaces.flatMap((workspace) => workspace.sessions).find((candidate) => candidate.id === sessionId)
		: undefined;
	const resolvedProjectId = session?.workspaceId ?? projectId;
	const workspace = resolvedProjectId ? workspaces.find((candidate) => candidate.id === resolvedProjectId) : undefined;
	// Do not carry the project's complete sessions array into shell chrome. With
	// React Query's structural sharing, this small metadata projection retains
	// its identity when another session in the same project streams an update.
	const project = workspace
		? {
				id: workspace.id,
				kind: workspace.kind,
				name: workspace.name,
				orchestratorAgent: workspace.orchestratorAgent,
			}
		: undefined;
	return {
		project,
		session,
		hasWorkerSessions: workspace ? workerSessions(workspace.sessions).length > 0 : false,
		orchestrator: workspace ? newestActiveOrchestrator(workspace.sessions) : undefined,
	};
}

/**
 * Subscribe shell chrome to just the routed project and session. This avoids
 * redrawing the topbar for streamed activity from every other project.
 */
export function useWorkspaceScope(projectId?: string, sessionId?: string, hostId?: string) {
	const selectLocalScope = useMemo(
		() => (workspaces: WorkspaceSummary[]) => selectWorkspaceScope(workspaces, projectId, sessionId),
		[projectId, sessionId],
	);
	const local = useQuery({ ...workspaceQueryOptions, select: selectLocalScope, enabled: !hostId });
	const remote = useQuery({ queryKey: remoteWorkspaceQueryKey(hostId ?? ""), queryFn: ({ client }: { client: QueryClient }) => fetchRemoteWorkspaces(client, hostId ?? ""), select: selectLocalScope, enabled: Boolean(hostId), structuralSharing: retainingStructuralSharing });
	return hostId ? remote : local;
}

export function selectTraySessions(workspaces: WorkspaceSummary[]): TraySessionEntry[] {
	const entries: TraySessionEntry[] = [];
	for (const workspace of workspaces) {
		for (const session of workerSessions(workspace.sessions)) {
			if (isWaitingOnOrchestrator(session)) continue; // the orchestrator is handling it
			const zone = attentionZone(session);
			if ((zone === "merge" && session.status === "merged") || (zone !== "action" && zone !== "merge")) continue;
			entries.push({
				projectId: session.workspaceId,
				projectName: workspace.name,
				sessionId: session.id,
				title: session.title,
				zone,
			});
		}
	}
	return entries;
}

/**
 * The tray lives for the whole app lifetime, but only attention-worthy worker
 * sessions affect its native payload. Select that compact projection at the
 * query boundary so ordinary streamed activity does not wake the runtime.
 */
export function useWorkspaceTraySessions() {
	const local = useQuery({
		...workspaceQueryOptions,
		select: selectTraySessions,
	});
	return local;
}
