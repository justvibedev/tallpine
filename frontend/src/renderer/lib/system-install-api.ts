/* System dependency install HTTP calls, split out of components/InstallDependencyDialog.tsx (B298, P57). Each wrapper returns the apiClient result unchanged. */

import { apiClient } from "./api-client";

export type InstallTarget = "tmux" | "gh" | "claude" | "codex";

export function refreshAgents() {
	return apiClient.POST("/api/v1/agents/refresh");
}

/** The current job for `target`, or (when none runs) the install plan preview. */
export function getInstallJob(target: InstallTarget) {
	return apiClient.GET("/api/v1/system/install/{target}", { params: { path: { target } } });
}

export function startInstallJob(target: InstallTarget) {
	return apiClient.POST("/api/v1/system/install/{target}", { params: { path: { target } } });
}
