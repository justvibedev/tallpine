/**
 * Reasoning-effort vocabulary. The Go source of truth is
 * `backend/internal/domain/effort.go` (EffortOrder, AgentEfforts);
 * `backend/internal/domain/effort_parity_test.go` fails when this file drifts.
 * Every frontend ordering of effort levels reads from here.
 */

/** Ascending order. "none" and "off" share a rank. */
export const EFFORT_ORDER: readonly string[] = ["none", "off", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"];

/** Effort levels each agent CLI accepts, for surfaces with no live model catalog. */
export const AGENT_EFFORTS: Readonly<Record<string, readonly string[]>> = {
	codex: ["low", "medium", "high", "xhigh", "max"],
	"claude-code": ["low", "medium", "high", "max"],
};

/** Sort key: none/off share rank 0. -1 for a level outside the vocabulary. */
export function effortRank(level: string): number {
	const index = EFFORT_ORDER.indexOf(level);
	return index < 0 ? -1 : level === "none" || level === "off" ? 0 : index - 1;
}
