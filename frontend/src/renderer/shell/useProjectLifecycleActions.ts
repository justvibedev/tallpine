import type { UseNavigateResult } from "@tanstack/react-router";
import type { QueryClient } from "@tanstack/react-query";
import type { TFunction } from "i18next";
import type { RemoteHost } from "../hooks/useRemoteHosts";
import type { ProjectSettingsSection } from "../stores/settings-modal";
import type { OrchestratorReplacementFailure } from "../stores/ui-store";
import type { GlobalToastOptions } from "../stores/ui-store";
import { useCallback } from "react";
import type { CreateProjectInput } from "../components/CreateProjectFlow";
import { apiClient, apiErrorCode, apiErrorDetails, apiErrorMessage } from "../lib/api-client";
import { restartProjectOrchestrator } from "../lib/restart-orchestrator";
import { useUiStore } from "../stores/ui-store";
import { hasConfiguredOrchestratorAgent, newestActiveOrchestrator, type WorkspaceSummary } from "../types/workspace";
import { remoteWorkspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { clientForHost } from "../lib/host-clients";
import { openRemoteOrchestrator } from "../lib/remote-orchestrator";
import { projectNavigateTarget, sessionNavigateTarget } from "../lib/navigate-to-session";
import { sessionUiKey } from "../lib/hosts";
import { createProjectConfig, PROVISIONING_TIMEOUT_MS } from "./projectConfig";

export function useProjectLifecycleActions({ t, navigate, queryClient, workspaces, remoteHosts, remoteWorkspaces, openProjectSettings, routeParams, setProjectRestarting, setOrchestratorReplacementError, setOrchestratorStartupError, setProjectProvisioning, showGlobalToast, updateWorkspaces }: {
	t: TFunction<"translation", undefined>;
	navigate: UseNavigateResult<string>;
	queryClient: QueryClient;
	workspaces: NoInfer<WorkspaceSummary[]>;
	remoteHosts: RemoteHost[];
	remoteWorkspaces: WorkspaceSummary[];
	openProjectSettings: (projectId: string, options?: string | { section?: ProjectSettingsSection | undefined; } | undefined) => void;
	routeParams: { hostId?: string | undefined; projectId?: string | undefined; sessionId?: string | undefined; };
	setProjectRestarting: (projectId: string, restarting: boolean, hostId?: string | undefined) => void;
	setOrchestratorReplacementError: (projectId: string, failure: OrchestratorReplacementFailure | null) => void;
	setOrchestratorStartupError: (projectId: string, message: string | null, hostId?: string | undefined) => void;
	setProjectProvisioning: (projectId: string, provisioning: boolean, hostId?: string | undefined) => void;
	showGlobalToast: (title: string, body?: string | undefined, style?: "error" | "info" | "bottom-right" | "top-center" | GlobalToastOptions | undefined) => void;
	updateWorkspaces: (updater: (workspaces: WorkspaceSummary[]) => WorkspaceSummary[]) => void;
}) {
	const createRemoteProject = useCallback(async (hostId: string, input: CreateProjectInput) => {
		const host = remoteHosts.find((candidate) => candidate.hostId === hostId);
		if (host?.status !== "connected") throw new Error(`Connect to ${host?.label ?? hostId} before adding a project.`);
		const client = clientForHost(hostId);
		const { data, error } = await client.POST("/api/v1/projects", { body: {
			path: input.path,
			asWorkspace: input.asWorkspace || undefined,
			clonePreparationId: input.clonePreparationId,
			config: createProjectConfig(input),
		} });
		let projectId = error ? undefined : data?.project.id;
		if (input.clonePreparationId && apiErrorCode(error) === "PATH_ALREADY_REGISTERED") {
			const existingProjectId = apiErrorDetails(error)?.existingProjectId;
			if (typeof existingProjectId === "string" && existingProjectId !== "") {
				const existing = await client.GET("/api/v1/projects/{id}", { params: { path: { id: existingProjectId } } });
				if (!existing.error && existing.data?.status === "ok" && existing.data.project.id === existingProjectId) projectId = existingProjectId;
			}
		}
		if (!projectId) throw Object.assign(new Error(apiErrorMessage(error, "Could not add project on this host.")), { code: apiErrorCode(error) });
		const showProject = (sessionId?: string) => {
			void queryClient.invalidateQueries({ queryKey: remoteWorkspaceQueryKey(hostId) });
			void navigate(sessionId ? sessionNavigateTarget(projectId, sessionId, hostId) : projectNavigateTarget(projectId, hostId));
		};
		showProject();
		setOrchestratorStartupError(projectId, null, hostId);
		setProjectProvisioning(projectId, true, hostId);
		const provisioningGuard = window.setTimeout(() => {
			setProjectProvisioning(projectId, false, hostId);
			setOrchestratorStartupError(projectId, "Project added, but orchestrator startup timed out. Try starting it again.", hostId);
		}, PROVISIONING_TIMEOUT_MS);
		void client.POST("/api/v1/orchestrators", { body: { projectId } }).then(({ data, error }) => {
			if (error || !data?.orchestrator?.id) throw new Error(apiErrorMessage(error, "Could not start the orchestrator."));
			window.clearTimeout(provisioningGuard);
			setProjectProvisioning(projectId, false, hostId);
			setOrchestratorStartupError(projectId, null, hostId);
			showProject(data.orchestrator.id);
		}).catch((cause) => {
			window.clearTimeout(provisioningGuard);
			setProjectProvisioning(projectId, false, hostId);
			const message = cause instanceof Error ? cause.message : "Try starting it from project settings.";
			setOrchestratorStartupError(projectId, `Project added, but orchestrator did not start: ${message}`, hostId);
			showGlobalToast("Orchestrator did not start", message, "error");
		});
	}, [navigate, queryClient, remoteHosts, setOrchestratorStartupError, setProjectProvisioning, showGlobalToast]);
	const initializeRemoteProject = useCallback(async (hostId: string, path: string) => {
		const { error } = await clientForHost(hostId).POST("/api/v1/projects/initialize", { body: { path } });
		if (error) throw Object.assign(new Error(apiErrorMessage(error)), { code: apiErrorCode(error) });
	}, []);

	const validateImport = useCallback(
		async (input: { path: string; importKind: "project" | "workspace" }) => {
			const { data, error } = await apiClient.POST("/api/v1/imports/validate", { body: input });
			if (error) throw new Error(apiErrorMessage(error));
			if (!data) throw new Error("Import validation returned no result");
			return data;
		},
		[],
	);

	const removeProject = useCallback(
		async (projectId: string) => {
			const isLastWorkspace =
              workspaces.length === 1 && workspaces[0]?.id === projectId;
			const { error } = await apiClient.DELETE("/api/v1/projects/{id}", {
				params: { path: { id: projectId } },
			});
			if (error) {
				const failure = new Error(apiErrorMessage(error)) as Error & { code?: string };
				failure.code = apiErrorCode(error);
				throw failure;
			}
			updateWorkspaces((current) => current.filter((item) => item.id !== projectId));
			if (isLastWorkspace) {
              void navigate({ to: "/" });
}
		},
		[navigate, updateWorkspaces, workspaces],
	);
	const removeRemoteProject = useCallback(async (hostId: string, projectId: string) => {
		const selectedProject = routeParams.hostId === hostId && (
			routeParams.projectId === projectId || remoteWorkspaces.some((workspace) =>
				workspace.hostId === hostId && workspace.id === projectId &&
				workspace.sessions.some((session) => session.id === routeParams.sessionId),
			)
		);
		const { error } = await clientForHost(hostId).DELETE("/api/v1/projects/{id}", {
			params: { path: { id: projectId } },
		});
		if (error) throw new Error(apiErrorMessage(error));
		void queryClient.invalidateQueries({ queryKey: remoteWorkspaceQueryKey(hostId) });
		if (selectedProject) void navigate({ to: "/" });
	}, [navigate, queryClient, remoteWorkspaces, routeParams.hostId, routeParams.projectId, routeParams.sessionId]);
	const openRemoteProjectOrchestrator = useCallback(async (hostId: string, projectId: string) => {
		const workspace = remoteWorkspaces.find((item) => item.hostId === hostId && item.id === projectId);
		if (!workspace) return;
		const projectKey = sessionUiKey(projectId, hostId);
		const uiState = useUiStore.getState();
		if (uiState.provisioningProjectIds.has(projectKey) || uiState.restartingProjectIds.has(projectKey)) return;
		if (!hasConfiguredOrchestratorAgent(workspace)) {
			openProjectSettings(projectId, hostId);
			return;
		}
		setOrchestratorStartupError(projectId, null, hostId);
		try {
			const sessionId = await openRemoteOrchestrator(hostId, projectId, newestActiveOrchestrator(workspace.sessions));
			await queryClient.invalidateQueries({ queryKey: remoteWorkspaceQueryKey(hostId) });
			void navigate(sessionNavigateTarget(projectId, sessionId, hostId));
		} catch (cause) {
			const message = cause instanceof Error ? cause.message : t("shell.couldNotSpawn");
			setOrchestratorStartupError(projectId, message, hostId);
			showGlobalToast(t("shell.couldNotSpawn"), message, "error");
		}
	}, [navigate, openProjectSettings, queryClient, remoteWorkspaces, setOrchestratorStartupError, showGlobalToast, t]);

	const restartOrchestrator = useCallback(
		async (projectId: string, mode?: "chat" | "tui", approvalMode?: "bypass-permissions") => {
			await restartProjectOrchestrator({
				projectId,
				queryClient,
				navigate,
				setProjectRestarting,
				setOrchestratorReplacementError,
				mode,
				approvalMode,
				onError: (_error) => {
				},
			});
		},
		[navigate, queryClient, setOrchestratorReplacementError, setProjectRestarting],
	);
	return { createRemoteProject, initializeRemoteProject, validateImport, removeProject, removeRemoteProject, openRemoteProjectOrchestrator, restartOrchestrator };
}
