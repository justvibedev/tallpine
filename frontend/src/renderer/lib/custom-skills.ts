import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "./api-client";

export type CustomSkill = components["schemas"]["ProjectCustomSkill"];
export type AppSkills = components["schemas"]["AppSkillsResponse"];

/** Whose custom skills: a project id, or null for the app-wide ones every project sees. */
export type CustomSkillOwner = string | null;

/** The app-wide skills inventory (Settings > Instructions and skills). */
export const appSkillsQueryKey = ["app-skills"] as const;

/** Every query listing custom skills of owner: the app's appear on every project page too. */
export function customSkillsQueryKeys(owner: CustomSkillOwner): readonly (readonly string[])[] {
	return owner === null ? [appSkillsQueryKey, ["project-skills"]] : [["project-skills", owner]];
}

/** The SKILL.md a new custom skill starts with. */
export function customSkillTemplate(name: string): string {
	return `---\nname: ${name}\ndescription: When to use this skill\n---\n\n`;
}

function unwrap<T>({ data, error }: { data?: T; error?: unknown }): T {
	if (error || !data) throw new Error(apiErrorMessage(error));
	return data;
}

export async function fetchAppSkills(): Promise<AppSkills> {
	return unwrap(await apiClient.GET("/api/v1/settings/skills"));
}

export async function createCustomSkill(owner: CustomSkillOwner, name: string, content: string): Promise<CustomSkill> {
	return unwrap(owner === null
		? await apiClient.POST("/api/v1/settings/skills/custom", { body: { name, content } })
		: await apiClient.POST("/api/v1/projects/{id}/skills/custom", { params: { path: { id: owner } }, body: { name, content } }));
}

export async function importCustomSkill(owner: CustomSkillOwner, name: string, source: string): Promise<CustomSkill> {
	return unwrap(owner === null
		? await apiClient.POST("/api/v1/settings/skills/custom/import", { body: { name, source } })
		: await apiClient.POST("/api/v1/projects/{id}/skills/custom/import", { params: { path: { id: owner } }, body: { name, source } }));
}

export async function fetchCustomSkill(owner: CustomSkillOwner, name: string): Promise<CustomSkill> {
	return unwrap(owner === null
		? await apiClient.GET("/api/v1/settings/skills/custom/{name}", { params: { path: { name } } })
		: await apiClient.GET("/api/v1/projects/{id}/skills/custom/{name}", { params: { path: { id: owner, name } } }));
}

export async function updateCustomSkill(owner: CustomSkillOwner, name: string, content: string): Promise<CustomSkill> {
	return unwrap(owner === null
		? await apiClient.PUT("/api/v1/settings/skills/custom/{name}", { params: { path: { name } }, body: { content } })
		: await apiClient.PUT("/api/v1/projects/{id}/skills/custom/{name}", { params: { path: { id: owner, name } }, body: { content } }));
}

export async function deleteCustomSkill(owner: CustomSkillOwner, name: string): Promise<void> {
	const { error } = owner === null
		? await apiClient.DELETE("/api/v1/settings/skills/custom/{name}", { params: { path: { name } } })
		: await apiClient.DELETE("/api/v1/projects/{id}/skills/custom/{name}", { params: { path: { id: owner, name } } });
	if (error) throw new Error(apiErrorMessage(error));
}
