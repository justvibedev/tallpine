// Pure view-model for the per-project role settings screen: what each role is
// set to, which effort levels to offer, and the minimal patch to save. No React
// Native imports, so it is unit-testable.
import type { ProjectDetail, RoleKey, RoleSettingsPatch, RoleSettingsRequest } from "./api";
import { defaultReviewerHarness } from "./reviewerControls";

export const ROLE_KEYS: readonly RoleKey[] = ["orchestrator", "worker", "reviewer"];

export const ROLE_TITLES: Record<RoleKey, string> = {
	orchestrator: "Orchestrator",
	worker: "Worker",
	reviewer: "Reviewer",
};

export type RoleValues = { agent: string; model: string; effort: string };
export type RoleSettingsValues = Record<RoleKey, RoleValues>;

const NONE: RoleValues = { agent: "", model: "", effort: "" };

/** What the project stores for each role; an empty field inherits the default. */
export function roleValuesFromProject(project: ProjectDetail | undefined): RoleSettingsValues {
	const cfg = project?.config;
	const reviewer = cfg?.reviewers?.[0];
	return {
		orchestrator: { agent: cfg?.orchestrator?.agent ?? "", model: cfg?.orchestrator?.agentConfig?.model ?? "", effort: cfg?.orchestrator?.agentConfig?.effort ?? "" },
		worker: { agent: cfg?.worker?.agent ?? "", model: cfg?.worker?.agentConfig?.model ?? "", effort: cfg?.worker?.agentConfig?.effort ?? "" },
		reviewer: { agent: reviewer?.harness ?? "", model: reviewer?.agentConfig?.model ?? "", effort: reviewer?.agentConfig?.effort ?? "" },
	};
}

/** The request that turns `before` into `after`: changed fields only, or undefined when nothing changed. */
export function roleSettingsPatch(before: RoleSettingsValues, after: RoleSettingsValues): RoleSettingsRequest | undefined {
	const out: RoleSettingsRequest = {};
	for (const role of ROLE_KEYS) {
		const patch: RoleSettingsPatch = {};
		for (const field of ["agent", "model", "effort"] as const) {
			if ((before[role] ?? NONE)[field] !== (after[role] ?? NONE)[field]) patch[field] = after[role][field];
		}
		if (Object.keys(patch).length > 0) out[role] = patch;
	}
	return Object.keys(out).length > 0 ? out : undefined;
}

// Effort levels each agent CLI accepts, for use when the model catalog lists
// none. Mirrors AGENT_EFFORTS in frontend/src/shared/effort.ts and
// backend/internal/domain/effort.go.
const AGENT_EFFORTS: Record<string, readonly string[]> = {
	codex: ["low", "medium", "high", "xhigh", "max"],
	"claude-code": ["low", "medium", "high", "max"],
};

/** The levels to offer: the chosen model's own list, else the agent's, else none (effort stays inherited). */
export function effortOptions(agent: string, modelEfforts?: readonly string[]): readonly string[] {
	if (modelEfforts && modelEfforts.length > 0) return modelEfforts;
	return AGENT_EFFORTS[agent] ?? [];
}

/** A short label for a stored value: the value itself, or that the role inherits the project/app default. */
export function roleValueLabel(value: string): string {
	return value || "Default";
}

// The harnesses the daemon accepts for a reviewer (domain.AllReviewerHarnesses);
// the worker and orchestrator roles accept every selectable agent.
const REVIEWER_AGENTS: readonly string[] = ["claude-code", "codex", "kiro", "deepseek-harness"];

/** The agents to offer for a role; undefined means no restriction. */
export function allowedAgents(role: RoleKey, selectable: readonly string[]): string[] | undefined {
	if (role === "reviewer") return selectable.length ? selectable.filter((id) => REVIEWER_AGENTS.includes(id)) : [...REVIEWER_AGENTS];
	return selectable.length ? [...selectable] : undefined;
}

/**
 * The agent a role runs when none is set, for loading its model list: the
 * reviewer follows its configured entry, else the worker's default reviewer;
 * the other roles have no client-side default.
 */
export function effectiveAgent(role: RoleKey, values: RoleSettingsValues): string {
	if (values[role].agent) return values[role].agent;
	return role === "reviewer" ? defaultReviewerHarness(undefined, values.worker.agent) : "";
}

/** The effort to keep after the model changes: the stored one only if the new model lists it (or lists none). */
export function effortAfterModelChange(effort: string, modelEfforts?: readonly string[]): string {
	return !effort || !modelEfforts || modelEfforts.length === 0 || modelEfforts.includes(effort) ? effort : "";
}

/**
 * `values` with one field changed. Changing the agent resets the model and
 * effort (they belong to the old agent); choosing the agent already set, as
 * tapping it in the picker to dismiss it does, changes nothing.
 */
export function setRoleField(values: RoleSettingsValues, role: RoleKey, field: keyof RoleValues, value: string): RoleSettingsValues {
	const current = values[role];
	if (current[field] === value) return values;
	return { ...values, [role]: { ...current, [field]: value, ...(field === "agent" ? { model: "", effort: "" } : {}) } };
}
