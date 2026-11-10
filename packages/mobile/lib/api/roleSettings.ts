import type { ServerConfig } from "../config";
import { API, ApiError, req } from "./http";
import type { ProjectDetail } from "./sessions";

// ---- Per-project role settings (agent, model, effort) -----------------------
// PATCH /projects/{id}/role-settings is the one project config write the phone
// may make. The full config route (env, post-create) stays desktop-only.

export type RoleKey = "orchestrator" | "worker" | "reviewer";

/** A field left out is unchanged; an empty string clears it back to inherited. */
export type RoleSettingsPatch = { agent?: string; model?: string; effort?: string };

export type RoleSettingsRequest = Partial<Record<RoleKey, RoleSettingsPatch>>;

/** The daemon on the Mac has no role-settings route: it needs a newer build. */
export class RoleSettingsUnsupportedError extends Error {
	constructor() {
		super("This computer's Tallpine is too old to change role settings from the phone. Update Tallpine on the computer, then try again.");
		this.name = "RoleSettingsUnsupportedError";
	}
}

export async function setProjectRoleSettings(cfg: ServerConfig, projectId: string, patch: RoleSettingsRequest): Promise<ProjectDetail> {
	try {
		const res = await req(cfg, `${API}/projects/${encodeURIComponent(projectId)}/role-settings`, {
			method: "PATCH",
			body: JSON.stringify(patch),
		});
		const data = await res.json();
		return (data?.project ?? data) as ProjectDetail;
	} catch (e) {
		// An older daemon answers 404 (or 405) for the route itself; a missing
		// project answers 404 with PROJECT_NOT_FOUND, which is a different message.
		if (e instanceof ApiError && (e.status === 405 || (e.status === 404 && e.code !== "PROJECT_NOT_FOUND"))) {
			throw new RoleSettingsUnsupportedError();
		}
		throw e;
	}
}
