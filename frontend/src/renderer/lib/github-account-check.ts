import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "./api-client";

export type GitHubAccountCheck = components["schemas"]["ProjectGitHubAccountCheckResponse"];

export const githubAccountCheckKey = (projectId: string) => ["github-account-check", projectId] as const;

export async function fetchGitHubAccountCheck(projectId: string): Promise<GitHubAccountCheck> {
	const { data, error } = await apiClient.GET("/api/v1/projects/{id}/github-account-check", { params: { path: { id: projectId } } });
	if (error || !data) throw new Error(apiErrorMessage(error));
	return data;
}
