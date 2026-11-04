/** Recently used model storage for the agent model combobox. */
import { RECENT_MODELS_STORAGE_KEY, MAX_RECENT_MODELS } from "./model-constants";

export function recentModelsStorage(): Storage | undefined {
	if (typeof window === "undefined") return undefined;
	try {
		return window.localStorage;
	} catch {
		return undefined;
	}
}

export function readRecentModelMap(): Record<string, string[]> {
	const storage = recentModelsStorage();
	if (!storage) return {};
	try {
		const parsed: unknown = JSON.parse(storage.getItem(RECENT_MODELS_STORAGE_KEY) ?? "{}");
		if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return {};
		const result: Record<string, string[]> = {};
		for (const [scope, values] of Object.entries(parsed)) {
			if (!Array.isArray(values)) continue;
			result[scope] = values
				.filter((value): value is string => typeof value === "string")
				.slice(0, MAX_RECENT_MODELS);
		}
		return result;
	} catch {
		return {};
	}
}

export function readRecentModels(scope: string | undefined): string[] {
	return scope ? (readRecentModelMap()[scope] ?? []) : [];
}

export function rememberRecentModel(scope: string, modelID: string): string[] {
	const recentModels = readRecentModelMap();
	const next = [modelID, ...(recentModels[scope] ?? []).filter((id) => id !== modelID)].slice(
		0,
		MAX_RECENT_MODELS,
	);
	recentModels[scope] = next;
	try {
		recentModelsStorage()?.setItem(RECENT_MODELS_STORAGE_KEY, JSON.stringify(recentModels));
	} catch {
		// Recents are optional polish; model selection must still work without storage.
	}
	return next;
}
