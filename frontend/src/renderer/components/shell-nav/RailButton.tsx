import type { ReactNode } from "react";
import { cn } from "../../lib/utils";
import { NAV_ROW_HIGHLIGHT_HOST_CLASS, NavRowHighlight } from "../NavRowHighlight";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";

/** One icon on the shell rail: tooltip, growing highlight, optional count badge. */
export function RailButton({
	label,
	icon,
	active = false,
	pressed,
	badge,
	badgeTone = "muted",
	expanded = false,
	visibleLabel,
	ariaExpanded,
	onClick,
	testId,
}: {
	label: string;
	icon: ReactNode;
	active?: boolean;
	/** Set for a toggle (the panel): reflects open/closed instead of the current page. */
	pressed?: boolean;
	badge?: number;
	badgeTone?: "attention" | "muted";
	/** Show the label as text beside the icon (the labelled rail). */
	expanded?: boolean;
	/** Text shown when expanded, if shorter than the accessible `label` (which may carry a count). */
	visibleLabel?: string;
	/** Marks a disclosure button (the rail's expand/collapse toggle). */
	ariaExpanded?: boolean;
	onClick: () => void;
	testId?: string;
}) {
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<button
					aria-current={active && pressed === undefined ? "page" : undefined}
					aria-expanded={ariaExpanded}
					aria-label={label}
					aria-pressed={pressed}
					className={cn(
						NAV_ROW_HIGHLIGHT_HOST_CLASS,
						expanded
							? "flex h-control-board w-full items-center gap-2.5 rounded-lg px-2.5 text-left text-control text-muted-foreground [&_svg]:size-icon-base"
							: "grid size-control-board place-items-center rounded-lg text-muted-foreground [&_svg]:size-icon-base",
					)}
					data-active={active}
					data-testid={testId}
					onClick={onClick}
					type="button"
				>
					<NavRowHighlight active={active} />
					<span className="relative z-[1] grid shrink-0 place-items-center">{icon}</span>
					{expanded ? <span className="relative z-[1] min-w-0 flex-1 truncate" data-testid={testId ? `${testId}-label` : undefined}>{visibleLabel ?? label}</span> : null}
					{badge ? (
						<span
							aria-hidden="true"
							className={cn(
								"absolute z-[2] grid min-w-4 place-items-center rounded-full px-1 text-2xs font-medium leading-4",
								expanded ? "right-2 top-1/2 -translate-y-1/2" : "-right-0.5 -top-0.5",
								badgeTone === "attention" ? "bg-primary text-primary-foreground" : "bg-muted text-muted-foreground",
							)}
							data-testid={testId ? `${testId}-badge` : undefined}
						>
							{badge > 99 ? "99+" : badge}
						</span>
					) : null}
				</button>
			</TooltipTrigger>
			{expanded ? null : <TooltipContent side="right">{label}</TooltipContent>}
		</Tooltip>
	);
}
