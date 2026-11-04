import { useInfiniteQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { useMemo } from "react";
import { LOCAL_HOST } from "../lib/hosts";
import { apiClient } from "../lib/api-client";
import { clientForHost } from "../lib/host-clients";
import {
	STANDALONE_WORKSPACE_ID,
	type WorkspaceSession,
	type WorkspaceSummary,
} from "../types/workspace";
import { toWorkspaceSession, workspaceQueryKeyForHost } from "./useWorkspaceQuery";
import { appI18n } from "../i18n";

/** Terminated sessions per request; the daemon's cap is 200. */
export const ARCHIVE_PAGE_SIZE = 50;

export const archivedSessionsQueryRoot = ["archived-sessions"] as const;

export type ArchivedSessionsScope = {
	/** One project's archive. Omit for every project. */
	projectId?: string;
	/** Only sessions that belong to no project (the standalone archive). */
	standalone?: boolean;
};

export const archivedSessionsQueryKey = (scope: ArchivedSessionsScope, hostId?: string) =>
	[...archivedSessionsQueryRoot, hostId ?? LOCAL_HOST, scope.standalone ? "standalone" : (scope.projectId ?? "all")] as const;

/** Refresh every loaded archive page, e.g. after a session is archived or restored. */
export function invalidateArchivedSessions(queryClient: QueryClient, hostId?: string): Promise<void> {
	return queryClient.invalidateQueries({ queryKey: [...archivedSessionsQueryRoot, hostId ?? LOCAL_HOST] });
}

type ArchivePage = { sessions: WorkspaceSession[]; nextCursor?: string };

/**
 * The terminated worker sessions the workspace query no longer lists, one page at
 * a time. Nothing is fetched until `enabled` (the Archive panel opening, a
 * palette search), so a long history costs nothing while it is not looked at.
 */
export function useArchivedSessions(scope: ArchivedSessionsScope, options: { hostId?: string; enabled?: boolean } = {}) {
	const { hostId, enabled = true } = options;
	const queryClient = useQueryClient();
	const query = useInfiniteQuery({
		queryKey: archivedSessionsQueryKey(scope, hostId),
		initialPageParam: undefined as string | undefined,
		enabled,
		retry: 1,
		staleTime: 30_000,
		queryFn: async ({ pageParam }): Promise<ArchivePage> => {
			const api = hostId ? clientForHost(hostId) : apiClient;
			const { data, error } = await api.GET("/api/v1/sessions", {
				params: {
					query: {
						active: false,
						kind: "worker",
						limit: ARCHIVE_PAGE_SIZE,
						...(pageParam ? { cursor: pageParam } : {}),
						...(scope.standalone ? { standalone: true } : scope.projectId ? { project: scope.projectId } : {}),
					},
				},
			});
			if (error) throw error;
			const workspaces = queryClient.getQueryData<WorkspaceSummary[]>(workspaceQueryKeyForHost(hostId));
			return {
				nextCursor: data?.nextCursor,
				sessions: (data?.sessions ?? []).map((session) => {
					const project = session.projectId
						? (workspaces?.find((workspace) => workspace.id === session.projectId) ?? { id: session.projectId, name: "" })
						: { id: STANDALONE_WORKSPACE_ID, name: appI18n.t("standalone.workspaceName") };
					const converted = toWorkspaceSession(session, project);
					return hostId ? { ...converted, hostId } : converted;
				}),
			};
		},
		getNextPageParam: (last) => last.nextCursor,
	});
	const sessions = useMemo(() => {
		const seen = new Set<string>();
		return (query.data?.pages ?? []).flatMap((page) => page.sessions).filter((session) => {
			if (seen.has(session.id)) return false;
			seen.add(session.id);
			return true;
		});
	}, [query.data]);
	return {
		sessions,
		hasNextPage: query.hasNextPage,
		fetchNextPage: query.fetchNextPage,
		isFetching: query.isFetching,
		isFetchingNextPage: query.isFetchingNextPage,
		isError: query.isError,
		isSuccess: query.isSuccess,
	};
}
