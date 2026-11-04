/* Project settings HTTP calls, split out of the project settings components (B298, P57). */

import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "./api-client";
import { clientForHost } from "./host-clients";

export type Project = components["schemas"]["Project"];
export type ProjectConfig = components["schemas"]["ProjectConfig"];

export const projectQueryKey = (id: string, hostId?: string) =>
	hostId ? (["project", hostId, id] as const) : (["project", id] as const);

const clientFor = (hostId?: string) => (hostId ? clientForHost(hostId) : apiClient);

/** Load one project; `degradedMessage` is the error for a degraded manifest. */
export async function fetchProject(projectId: string, degradedMessage: string, hostId?: string): Promise<Project> {
	const { data, error } = await clientFor(hostId).GET("/api/v1/projects/{id}", { params: { path: { id: projectId } } });
	if (error) throw new Error(apiErrorMessage(error));
	if (data?.status !== "ok") throw new Error(degradedMessage);
	return data.project as Project;
}

/** Replace a project's whole config (the API is a whole-config PUT). */
export async function putProject(projectId: string, displayName: string, config: ProjectConfig, hostId?: string): Promise<void> {
	const { error } = await clientFor(hostId).PUT("/api/v1/projects/{id}", {
		params: { path: { id: projectId } },
		body: { displayName, config },
	});
	if (error) throw new Error(apiErrorMessage(error));
}

/**
 * Re-reads the project's latest config and writes back `change(config)`, so a
 * whole-config PUT cannot erase changes made elsewhere.
 */
export async function patchProjectConfig(
	projectId: string,
	change: (config: ProjectConfig) => ProjectConfig,
	degradedMessage = "project config unavailable",
): Promise<void> {
	const current = await apiClient.GET("/api/v1/projects/{id}", { params: { path: { id: projectId } } });
	if (current.error) throw new Error(apiErrorMessage(current.error));
	if (current.data?.status !== "ok" || !current.data.project) throw new Error(degradedMessage);
	const latest = current.data.project as Project;
	await putProject(projectId, latest.name, change({ ...latest.config }));
}

/** Retry cleanup of a project's terminated sessions. */
export async function cleanupProjectSessions(projectId: string) {
	const { data, error } = await apiClient.POST("/api/v1/sessions/cleanup", { params: { query: { project: projectId } } });
	if (error) throw new Error(apiErrorMessage(error));
	return data;
}
