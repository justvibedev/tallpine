import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "./api-client";

export type CommitTarget = components["schemas"]["ProjectSkillCommitTarget"];
export type CommitResult = components["schemas"]["CommitProjectSkillsResponse"];

/**
 * Commits exactly paths to the branch new sessions start from (and pushes when
 * they start from the remote). Only ever called from an explicit click.
 */
export async function commitProjectSkills(projectId: string, paths: readonly string[]): Promise<CommitResult> {
	const { data, error } = await apiClient.POST("/api/v1/projects/{id}/skills/commit", { params: { path: { id: projectId } }, body: { paths: [...paths] } });
	if (error || !data) throw new Error(apiErrorMessage(error));
	return data;
}

/** Pushes a kept commit as a new branch for a pull request, when the default branch refused the push. */
export async function pushProjectSkillsBranch(projectId: string, sha: string) {
	const { data, error } = await apiClient.POST("/api/v1/projects/{id}/skills/push-branch", { params: { path: { id: projectId } }, body: { sha } });
	if (error || !data) throw new Error(apiErrorMessage(error));
	return data;
}

/** The commit button label, naming the branch (and remote when it pushes). */
export function commitLabelKey(target: CommitTarget, create: boolean) {
	if (create) return target.needsPush ? "skillsCommit.createCommitPush" : "skillsCommit.createCommit";
	return target.needsPush ? "skillsCommit.commitPush" : "skillsCommit.commit";
}
