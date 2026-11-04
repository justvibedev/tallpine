/**
 * The settings change log (B202): who changed which setting of a project, so
 * the user sees what their orchestrators changed.
 */

import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient } from "../lib/api-client";

export type SettingsChange = components["schemas"]["SettingsChange"];

export const settingsChangesQueryKey = (projectId: string) => ["settings", "changes", projectId] as const;

export function useSettingsChanges(projectId: string, limit = 10) {
	return useQuery({
		queryKey: [...settingsChangesQueryKey(projectId), limit],
		queryFn: async (): Promise<SettingsChange[]> => {
			const { data, error } = await apiClient.GET("/api/v1/settings/changes", { params: { query: { project: projectId, limit } } });
			if (error) throw error;
			return data?.changes ?? [];
		},
	});
}
