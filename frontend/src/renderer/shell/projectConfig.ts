import { type WorkspaceSummary } from "../types/workspace";
import type { components } from "../../api/schema";

export function errorMessage(error: unknown) {
	return error instanceof Error ? error.message : "Could not load projects";
}

export function normalizeProjectPath(path: string): string {
	if (!path) return path;
	const trimmed = path.trim().replace(/[\\/]+$/, "");
	return trimmed === "" ? path : trimmed;
}

export function findRegisteredWorkspaceByPath(workspaces: WorkspaceSummary[], path: string): WorkspaceSummary | undefined {
	const normalizedPath = normalizeProjectPath(path);
	return workspaces.find((workspace) => normalizeProjectPath(workspace.path) === normalizedPath);
}

export type CreateProjectConfigInput = {
	workerAgent: string;
	orchestratorAgent: string;
	trackerIntake?: components["schemas"]["TrackerIntakeConfig"];
	defaultBranch?: string;
};

export function createProjectConfig(input: CreateProjectConfigInput): components["schemas"]["ProjectConfig"] {
	return {
		...(input.defaultBranch ? { defaultBranch: input.defaultBranch } : {}),
		worker: { agent: input.workerAgent as components["schemas"]["RoleOverride"]["agent"] },
		orchestrator: { agent: input.orchestratorAgent as components["schemas"]["RoleOverride"]["agent"] },
		...(input.trackerIntake ? { trackerIntake: input.trackerIntake } : {}),
	};
}

// Upper bound for the background orchestrator spawn after project creation.
// Past this the board releases the provisioning gate and shows the retry
// banner instead of staying gated forever on a hung spawn.
export const PROVISIONING_TIMEOUT_MS = 120_000;
