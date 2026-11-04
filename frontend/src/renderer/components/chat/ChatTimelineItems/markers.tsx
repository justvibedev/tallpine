import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { agentModelsQueryOptions } from "../../../hooks/useAgentModelsQuery";
import { agentLabel } from "../../../lib/agent-options";
import { recordedModelName } from "../../../lib/agent-model-choices";
import { formatEffortLabel } from "../../settings/EffortPicker";
import { ProviderMark } from "../ProviderMark";
import { ArrowRight } from "lucide-react";
import { cn } from "../../../lib/utils";
import { type ConversationActivity } from "../../../types/conversation";
import { formatTokens } from "./format";

/** A status message followed by a full-width rule, with no text inside the rule. */
export function TwoRowTimelineMarker({
	message,
	detail,
	tone = "text-muted-foreground/70",
	detailTone = "text-muted-foreground/70",
	action,
	wrapDetail = false,
}: {
	message: string;
	detail?: string;
	tone?: string;
	detailTone?: string;
	action?: ReactNode;
	/** The detail is an instruction the reader needs whole, so it wraps instead of truncating. */
	wrapDetail?: boolean;
}) {
	return (
		<div className="flex min-w-0 flex-col gap-1 py-1">
			<div className={cn("flex min-w-0 items-baseline gap-x-2 gap-y-0.5 text-xs", wrapDetail && "flex-wrap", tone)}>
				<span className="shrink-0">{message}</span>
				{detail ? (
					<span
						className={cn("min-w-0", wrapDetail ? "text-pretty" : "truncate", detailTone)}
						title={detail}
					>
						{detail}
					</span>
				) : null}
				{action}
			</div>
			<span aria-hidden="true" className="h-px w-full bg-border" />
		</div>
	);
}

export function CompactionMarker({ activity }: { activity: ConversationActivity }) {
	const reclaimed = activity.detail?.tokensReclaimed;
	const after = activity.detail?.tokensAfter;
	const contextWindow = activity.detail?.contextWindow;
	const detail = reclaimed ? `−${formatTokens(reclaimed)}` : undefined;
	const fullness = after && contextWindow ? `${Math.round((after / contextWindow) * 100)}% full` : undefined;

	return (
		<TwoRowTimelineMarker
			message="The conversation history was compacted"
			detail={[detail, fullness].filter(Boolean).join(" · ") || undefined}
		/>
	);
}

/** The remote host a chat's session runs on, whose catalogs name its models. */
export const ChatHostId = createContext<string | undefined>(undefined);

/** One side of a handoff: the agent's mark, then its model by name when one was recorded. */
function HandoffSide({ harness, model, effort, compactWindow, bright }: { harness: string; model?: string; effort?: string; compactWindow?: string; bright?: boolean }) {
	const { t } = useTranslation();
	// The marker stores the model id. Its name comes from the agent's catalog on the session's host.
	const hostId = useContext(ChatHostId);
	const catalog = useQuery({ ...agentModelsQueryOptions(harness, "", hostId), enabled: Boolean(model) }).data;
	const name = model ? recordedModelName(harness, model, catalog?.models ?? []) : agentLabel(harness);
	return (
		<span className={cn("flex min-w-0 items-center gap-1.5", bright ? "text-foreground" : undefined)}>
			<ProviderMark provider={harness} />
			<span className="truncate">{name.replace(/\s*\b1M\b/g, "")}</span>
			{effort ? <span className="shrink-0 text-muted-foreground">{formatEffortLabel(effort, t)}</span> : null}
			{compactWindow ? <span className="shrink-0 text-muted-foreground" title="Compacts on its own at this size">· {compactWindow.toUpperCase()}</span> : null}
		</span>
	);
}

/** Where one agent handed the conversation to another. */
export function ProviderSwitchMarker({ activity }: { activity: ConversationActivity }) {
	const { t } = useTranslation();
	const detail = activity.detail;
	const switching = Boolean(detail?.switching);
	const named = Boolean(detail?.sourceHarness && detail.targetHarness);
	// A switched row only seconds old is the one just made, so it opens from closed; older ones start open.
	const [open, setOpen] = useState(() => switching || !(Date.now() - Date.parse(activity.createdAt) < 8000));
	useEffect(() => {
		if (switching || open) return;
		let inner = 0;
		const outer = requestAnimationFrame(() => { inner = requestAnimationFrame(() => setOpen(true)); });
		return () => { cancelAnimationFrame(outer); cancelAnimationFrame(inner); };
	}, [switching, open]);
	const revealed = !switching && open;
	const label = switching
		? t("agentSwitch.switching")
		: named
			? t("agentSwitch.switchedFromTo", { from: agentLabel(detail?.sourceHarness ?? ""), to: agentLabel(detail?.targetHarness ?? "") })
			: t("agentSwitch.switched");
	return (
		<div role="separator" aria-label={label} aria-busy={switching || undefined} className="flex min-w-0 items-center gap-3 py-2 text-[11px] text-muted-foreground">
			<span aria-hidden="true" className="h-px min-w-4 flex-1 bg-border" />
			<div className="flex min-w-0 items-center">
				<span data-shimmer={switching ? t("agentSwitch.switching") : undefined} className={cn("inline-block shrink-0", switching ? "chat-working-shimmer" : undefined)}>
					{switching ? t("agentSwitch.switching") : t("agentSwitch.switched")}
				</span>
				{named ? (
					// Closed while the switch runs, then its width grows from 0 to its own.
					<div
						className={cn(
							"grid min-w-0 transition-[grid-template-columns] duration-300 ease-out motion-reduce:transition-none",
							revealed ? "grid-cols-[1fr]" : "grid-cols-[0fr]",
						)}
					>
						<div className="min-w-0 overflow-hidden">
							<span className="flex w-max shrink-0 items-center gap-3 whitespace-nowrap pl-3">
								<HandoffSide harness={detail?.sourceHarness ?? ""} model={detail?.sourceModel} />
								<ArrowRight aria-hidden="true" className="size-3 shrink-0" />
								<HandoffSide harness={detail?.targetHarness ?? ""} model={detail?.targetModel} effort={detail?.targetEffort} compactWindow={detail?.targetAutoCompactWindow} bright />
							</span>
						</div>
					</div>
				) : null}
			</div>
			<span aria-hidden="true" className="h-px min-w-4 flex-1 bg-border" />
		</div>
	);
}

/** Where the user continued a stopped turn: a marker in place of the continue message. */
export function ContinuedMarker() {
	return <TwoRowTimelineMarker message="Continued" />;
}
