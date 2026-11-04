import managedKeys from "./managed-keys.json";

/**
 * One row per project registry key the organization can lock (managed-keys.json).
 *
 * The project settings form rebuilds several config fields from its draft, with
 * fallbacks that would read as a change to a locked field. `echo` names the
 * config fields sent back exactly as loaded when the key is locked; an empty
 * list means the form never rewrites the field (it spreads the loaded config),
 * so the stored value is preserved already.
 */
export interface ProjectManagedKey {
	key: string;
	echo: readonly string[];
}

export const PROJECT_MANAGED_KEYS: readonly ProjectManagedKey[] = [
	{ key: "project.worker", echo: ["worker", "agentConfig"] },
	{ key: "project.orchestrator", echo: ["orchestrator", "agentConfig"] },
	{ key: "project.reviewers", echo: ["reviewers"] },
	{ key: "project.trackerIntake", echo: ["trackerIntake"] },
	{ key: "project.models", echo: [] },
	{ key: "project.planner", echo: [] },
	{ key: "project.autoReview", echo: ["autoReview"] },
	{ key: "project.terminateOnPrMerge", echo: ["terminateOnPrMerge"] },
	{ key: "project.reviewRisk", echo: ["reviewRisk"] },
];

/** Registry key behind each locked section of the project settings editor. */
export const PROJECT_LOCK_KEY = {
	worker: "project.worker",
	orchestrator: "project.orchestrator",
	reviewer: "project.reviewers",
	intake: "project.trackerIntake",
	autoReview: "project.autoReview",
	terminateOnPrMerge: "project.terminateOnPrMerge",
	reviewRisk: "project.reviewRisk",
} as const;

/** Every `project.*` key the managed file can set (the table must cover all of them). */
export function managedProjectKeyNames(): string[] {
	return managedKeys.map((entry) => entry.key).filter((key) => key.startsWith("project."));
}

/** Config fields to send back as loaded, given the locked registry keys. */
export function lockedEchoFields(locked: ReadonlySet<string>): Set<string> {
	const fields = new Set<string>();
	for (const row of PROJECT_MANAGED_KEYS) {
		if (locked.has(row.key)) for (const field of row.echo) fields.add(field);
	}
	return fields;
}

/** The locked config fields with their loaded values, to lay over the form's rebuilt config. */
export function lockedEchoPatch<T extends object>(loaded: T, locked: ReadonlySet<string>): Partial<T> {
	const patch: Record<string, unknown> = {};
	for (const field of lockedEchoFields(locked)) patch[field] = (loaded as Record<string, unknown>)[field];
	return patch as Partial<T>;
}
