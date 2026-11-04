import type { QueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { agentModelsQueryOptions } from "../hooks/useAgentModelsQuery";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { STANDALONE_WORKSPACE_ID, type WorkspaceSummary } from "../types/workspace";
import type { components } from "../../api/schema";

export function useScopedRouteSession({ queryClient, workspaces, remoteWorkspaces, routeParams }: {
	queryClient: QueryClient;
	workspaces: NoInfer<WorkspaceSummary[]>;
	remoteWorkspaces: WorkspaceSummary[];
	routeParams: { hostId?: string | undefined; projectId?: string | undefined; sessionId?: string | undefined; };
}) {
	// Project in scope for a new-session shortcut: the route's project, or the
	// workspace owning the open session (so the shortcut works from a worker's
	// detail view, where the URL carries only a sessionId).
	const scopedProjectId = routeParams.hostId ? undefined : routeParams.projectId
		? routeParams.projectId
		: routeParams.sessionId
			? workspaces.find((workspace) => workspace.sessions.some((session) => session.id === routeParams.sessionId))?.id
			: undefined;
	const scopedSession = routeParams.sessionId
		? (routeParams.hostId ? remoteWorkspaces : workspaces)
			.flatMap((workspace) => workspace.sessions)
			.find((session) => session.id === routeParams.sessionId)
		: undefined;
	useEffect(() => {
	}, [routeParams.hostId, scopedSession?.id, scopedSession?.kind]);
	// Warms the New Task composer's model-catalog cache while the user is just
	// looking at the project, so the picker never shows a loading flash the
	// first time they actually open the dialog.
	useEffect(() => {
		if (!scopedProjectId || scopedProjectId === STANDALONE_WORKSPACE_ID) return;
		const projectQueryKey = ["project", scopedProjectId];
		void queryClient
			.prefetchQuery({
				queryKey: projectQueryKey,
				queryFn: async () => {
					const { data, error: apiError } = await apiClient.GET("/api/v1/projects/{id}", {
						params: { path: { id: scopedProjectId } },
					});
					if (apiError) throw new Error(apiErrorMessage(apiError));
					if (data?.status !== "ok") throw new Error("Project config unavailable");
					return data.project as components["schemas"]["Project"];
				},
			})
			.then(() => {
				const project = queryClient.getQueryData<components["schemas"]["Project"]>(projectQueryKey);
				const defaultWorkerAgent = project?.config?.worker?.agent || project?.agent || "";
				if (defaultWorkerAgent) {
					void queryClient.prefetchQuery(agentModelsQueryOptions(defaultWorkerAgent, ""));
				}
			});
	}, [queryClient, scopedProjectId]);
	return { scopedProjectId };
}
