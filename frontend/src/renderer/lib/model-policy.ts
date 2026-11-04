/**
 * The project model policy, as the backend enforces it. The daemon is the
 * authority (spawn, in-chat model change and agent switch all refuse a denied
 * model); this only keeps denied models out of the pickers.
 */
export interface ModelPolicy {
	denylist?: readonly string[];
	allowlist?: readonly string[];
}

/** Mirrors domain.ShippedModelDenylist: refused unless a project allowlists it. */
export const SHIPPED_MODEL_DENYLIST: readonly string[] = ["gpt-6-astra"];

const norm = (id: string) => id.trim().toLowerCase();

export function isModelDenied(id: string, policy?: ModelPolicy): boolean {
	const model = norm(id);
	if (!model) return false;
	if (policy?.allowlist?.some((entry) => norm(entry) === model)) return false;
	return [...SHIPPED_MODEL_DENYLIST, ...(policy?.denylist ?? [])].some((entry) => norm(entry) === model);
}

/** Drops denied models. Unlike a Settings filter it never falls back to the whole list. */
export function filterDeniedModels<T extends { id: string }>(models: T[], policy?: ModelPolicy): T[] {
	return models.some((model) => isModelDenied(model.id, policy)) ? models.filter((model) => !isModelDenied(model.id, policy)) : models;
}
