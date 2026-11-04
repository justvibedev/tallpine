import type { TFunction } from "i18next";
import { formatTokenCount } from "../../lib/format-token-count";
import type { ConversationCompaction } from "../../types/conversation";

const tokens = (n: number) => formatTokenCount(n).replace(/ tok$/, "");

/**
 * The popover's one-line compaction state: "Compacting…", "Compaction failed", or
 * "Compacted 12:04, 410K → 62K, auto". Sizes and trigger appear only when the
 * provider reported them; nothing reported yields no line.
 */
export function compactionLine(t: TFunction, compaction?: ConversationCompaction): string | undefined {
	if (!compaction) return undefined;
	if (compaction.status === "compacting") return t("contextMeter.details.compaction.running");
	if (compaction.status === "failed") return t("contextMeter.details.compaction.failed");
	const at = new Date(compaction.at);
	const time = Number.isNaN(at.getTime()) ? "" : at.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
	const parts = [t("contextMeter.details.compaction.done", { time }).trim()];
	if (compaction.preTokens && compaction.postTokens) {
		parts.push(t("contextMeter.details.compaction.tokens", { before: tokens(compaction.preTokens), after: tokens(compaction.postTokens) }));
	}
	if (compaction.trigger) parts.push(t(`contextMeter.details.compaction.${compaction.trigger}`));
	return parts.join(", ");
}
