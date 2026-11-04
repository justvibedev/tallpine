import type { UseNavigateResult } from "@tanstack/react-router";
import type { QueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import { workspaceQueryKey, toWorkspaceSession } from "../hooks/useWorkspaceQuery";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { useUiStore } from "../stores/ui-store";
import { toProjectKind, type WorkspaceSummary } from "../types/workspace";
import type { components } from "../../api/schema";
import { useSidebarOrderStore } from "../stores/sidebar-order-store";
import { CreateProjectConfigInput, PROVISIONING_TIMEOUT_MS } from "./projectConfig";

export function useProjectProvisioning({ navigate, queryClient, routeParamsRef, setOrchestratorStartupError, setProjectProvisioning }: {
	navigate: UseNavigateResult<string>;
	queryClient: QueryClient;
	routeParamsRef: React.RefObject<{ hostId?: string | undefined; projectId?: string | undefined; sessionId?: string | undefined; }>;
	setOrchestratorStartupError: (projectId: string, message: string | null, hostId?: string | undefined) => void;
	setProjectProvisioning: (projectId: string, provisioning: boolean, hostId?: string | undefined) => void;
}) {
	const updateWorkspaces = useCallback(
		(updater: (workspaces: WorkspaceSummary[]) => WorkspaceSummary[]) => {
			queryClient.setQueryData<WorkspaceSummary[]>(workspaceQueryKey, (current = []) => updater(current));
		},
		[queryClient],
	);

	// Background orchestrator provisioning for a newly created project. Owns
	// the provisioning flag, the hung-spawn timeout, the session refresh, and
	// the retry error end to end — callers must fire and forget it, never await.
	const provisionOrchestrator = useCallback(
		async (
			workspace: WorkspaceSummary,
			input: CreateProjectConfigInput,
			_source: "project_add" | "project_clone",
		) => {
		// Safety: a hung spawn must never wedge the board behind the
		// provisioning gate. If it outlives this budget, release the gate and
		// surface the retry banner; a late success still navigates below and
		// the board clears the banner once the orchestrator appears.
		const provisioningGuard = window.setTimeout(() => {
			if (!useUiStore.getState().provisioningProjectIds.has(workspace.id)) return;
			setProjectProvisioning(workspace.id, false);
			setOrchestratorStartupError(
				workspace.id,
				"Project added, but the orchestrator is taking longer than expected to start. Retry from the board if it does not appear.",
			);
		}, PROVISIONING_TIMEOUT_MS);
		try {
			const {
				data: spawnData,
				error: spawnError,
				response: spawnResponse,
			} = await apiClient.POST("/api/v1/sessions", {
				body: {
					projectId: workspace.id,
					kind: "orchestrator",
					harness: input.orchestratorAgent as components["schemas"]["SpawnSessionRequest"]["harness"],
				},
			});
			if (spawnError || !spawnData?.session?.id) {
				const message = spawnError
					? apiErrorMessage(spawnError, `Failed to spawn orchestrator (${spawnResponse.status})`)
					: `Failed to spawn orchestrator (${spawnResponse.status})`;
				throw new Error(message);
			}
			const sessionId = spawnData.session.id;
			// Publish before navigating so Chat does not wait for a list refresh.
			await queryClient.cancelQueries({ queryKey: workspaceQueryKey });
			queryClient.setQueryData<WorkspaceSummary[]>(workspaceQueryKey, (current = []) =>
				current.map((item) => item.id === workspace.id
					? { ...item, sessions: [toWorkspaceSession(spawnData.session, { id: workspace.id, name: workspace.name }), ...item.sessions.filter((session) => session.id !== sessionId)] }
					: item),
			);
			// The session now exists, so the sidebar button can open it even if
			// the user left while it spawned.
			window.clearTimeout(provisioningGuard);
			setProjectProvisioning(workspace.id, false);
			// Follow the new orchestrator only if the user is still waiting on
			// this project's board; do not pull them back from another page.
			const current = routeParamsRef.current;
			if (current.projectId === workspace.id && !current.sessionId && !current.hostId) {
				void navigate({
					to: "/projects/$projectId/sessions/$sessionId",
					params: { projectId: workspace.id, sessionId },
				});
			}
		} catch (spawnError) {
			window.clearTimeout(provisioningGuard);
			setProjectProvisioning(workspace.id, false);
			const message = spawnError instanceof Error ? spawnError.message : "Could not start orchestrator";
			const startupMessage = `Project added, but orchestrator did not start: ${message}`;
			setOrchestratorStartupError(workspace.id, startupMessage);
		}
	},
	[navigate, queryClient, setOrchestratorStartupError, setProjectProvisioning],
);

	const completeProjectCreation = useCallback(
		async (
			project: components["schemas"]["Project"],
			input: CreateProjectConfigInput,
			source: "project_add" | "project_clone",
		) => {
			const workspace: WorkspaceSummary = {
				id: project.id,
				name: project.name,
				kind: toProjectKind(project.kind),
				path: project.path,
				workspaceRepos: project.workspaceRepos,
				type: "main",
				orchestratorAgent: input.orchestratorAgent as WorkspaceSummary["orchestratorAgent"],
				sessions: [],
			};
			// A pre-create list request must not erase the newly registered project.
			await queryClient.cancelQueries({ queryKey: workspaceQueryKey });
			useSidebarOrderStore.getState().moveProjectToTop(workspace.id);
			updateWorkspaces((current) => [workspace, ...current.filter((item) => item.id !== workspace.id)]);
			setOrchestratorStartupError(workspace.id, null);
			setProjectProvisioning(workspace.id, true);
			await navigate({ to: "/projects/$projectId", params: { projectId: workspace.id } });
			void provisionOrchestrator(workspace, input, source);
		},
		[navigate, provisionOrchestrator, queryClient, setOrchestratorStartupError, setProjectProvisioning, updateWorkspaces],
	);
	return { updateWorkspaces, provisionOrchestrator, completeProjectCreation };
}
