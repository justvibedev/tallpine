export { formatResetIn } from "@tallpine/shared/format/duration";
import type { ConversationRateLimits } from "../../types/conversation";

/**
 * A remaining duration as the largest useful unit. The provider's windows are
 * measured in days, so minute precision on a four-day reset would be noise.
 */

/**
 * The tighter of the provider's two windows, or undefined when it reported
 * neither. Negative is the daemon's "not reported" signal and must not be drawn:
 * a meter running backwards is worse than no meter.
 */
export function worstWindow(
	limits: ConversationRateLimits,
): { percent: number; resetsIn?: number } | undefined {
	const windows = [
		{ percent: limits.primaryUsedPercent, resetsIn: limits.primaryResetsInSeconds },
		{ percent: limits.secondaryUsedPercent, resetsIn: limits.secondaryResetsInSeconds },
	].filter((w) => typeof w.percent === "number" && w.percent >= 0);
	if (windows.length === 0) return undefined;
	return windows.reduce((worst, w) => (w.percent > worst.percent ? w : worst));
}
