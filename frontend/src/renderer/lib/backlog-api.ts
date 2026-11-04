import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "./api-client";

export type BacklogItem = components["schemas"]["BacklogItemResponse"];
export type BacklogLink = components["schemas"]["BacklogLinkResponse"];
export type BacklogState = "inbox" | "open" | "done" | "dropped";
export type BacklogLinkKind = "session" | "pr" | "issue" | "artifact" | "memory" | "item" | "blocked_by";
export type BacklogUpdate = components["schemas"]["UpdateBacklogRequest"];

export const backlogRootKey = ["backlog"] as const;
export const backlogProjectKey = (projectId: string) => ["backlog", projectId] as const;
export const backlogListKey = (projectId: string, state: BacklogState) => ["backlog", projectId, "list", state] as const;
export const backlogItemKey = (projectId: string, ref: string) => ["backlog", projectId, "item", ref] as const;
export const backlogInboxCountsKey = ["backlog", "inbox-counts"] as const;

/** Largest page the API serves. */
export const BACKLOG_PAGE_LIMIT = 500;

function fail(error: unknown): never {
	throw new Error(apiErrorMessage(error));
}

export async function listBacklog(projectId: string, state: BacklogState): Promise<BacklogItem[]> {
	const { data, error } = await apiClient.GET("/api/v1/projects/{projectId}/backlog", {
		params: { path: { projectId }, query: { state, limit: String(BACKLOG_PAGE_LIMIT) } },
	});
	if (error) fail(error);
	return data?.items ?? [];
}

export async function createBacklogItem(projectId: string, input: { title: string; note?: string }): Promise<BacklogItem> {
	const { data, error } = await apiClient.POST("/api/v1/projects/{projectId}/backlog", {
		params: { path: { projectId } },
		// A human filing from the UI skips the inbox: it is their own idea, not an agent's follow-up.
		body: { title: input.title, ...(input.note ? { note: input.note } : {}), open: true },
	});
	if (error || !data) fail(error);
	return data.item;
}

export async function getBacklogItem(projectId: string, ref: string): Promise<BacklogItem> {
	const { data, error } = await apiClient.GET("/api/v1/projects/{projectId}/backlog/{ref}", { params: { path: { projectId, ref } } });
	if (error || !data) fail(error);
	return data.item;
}

export async function updateBacklogItem(projectId: string, ref: string, body: BacklogUpdate): Promise<BacklogItem> {
	const { data, error } = await apiClient.PATCH("/api/v1/projects/{projectId}/backlog/{ref}", { params: { path: { projectId, ref } }, body });
	if (error || !data) fail(error);
	return data.item;
}

export async function setBacklogState(projectId: string, ref: string, state: BacklogState, reason?: string): Promise<BacklogItem> {
	const { data, error } = await apiClient.POST("/api/v1/projects/{projectId}/backlog/{ref}/state", {
		params: { path: { projectId, ref } },
		body: { state, ...(reason ? { reason } : {}) },
	});
	if (error || !data) fail(error);
	return data.item;
}

export async function reorderBacklogItem(projectId: string, ref: string, target: { before: string } | { after: string }): Promise<void> {
	const { error } = await apiClient.POST("/api/v1/projects/{projectId}/backlog/reorder", { params: { path: { projectId } }, body: { ref, ...target } });
	if (error) fail(error);
}

export async function addBacklogLink(projectId: string, ref: string, kind: BacklogLinkKind, target: string): Promise<BacklogItem> {
	const { data, error } = await apiClient.POST("/api/v1/projects/{projectId}/backlog/{ref}/links", {
		params: { path: { projectId, ref } },
		body: { kind, ref: target },
	});
	if (error || !data) fail(error);
	return data.item;
}

export async function removeBacklogLink(projectId: string, ref: string, kind: BacklogLinkKind, target: string): Promise<BacklogItem> {
	// The server reads the link from the DELETE body, but the generated schema types DELETE
	// requests without one, so the init is widened here.
	const init = { params: { path: { projectId, ref } }, body: { kind, ref: target } } as unknown as { params: { path: { projectId: string; ref: string } } };
	const { data, error } = await apiClient.DELETE("/api/v1/projects/{projectId}/backlog/{ref}/links", init);
	if (error || !data) fail(error);
	return data.item;
}

export async function setBacklogField(projectId: string, ref: string, key: string, value: string): Promise<BacklogItem> {
	const { data, error } = await apiClient.PUT("/api/v1/projects/{projectId}/backlog/{ref}/fields/{key}", {
		params: { path: { projectId, ref, key } },
		body: { value },
	});
	if (error || !data) fail(error);
	return data.item;
}

export async function clearBacklogField(projectId: string, ref: string, key: string): Promise<BacklogItem> {
	const { data, error } = await apiClient.DELETE("/api/v1/projects/{projectId}/backlog/{ref}/fields/{key}", { params: { path: { projectId, ref, key } } });
	if (error || !data) fail(error);
	return data.item;
}

export async function getBacklogInboxCounts(): Promise<Record<string, number>> {
	const { data, error } = await apiClient.GET("/api/v1/backlog/inbox-counts");
	if (error) fail(error);
	return data?.counts ?? {};
}
