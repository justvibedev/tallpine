import { PRODUCT_NAME } from "../../shared/brand";
import type { UseNavigateResult } from "@tanstack/react-router";
import type { QueryClient } from "@tanstack/react-query";
import type { GlobalToastOptions } from "../stores/ui-store";
import { useCallback } from "react";
import { workspaceQueryKey, workspaceQueryOptions } from "../hooks/useWorkspaceQuery";
import { apiClient, apiErrorCode, apiErrorDetails, apiErrorMessage, apiErrorRequestId } from "../lib/api-client";
import { refreshDaemonStatus } from "../lib/daemon-status";
import { type WorkspaceSummary } from "../types/workspace";
import type { components } from "../../api/schema";
import { findRegisteredWorkspaceByPath, CreateProjectConfigInput, createProjectConfig } from "./projectConfig";

export function useLocalProjectActions({ navigate, queryClient, workspacesRef, setOrchestratorStartupError, setProjectProvisioning, showGlobalToast, provisionOrchestrator, completeProjectCreation }: {
	navigate: UseNavigateResult<string>;
	queryClient: QueryClient;
	workspacesRef: React.RefObject<WorkspaceSummary[]>;
	setOrchestratorStartupError: (projectId: string, message: string | null, hostId?: string | undefined) => void;
	setProjectProvisioning: (projectId: string, provisioning: boolean, hostId?: string | undefined) => void;
	showGlobalToast: (title: string, body?: string | undefined, style?: "error" | "info" | "bottom-right" | "top-center" | GlobalToastOptions | undefined) => void;
	provisionOrchestrator: (workspace: WorkspaceSummary, input: CreateProjectConfigInput, _source: "project_add" | "project_clone") => Promise<void>;
	completeProjectCreation: (project: components["schemas"]["Project"], input: CreateProjectConfigInput, source: "project_add" | "project_clone") => Promise<void>;
}) {
	const createProject = useCallback(
		async (input: {
			path: string;
			workerAgent: string;
			orchestratorAgent: string;
			trackerIntake?: components["schemas"]["TrackerIntakeConfig"];
			asWorkspace?: boolean;
			clonePreparationId?: string;
		}) => {
			const status = await refreshDaemonStatus();
			if (status.state !== "ready" || !status.port) {
				throw new Error(status.message || `${PRODUCT_NAME} daemon is not ready.`);
			}
			const { data, error } = await apiClient.POST("/api/v1/projects", {
				body: {
					path: input.path,
					asWorkspace: input.asWorkspace || undefined,
					clonePreparationId: input.clonePreparationId,
					config: createProjectConfig(input),
				},
			}).catch((requestError: unknown) => {
				throw requestError;
			});
			if (error) {
				const failure = new Error(apiErrorMessage(error)) as Error & {
					code?: string;
					details?: Record<string, unknown>;
					requestId?: string;
				};
				failure.code = apiErrorCode(error);
				failure.details = apiErrorDetails(error);
				failure.requestId = apiErrorRequestId(error);
				if (failure.code === "PATH_ALREADY_REGISTERED") {
					const existingProjectId = failure.details?.existingProjectId;
					const findRegisteredWorkspace = (items: WorkspaceSummary[]) => {
						const local = items;
						// The daemon resolves symlinks/canonical paths. Prefer its identity
						// over renderer path spelling, but only open a known local project.
						return typeof existingProjectId === "string" && existingProjectId !== ""
							? local.find((workspace) => workspace.id === existingProjectId)
							: findRegisteredWorkspaceByPath(local, input.path);
					};
					let registeredWorkspace = findRegisteredWorkspace(
						queryClient.getQueryData<WorkspaceSummary[]>(workspaceQueryKey) ?? workspacesRef.current,
					);
					if (!registeredWorkspace) {
						try {
							registeredWorkspace = findRegisteredWorkspace(await queryClient.fetchQuery({
								...workspaceQueryOptions, staleTime: 0, retry: false,
							}));
						} catch {
							// Keep the original conflict and selection if refresh is unavailable.
						}
					}
					if (registeredWorkspace) {
						if (input.clonePreparationId) {
							setOrchestratorStartupError(registeredWorkspace.id, null);
							setProjectProvisioning(registeredWorkspace.id, true);
							await navigate({ to: "/projects/$projectId", params: { projectId: registeredWorkspace.id } });
							// Registration may have committed before its response was lost.
							// The daemon reuses an active orchestrator under its project lock.
							void provisionOrchestrator(registeredWorkspace, input, "project_add");
							return;
						}
						showGlobalToast("Project already added", "Opened the registered project for this folder.");
						void navigate({ to: "/projects/$projectId", params: { projectId: registeredWorkspace.id } });
						return;
					}
				}
				throw failure;
			}
			if (!data?.project) {
				throw new Error("Project creation returned no project");
			}
			await completeProjectCreation(data.project, input, "project_add");
		},
		[completeProjectCreation, navigate, provisionOrchestrator, queryClient, setOrchestratorStartupError, setProjectProvisioning, showGlobalToast],
	);

	const cloneProject = useCallback(
		async (input: {
			remoteUrl: string;
			destinationParent: string;
			workerAgent: string;
			orchestratorAgent: string;
			trackerIntake?: components["schemas"]["TrackerIntakeConfig"];
			signal?: AbortSignal;
		}) => {
			const reportFailure = (_code: string | undefined) => {
			};
			const status = await refreshDaemonStatus();
			if (status.state !== "ready" || !status.port) {
				throw new Error(status.message || `${PRODUCT_NAME} daemon is not ready.`);
			}
			const { data, error } = await apiClient.POST("/api/v1/projects/clone", {
				signal: input.signal,
				body: {
					remoteUrl: input.remoteUrl,
					destinationParent: input.destinationParent,
					config: createProjectConfig(input),
				},
			}).catch((requestError: unknown) => {
				// A clone the user cancelled is not a failure.
				if (!input.signal?.aborted) reportFailure("REQUEST_FAILED");
				throw requestError;
			});
			if (error) {
				const failure = new Error(apiErrorMessage(error)) as Error & {
					code?: string;
					details?: Record<string, unknown>;
					requestId?: string;
				};
				failure.code = apiErrorCode(error);
				failure.details = apiErrorDetails(error);
				failure.requestId = apiErrorRequestId(error);
				throw failure;
			}
			if (!data?.project) {
				throw new Error("Project clone returned no project");
			}
			await completeProjectCreation(data.project, input, "project_clone");
		},
		[completeProjectCreation],
	);

	const initializeProjectRepository = useCallback(async (path: string) => {
		const { error } = await apiClient.POST("/api/v1/projects/initialize", {
			body: { path },
		});
		if (error) {
			const failure = new Error(apiErrorMessage(error)) as Error & { code?: string };
			failure.code = apiErrorCode(error);
			throw failure;
		}
	}, []);
	return { createProject, cloneProject, initializeProjectRepository };
}
