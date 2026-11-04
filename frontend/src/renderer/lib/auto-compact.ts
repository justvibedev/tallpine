/**
 * Where an agent compacts its own conversation, as a stored choice.
 *
 * A token preset, or a percent of the model's context window. Only the choice is
 * parsed and validated here; the daemon turns it into tokens (backend
 * domain.AutoCompactTokenCount) and sends the resolved threshold.
 */

/** Any whole percent in this range is accepted (backend domain.ParseAutoCompactPercent). */
export const AUTO_COMPACT_PERCENT_MIN = 20;
export const AUTO_COMPACT_PERCENT_MAX = 95;
/** The steps the pickers list; "Custom" takes any other whole percent in range. */
export const AUTO_COMPACT_PERCENTS = ["40%", "50%", "60%", "70%", "80%", "90%"] as const;
export type AutoCompactChoice = "200k" | "1m" | `${number}%`;

/** The whole percent of a stored "NN%" choice, or undefined for anything else or out of range. */
export function parseAutoCompactPercent(choice: string | undefined): number | undefined {
	if (!choice || !/^\d{1,2}%$/.test(choice)) return undefined;
	const percent = Number(choice.slice(0, -1));
	return percent >= AUTO_COMPACT_PERCENT_MIN && percent <= AUTO_COMPACT_PERCENT_MAX ? percent : undefined;
}

/** A session's own choice wins; otherwise the Settings default for its agent applies. */
export function effectiveAutoCompact(sessionChoice: string | undefined, harnessDefault: string | undefined): string | undefined {
	return sessionChoice || harnessDefault || undefined;
}

/** Claude Code compacts early on its larger-window models; Codex has a token limit. */
export function supportsAutoCompact(harness: string | undefined, model = "", terminal = true): "yes" | "no" | "unsupported" {
	if (harness === "claude-code") return /haiku|opus-4-5/.test(model) ? "no" : "yes";
	if (harness === "codex") return terminal ? "yes" : "unsupported";
	return "no";
}
