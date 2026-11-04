import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "./api-client";

export type DefaultBranchCheck = components["schemas"]["ProjectDefaultBranchCheckResponse"];

export const defaultBranchCheckKey = (projectId: string) => ["default-branch-check", projectId] as const;

export async function fetchDefaultBranchCheck(projectId: string): Promise<DefaultBranchCheck> {
	const { data, error } = await apiClient.GET("/api/v1/projects/{id}/default-branch-check", { params: { path: { id: projectId } } });
	if (error || !data) throw new Error(apiErrorMessage(error));
	return data;
}
