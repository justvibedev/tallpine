import { useState } from "react";
import { ChevronRight, Shuffle, ShieldCheck, ShieldX } from "lucide-react";
import { cn } from "../../../lib/utils";
import { ActivityTitle } from "../ChatMarkdown";
import { reviewedPaths, type ConversationActivity } from "../../../types/conversation";
import { shortenPaths, formatDecisionDuration } from "./format";

const RISK_TONE: Record<string, string> = {
	low: "text-muted-foreground",
	medium: "text-warning",
	high: "text-destructive",
	critical: "text-destructive",
};

/**
 * A decision the provider made instead of asking.
 *
 * This is not an approval card and must not read like one: nobody was asked, and
 * there is nothing to answer. What the row owes the user is the fact that a decision
 * was taken on their behalf, what it allowed, and — reachable rather than shouted —
 * the reasoning that allowed it. Denials are called out more loudly than approvals,
 * because a denial is why something the user expected did not happen.
 */
export function AutoReviewRow({ activity }: { activity: ConversationActivity }) {
	const [open, setOpen] = useState(false);
	const detail = activity.detail;
	const denied = (detail?.status ?? "").toLowerCase().includes("den");
	const Icon = denied ? ShieldX : ShieldCheck;
	const paths = reviewedPaths(activity);
	const hasBody = Boolean(
		detail?.rationale || detail?.command || detail?.cwd || detail?.host || paths.length,
	);

	return (
		<div className="group/activity border-t border-border first:border-t-0">
			<button
				type="button"
				onClick={() => setOpen((prev) => !prev)}
				disabled={!hasBody}
				aria-expanded={hasBody ? open : undefined}
				className={cn(
					"activity-row-toggle flex min-h-[35px] w-full select-none items-center gap-[9px] px-[11px] py-2 text-left text-xs",
					hasBody && "hover:text-foreground",
					!hasBody && "cursor-default",
				)}
			>
				<Icon
					aria-hidden="true"
					className={cn(
						"w-[15px] shrink-0 text-center",
						denied ? "text-destructive" : "text-muted-foreground/70",
					)}
					size={14}
				/>
				<strong className="shrink-0 font-medium text-foreground">
					{denied ? "Auto-declined" : "Auto-approved"}
				</strong>
				<span
					className="min-w-0 flex-1 truncate font-mono text-caption text-muted-foreground"
					title={activity.summary}
				>
					<ActivityTitle text={shortenPaths(activity.summary)} />
				</span>
				{detail?.riskLevel ? (
					<span
						className={cn(
							"shrink-0 text-xs",
							RISK_TONE[detail.riskLevel.toLowerCase()] ?? "text-muted-foreground",
						)}
						title={`Risk assessed as ${detail.riskLevel}`}
					>
						{detail.riskLevel}
					</span>
				) : null}
				{hasBody ? (
					<ChevronRight
						aria-hidden="true"
						className={cn(
							"size-3 shrink-0 text-muted-foreground/50 transition-all",
							open ? "rotate-90 opacity-100" : "opacity-0 group-hover/activity:opacity-100",
						)}
					/>
				) : null}
			</button>

			{open && hasBody ? (
				<div className="flex flex-col gap-2 px-[11px] pb-2.5">
					{/* Said in full rather than implied by the label: "auto-approved" alone
					    leaves it ambiguous whether the user set something up that did this. */}
					<p className="text-xs leading-relaxed text-muted-foreground">
						{denied
							? "The agent asked to do this and the provider declined on your behalf. You were not asked."
							: "The agent asked to do this and the provider allowed it on your behalf. You were not asked."}
					</p>
					{detail?.rationale ? (
						<p className="rounded border border-border bg-background px-2.5 py-1.5 text-xs leading-relaxed text-foreground">
							{detail.rationale}
						</p>
					) : null}
					<dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 font-mono text-caption leading-relaxed">
						{detail?.command ? (
							<>
								<dt className="text-muted-foreground/70">command</dt>
								<dd className="min-w-0 break-all text-foreground">{detail.command}</dd>
							</>
						) : null}
						{detail?.cwd ? (
							<>
								<dt className="text-muted-foreground/70">cwd</dt>
								<dd className="min-w-0 break-all text-muted-foreground">
									{shortenPaths(detail.cwd)}
								</dd>
							</>
						) : null}
						{detail?.host ? (
							<>
								<dt className="text-muted-foreground/70">host</dt>
								<dd className="min-w-0 break-all text-muted-foreground">{detail.host}</dd>
							</>
						) : null}
						{paths.length ? (
							<>
								<dt className="text-muted-foreground/70">files</dt>
								<dd className="min-w-0 break-all text-muted-foreground">
									{paths.map((path) => shortenPaths(path)).join(", ")}
								</dd>
							</>
						) : null}
						{detail?.decisionSource ? (
							<>
								<dt className="text-muted-foreground/70">decided by</dt>
								<dd className="text-muted-foreground">
									{/* A model making the call is a materially different thing to be
									    told than a policy rule matching, so the provider's own word
									    for it is carried rather than flattened to "automatically". */}
									{detail.decisionSource}
									{detail.durationMs !== undefined && detail.durationMs > 0
										? ` · ${formatDecisionDuration(detail.durationMs)}`
										: ""}
								</dd>
							</>
						) : null}
					</dl>
				</div>
			) : null}
		</div>
	);
}

/* -------------------------------------------------------------------------- */
/* system events                                                               */
/* -------------------------------------------------------------------------- */

/**
 * Where the provider swapped the model out.
 *
 * On the timeline as well as in the composer because it happened at a point in the
 * conversation: everything after this row was answered by a different model than
 * everything before it, and only a timeline entry can say where the line is.
 */
export function RerouteRow({ activity }: { activity: ConversationActivity }) {
	const detail = activity.detail;
	return (
		<div className="flex items-start gap-2.5 rounded-md border border-border bg-surface/60 px-3 py-2">
			<Shuffle aria-hidden="true" className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />
			<div className="flex min-w-0 flex-col gap-0.5">
				<span className="text-xs text-foreground">
					Answered by{" "}
					<strong className="font-medium">{detail?.toModel ?? "another model"}</strong>
					{detail?.fromModel ? (
						<>
							{" "}
							instead of <span className="text-muted-foreground">{detail.fromModel}</span>
						</>
					) : null}
				</span>
				{detail?.reason ? (
					<span className="text-xs leading-snug text-muted-foreground">{detail.reason}</span>
				) : null}
			</div>
		</div>
	);
}
