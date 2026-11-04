import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "./api-client";
import { backlogProjectKey } from "./backlog-api";

export type BacklogRun = components["schemas"]["BacklogRunResponse"];
export type BacklogRunItem = components["schemas"]["BacklogRunItemResponse"];
export type BacklogUsage = components["schemas"]["BacklogUsageResponse"];
export type BacklogRunStatus = BacklogRun["status"];
export type BacklogRunControl = "pause" | "resume" | "stop";
export type BacklogRunScope = "local" | "pr" | "pr-merge";
/** What a run covers: every open item, or one item (a parent runs its children). */
export type BacklogRunSelection = { kind: "all" } | { kind: "refs"; refs: string[] } | { kind: "parent"; ref: string };

// Under the project's backlog key, so a `backlog_changed` event (which invalidates
// that key) refreshes the run card and item cost with everything else.
export const backlogRunKey = (projectId: string) => [...backlogProjectKey(projectId), "run"] as const;
export const backlogItemUsageKey = (projectId: string, ref: string) => [...backlogProjectKey(projectId), "usage", ref] as const;

function fail(error: unknown): never {
	throw new Error(apiErrorMessage(error));
}

/** The project's newest run (the open one while there is one), with its plan; undefined when it never had one. */
export async function getLatestBacklogRun(projectId: string): Promise<BacklogRun | undefined> {
	const list = await apiClient.GET("/api/v1/projects/{projectId}/backlog-runs", { params: { path: { projectId }, query: { limit: "1" } } });
	if (list.error) fail(list.error);
	const latest = list.data?.runs?.[0];
	if (!latest) return undefined;
	const { data, error } = await apiClient.GET("/api/v1/projects/{projectId}/backlog-runs/{runRef}", { params: { path: { projectId, runRef: latest.ref } } });
	if (error || !data) fail(error);
	return data.run;
}

export async function startBacklogRun(projectId: string, scope: BacklogRunScope, selection: BacklogRunSelection): Promise<BacklogRun> {
	const body: components["schemas"]["StartBacklogRunRequest"] =
		selection.kind === "all"
			? { scope, selection: "all" }
			: selection.kind === "refs"
				? { scope, selection: "refs", refs: selection.refs }
				: { scope, selection: "parent", refs: [selection.ref] };
	const { data, error } = await apiClient.POST("/api/v1/projects/{projectId}/backlog-runs", { params: { path: { projectId } }, body });
	if (error || !data) fail(error);
	return data.run;
}

const CONTROL_PATH = {
	pause: "/api/v1/projects/{projectId}/backlog-runs/{runRef}/pause",
	resume: "/api/v1/projects/{projectId}/backlog-runs/{runRef}/resume",
	stop: "/api/v1/projects/{projectId}/backlog-runs/{runRef}/stop",
} as const;

/** Pause, resume or stop a run. The daemon sends the orchestrator one short nudge for each. */
export async function controlBacklogRun(projectId: string, runRef: string, action: BacklogRunControl): Promise<BacklogRun> {
	const params = { params: { path: { projectId, runRef } } };
	const { data, error } =
		action === "pause" ? await apiClient.POST(CONTROL_PATH.pause, { ...params, body: {} }) : await apiClient.POST(CONTROL_PATH[action], params);
	if (error || !data) fail(error);
	return data.run;
}

export async function getBacklogItemUsage(projectId: string, ref: string): Promise<BacklogUsage> {
	const { data, error } = await apiClient.GET("/api/v1/projects/{projectId}/backlog/{ref}/usage", { params: { path: { projectId, ref } } });
	if (error || !data) fail(error);
	return data.usage;
}
