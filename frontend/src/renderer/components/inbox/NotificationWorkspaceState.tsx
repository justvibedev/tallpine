import { useQueries } from "@tanstack/react-query";
import { useCallback, useMemo, type ReactNode } from "react";
import { fetchSessionView, useRemoteWorkspaces, useWorkspaceQuery } from "../../hooks/useWorkspaceQuery";
import { STANDALONE_WORKSPACE_ID } from "../../types/workspace";
import type { WorkspaceSummary } from "../../types/workspace";
import { LOCAL_HOST, sessionUiKey, type HostId } from "../../lib/hosts";

export type SessionMeta = { projectId: string; projectName: string; sessionName: string };

export function useSessionTerminationLookup(
	workspaces: WorkspaceSummary[] | undefined,
	isError: boolean,
	isSuccess: boolean,
	refetch: () => void,
): {
	retryWorkspace: () => void;
	sessionsReady: boolean;
	terminatedIds: Set<string>;
	workspaceError: boolean;
} {
	const terminatedIds = useMemo(() => {
		const ids = new Set<string>();
		for (const workspace of workspaces ?? []) {
			for (const session of workspace.sessions) {
				if (session.isTerminated === true || session.status === "terminated") {
					ids.add(sessionUiKey(session.id, workspace.hostId));
				}
			}
		}
		return ids;
	}, [workspaces]);
	// Only successful workspace data is trustworthy. Pending and error both leave
	// sessions non-navigable — a failed query must not treat terminated rows as live.
	return {
		retryWorkspace: refetch,
		sessionsReady: isSuccess,
		terminatedIds,
		workspaceError: isError,
	};
}

/**
 * Keeps the broad workspace observer inside the open Inbox list, so streamed
 * workspace activity cannot wake the always-mounted shell while the list is closed.
 */
export function NotificationWorkspaceState({
	children,
	sessionRefs,
}: {
	/** Sessions named by the visible rows; those the live lists omit are read by id. */
	sessionRefs: Array<{ sessionId: string; hostId: HostId }>;
	children: (state: {
		retryWorkspace: () => void;
		loadedRemoteSessionIds: Set<string>;
		sessionMeta: Map<string, SessionMeta>;
		sessionsReady: boolean;
		lookupPendingIds: Set<string>;
		terminatedIds: Set<string>;
		workspaceError: boolean;
	}) => ReactNode;
}) {
	const workspaceQuery = useWorkspaceQuery();
	const remoteWorkspaces = useRemoteWorkspaces();
	const retryWorkspace = useCallback(() => {
		void workspaceQuery.refetch();
		void remoteWorkspaces.refetch();
	}, [remoteWorkspaces.refetch, workspaceQuery.refetch]);
	const { sessionsReady, terminatedIds: listedTerminatedIds, workspaceError } = useSessionTerminationLookup(
		[...(workspaceQuery.data ?? []), ...remoteWorkspaces.data],
		workspaceQuery.isError,
		workspaceQuery.isSuccess,
		retryWorkspace,
	);
	// The live lists hold no terminated sessions, so a notification about a
	// finished one (a restore prompt, a merged PR) would otherwise name nothing.
	const listedKeys = useMemo(() => {
		const keys = new Set<string>();
		for (const workspace of [...(workspaceQuery.data ?? []), ...remoteWorkspaces.data]) {
			for (const session of workspace.sessions) keys.add(sessionUiKey(session.id, workspace.hostId));
		}
		return keys;
	}, [remoteWorkspaces.data, workspaceQuery.data]);
	const missingRefs = useMemo(() => {
		const seen = new Set<string>();
		return sessionRefs.filter(({ sessionId, hostId }) => {
			const key = sessionUiKey(sessionId, hostId === LOCAL_HOST ? undefined : hostId);
			if (listedKeys.has(key) || seen.has(key)) return false;
			seen.add(key);
			return true;
		});
	}, [listedKeys, sessionRefs]);
	const lookups = useQueries({
		queries: missingRefs.map(({ sessionId, hostId }) => ({
			queryKey: ["session-lookup", hostId, sessionId] as const,
			queryFn: async () => (await fetchSessionView(hostId === LOCAL_HOST ? undefined : hostId, sessionId)) ?? null,
			staleTime: 60_000,
			retry: false,
			enabled: workspaceQuery.isSuccess,
		})),
	});
	const lookupState = useMemo(() => {
		const found = new Map<string, { projectId: string; session: NonNullable<(typeof lookups)[number]["data"]> }>();
		const pending = new Set<string>();
		missingRefs.forEach(({ sessionId, hostId }, index) => {
			const key = sessionUiKey(sessionId, hostId === LOCAL_HOST ? undefined : hostId);
			const lookup = lookups[index];
			if (lookup?.data) found.set(key, { projectId: lookup.data.projectId || STANDALONE_WORKSPACE_ID, session: lookup.data });
			else if (lookup?.isPending) pending.add(key);
		});
		return { found, pending };
	}, [lookups, missingRefs]);
	const terminatedIds = useMemo(() => {
		const ids = new Set(listedTerminatedIds);
		for (const [key, { session }] of lookupState.found) {
			if (session.isTerminated === true || session.status === "terminated") ids.add(key);
		}
		return ids;
	}, [listedTerminatedIds, lookupState.found]);
	const sessionMeta = useMemo(() => {
		const map = new Map<string, SessionMeta>();
		const projectNames = new Map<string, string>();
		for (const workspace of workspaceQuery.data ?? []) projectNames.set(workspace.id, workspace.name);
		for (const [key, { projectId, session }] of lookupState.found) {
			map.set(key, { projectId, projectName: projectNames.get(projectId) ?? "", sessionName: session.displayName ?? session.issueId ?? session.id });
		}
		for (const workspace of workspaceQuery.data ?? []) {
			for (const session of workspace.sessions) {
				map.set(sessionUiKey(session.id, workspace.hostId), { projectId: workspace.id, projectName: workspace.name, sessionName: session.title });
			}
		}
		for (const workspace of remoteWorkspaces.data) {
			for (const session of workspace.sessions) {
				map.set(sessionUiKey(session.id, workspace.hostId), { projectId: workspace.id, projectName: workspace.name, sessionName: session.title });
			}
		}
		return map;
	}, [lookupState.found, remoteWorkspaces.data, workspaceQuery.data]);
	const loadedRemoteSessionIds = useMemo(() => {
		const ids = new Set<string>(lookupState.found.keys());
		for (const workspace of remoteWorkspaces.data) {
			if (!workspace.hostId || !remoteWorkspaces.loadedSessionHostIds.includes(workspace.hostId) || remoteWorkspaces.failedHostIds.includes(workspace.hostId)) continue;
			for (const session of workspace.sessions) {
				ids.add(sessionUiKey(session.id, workspace.hostId));
			}
		}
		return ids;
	}, [lookupState.found, remoteWorkspaces.data, remoteWorkspaces.failedHostIds, remoteWorkspaces.loadedSessionHostIds]);
	return <>{children({ retryWorkspace, loadedRemoteSessionIds, lookupPendingIds: lookupState.pending, sessionMeta, sessionsReady, terminatedIds, workspaceError: workspaceError || remoteWorkspaces.failedHostIds.length > 0 })}</>;
}
