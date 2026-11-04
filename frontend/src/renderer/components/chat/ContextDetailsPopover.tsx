/**
 * The detail view behind the context circle: what the hover tooltip has no room
 * for. Model and window, fullness, where the agent compacts and how far away
 * that is, when it last did, and the account quota when the provider reports it.
 *
 * Every figure is one the daemon or provider reported. A missing window, a
 * percent threshold with no window to apply to, or an agent that cannot be told
 * where to compact reads as such rather than as a guess.
 */

import { useTranslation } from "react-i18next";
import { formatTokenCount } from "../../lib/format-token-count";
import { PopoverContent } from "../ui/popover";
import type { ConversationUsage } from "../../types/conversation";
import { resolveContextView, type ContextDetails } from "../../lib/context-view";
import { formatResetIn, worstWindow } from "./contextQuota";
import { compactionLine } from "./compactionLine";
import { CompactionControl } from "./CompactionControl";
import { ContextBreakdownSection } from "./ContextBreakdownSection";
import { ModelUsageSection } from "./ModelUsageSection";

const tokens = (n: number) => formatTokenCount(n).replace(/ tok$/, "");

function Row({ label, children }: { label: string; children: React.ReactNode }) {
	return (
		<div className="flex items-baseline justify-between gap-4">
			<dt className="text-muted-foreground">{label}</dt>
			<dd className="text-right tabular-nums">{children}</dd>
		</div>
	);
}

export function ContextDetailsPopover({ usage, details }: { usage: ConversationUsage; details?: ContextDetails }) {
	const { t } = useTranslation();
	const { contextUsed } = usage;
	const view = resolveContextView(usage, details);
	const { capacity, windowKnown, percent, threshold, left } = view;
	const unknown = t("contextMeter.details.unknown");

	let compactsAt: string;
	if (view.compactsAt.kind === "unsupported") compactsAt = t("contextMeter.details.notSupported");
	else if (view.compactsAt.kind === "project") compactsAt = t("contextMeter.details.setByProject", { value: tokens(view.compactsAt.tokens) });
	else if (view.compactsAt.kind === "agentDefault") compactsAt = t("contextMeter.details.agentDefault");
	else if (view.compactsAt.kind === "needsWindow") compactsAt = t("contextMeter.details.needsWindow", { choice: view.compactsAt.choice });
	else {
		const share = view.compactsAt.share === undefined ? "" : ` (${view.compactsAt.share}%)`;
		compactsAt = `${tokens(view.compactsAt.tokens)}${share}`;
	}
	const quota = details?.rateLimits ? worstWindow(details.rateLimits) : undefined;
	const compactionText = compactionLine(t, details?.lastCompaction);
	const compactedAt = details?.compactedAt ? new Date(details.compactedAt) : undefined;

	return (
		<PopoverContent align="start" className="w-80 max-h-[80vh] overflow-y-auto p-3 text-xs" aria-label={t("contextMeter.details.title")}>
			<p className="mb-2 text-sm font-medium">{t("contextMeter.details.title")}</p>
			<dl className="flex flex-col gap-1.5">
				<Row label={t("contextMeter.details.model")}>{details?.model || unknown}</Row>
				<Row label={t("contextMeter.details.window")}>{windowKnown ? t("contextMeter.details.tokens", { value: tokens(capacity) }) : unknown}</Row>
				<Row label={t("contextMeter.details.used")}>
					{t("contextMeter.details.tokens", { value: tokens(contextUsed) })}{percent === undefined ? "" : ` (${percent}%)`}
				</Row>
				<Row label={t("contextMeter.details.compactsAt")}>{compactsAt}</Row>
				{left !== undefined ? (
					<Row label={t("contextMeter.details.left")}>
						{left > 0 ? t("contextMeter.details.tokens", { value: tokens(left) }) : t("contextMeter.details.pastThreshold")}
					</Row>
				) : null}
				<Row label={t("contextMeter.details.lastCompacted")}>
					{compactedAt && !Number.isNaN(compactedAt.getTime()) ? compactedAt.toLocaleString() : t("contextMeter.details.noCompaction")}
				</Row>
				{compactionText ? <Row label={t("contextMeter.details.compaction.label")}>{compactionText}</Row> : null}
				{quota ? (
					<Row label={t("contextMeter.details.quota")}>
						{t("contextMeter.details.quotaUsed", { percent: Math.round(quota.percent) })}
						{quota.resetsIn && quota.resetsIn > 0 ? `, ${t("contextMeter.details.resetsIn", { in: formatResetIn(quota.resetsIn) })}` : ""}
						{details?.rateLimits?.planLabel ? ` · ${details.rateLimits.planLabel}` : ""}
					</Row>
				) : null}
			</dl>
			{details?.compaction ? <CompactionControl {...details.compaction} /> : null}
			{details?.breakdown ? <ContextBreakdownSection {...details.breakdown} resolved={threshold ? { threshold, capacity } : undefined} /> : null}
			{details?.modelUsage ? <ModelUsageSection {...details.modelUsage} /> : null}
		</PopoverContent>
	);
}
