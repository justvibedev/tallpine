/** Review source label and disclosure. */
import { type ReactNode, useState } from "react";
import { ChevronIcon } from "../icons";
import { cn } from "../utils";
import { InspectorVerdict } from "./review-types";
import { VerdictBadge } from "./InspectorReviewsView";

export function ReviewSourceLabel({
	children,
	icon,
	marker,
}: {
	children: ReactNode;
	icon: ReactNode;
	marker?: string;
}) {
	return (
		<div className="flex min-w-0 items-center gap-1.5 text-muted-foreground">
			<span className="flex size-5 shrink-0 items-center justify-center rounded-sm bg-muted/55 [&_svg]:size-icon-xs">
				{icon}
			</span>
			<span className="shrink-0 text-2xs font-semibold text-foreground">
				{children}
			</span>
			{marker ? (
				<span
					className={cn(
						"inline-flex h-5 shrink-0 items-center justify-center gap-1 overflow-hidden whitespace-nowrap rounded-full border border-transparent px-2 py-0.5 text-xs font-medium transition-[background-color,border-color,color,box-shadow] focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 [&>svg]:pointer-events-none [&>svg]:size-3",
						"border-border text-foreground hover:bg-muted",
						"h-4 px-1.5 text-[9px] leading-none text-passive",
					)}
					data-slot="badge"
				>
					{marker}
				</span>
			) : null}
			<span aria-hidden="true" className="ml-1 h-px min-w-0 flex-1 bg-border/80" />
		</div>
	);
}

export function ReviewDisclosure({
	children,
	collapsible = true,
	defaultOpen,
	meta,
	title,
	verdict,
}: {
	children: ReactNode;
	collapsible?: boolean;
	defaultOpen: boolean;
	meta: string;
	title: string;
	verdict?: InspectorVerdict;
}) {
	const [open, setOpen] = useState(defaultOpen);
	if (!collapsible) {
		return (
			<article
				className="relative overflow-visible rounded-lg border border-border bg-settings-row"
				data-testid="review-pr-row"
			>
				<div className="flex min-w-0 flex-col gap-1 border-b border-border/70 px-3 py-2.5">
					<span className="flex min-w-0 items-start justify-between gap-2 @max-[420px]/inspector:flex-col @max-[420px]/inspector:items-stretch">
						<span
							className="min-w-0 whitespace-normal break-words text-sm-md font-semibold leading-snug text-foreground"
							title={title}
						>
							{title}
						</span>
						{verdict ? <VerdictBadge className="@max-[420px]/inspector:self-start" verdict={verdict} /> : null}
					</span>
					<span className="whitespace-normal break-words font-mono text-micro leading-snug text-passive" title={meta}>
						{meta}
					</span>
				</div>
				<div className="flex flex-col gap-3 px-3 py-3">{children}</div>
			</article>
		);
	}
	return (
		<article className="relative overflow-visible rounded-lg border border-border bg-settings-row">
			<button
				aria-expanded={open}
				data-testid="review-pr-row"
				className="flex w-full min-w-0 items-start gap-2 px-3 py-2.5 text-left transition-colors hover:bg-interactive-hover/30 @max-[420px]/inspector:grid @max-[420px]/inspector:grid-cols-[auto_minmax(0,1fr)]"
				onClick={() => setOpen((current) => !current)}
				type="button"
			>
				<ChevronIcon className="size-icon-sm shrink-0 text-passive" direction={open ? "down" : "right"} />
					<span className="flex min-w-0 flex-1 flex-col gap-0.5">
					<span className="whitespace-normal break-words text-sm-md font-semibold leading-snug text-foreground" title={title}>
						{title}
					</span>
					<span className="whitespace-normal break-words font-mono text-micro leading-snug text-passive" title={meta}>
						{meta}
					</span>
				</span>
				{verdict ? <VerdictBadge className="@max-[420px]/inspector:col-start-2 @max-[420px]/inspector:row-start-2 @max-[420px]/inspector:justify-self-start" verdict={verdict} /> : null}
			</button>
			{open ? <div className="flex flex-col gap-3 px-3 py-3">{children}</div> : null}
		</article>
	);
}
