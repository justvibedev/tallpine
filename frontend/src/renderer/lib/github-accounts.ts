import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "./api-client";

export type GitHubAccounts = components["schemas"]["GitHubAccountsResponse"];

export const githubAccountsKey = (projectId: string) => ["github-accounts", projectId] as const;

/** The gh accounts signed in on this machine (logins only) and their push access to the project's repo. */
export async function fetchGitHubAccounts(projectId: string): Promise<GitHubAccounts> {
	const { data, error } = await apiClient.GET("/api/v1/projects/{id}/github-accounts", { params: { path: { id: projectId } } });
	if (error || !data) throw new Error(apiErrorMessage(error));
	return data;
}
