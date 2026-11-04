import { apiClient, apiErrorMessage } from "./api-client";
import { patchProjectConfig } from "./project-settings-api";

/** Chooses whether a repo skill is bridged to other agents; deselected names are persisted in the project config and no longer offered. */
export async function setSkillBridged(projectId: string, name: string, bridged: boolean): Promise<void> {
	await patchProjectConfig(projectId, (config) => {
		const rest = (config.skillsDeselected ?? []).filter((n) => n !== name);
		const next = bridged ? rest : [...rest, name].sort();
		return { ...config, skillsDeselected: next.length > 0 ? next : undefined };
	});
}

/** Deletes a link or copy the bridge created, never the skill it points to. */
export async function removeBridgedSkill(projectId: string, path: string): Promise<{ localOnly: boolean; branch?: string }> {
	const { data, error } = await apiClient.POST("/api/v1/projects/{id}/skills/remove", { params: { path: { id: projectId } }, body: { path } });
	if (error || !data) throw new Error(apiErrorMessage(error));
	return data;
}

/** The checkout-relative folder of a skill, for the remove call. */
export function skillRelativePath(workspace: string, skillPath: string): string {
	return skillPath.startsWith(`${workspace}/`) ? skillPath.slice(workspace.length + 1) : skillPath;
}
