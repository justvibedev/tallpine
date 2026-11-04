/** Shared header and footer of the elicitation dock. */
import { type ReactNode } from "react";
import { ChevronDown, X } from "lucide-react";
import { cn } from "../../../lib/utils";

export function DockHeader({
	id,
	title,
	pager,
	onDismiss,
	disabled,
	collapsed,
	onToggleCollapsed,
}: {
	id?: string;
	title: string;
	pager?: string;
	/** Cancel lives in the header as a quiet close, not as a footer pill. */
	onDismiss?: () => void;
	disabled?: boolean;
	/** Folds the answers away so the transcript above can be read first. */
	collapsed?: boolean;
	onToggleCollapsed?: () => void;
}) {
	return (
		<div className="flex items-start gap-2 px-4 pb-2 pt-3">
			<div className="min-w-0 flex-1">
				<p className="text-caption leading-snug text-muted-foreground">
					Question{pager ? <span className="tabular-nums"> · {pager}</span> : null}
				</p>
				<p id={id} className="mt-0.5 whitespace-pre-wrap break-words text-sm font-medium leading-snug text-foreground">
					{title}
				</p>
			</div>
			{onToggleCollapsed ? (
				<button
					type="button"
					aria-label={collapsed ? "Expand question" : "Collapse question"}
					aria-expanded={!collapsed}
					title={collapsed ? "Expand" : "Collapse"}
					onClick={onToggleCollapsed}
					className="grid size-6 shrink-0 place-items-center rounded-md text-muted-foreground transition-colors hover:bg-interactive-hover hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
				>
					<ChevronDown aria-hidden="true" className={cn("size-3.5 transition-transform", collapsed && "-rotate-90")} />
				</button>
			) : null}
			{onDismiss ? (
				<button
					type="button"
					aria-label="Cancel"
					title="Cancel"
					disabled={disabled}
					onClick={onDismiss}
					className="-mr-1 grid size-6 shrink-0 place-items-center rounded-md text-muted-foreground transition-colors hover:bg-interactive-hover hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40 disabled:opacity-50"
				>
					<X aria-hidden="true" className="size-3.5" />
				</button>
			) : null}
		</div>
	);
}

export function DockFooter({ children }: { children: ReactNode }) {
	return <div className="flex flex-wrap items-center justify-between gap-1.5 px-4 pb-3 pt-1.5">{children}</div>;
}

