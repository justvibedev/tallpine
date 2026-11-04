import { ChevronRight } from "lucide-react";
import { type ReactNode } from "react";
import { cn } from "../../lib/utils";
import { NAV_ROW_HIGHLIGHT_HOST_CLASS, NavRowHighlight } from "../NavRowHighlight";
import { SECTION_ROW_CLASS } from "./constants";

export function SectionDisclosure({
	icon,
	label,
	open = true,
	onToggle,
	className,
	trailing,
	collapsible = true,
}: {
	icon?: ReactNode;
	label: string;
	open?: boolean;
	onToggle?: () => void;
	className?: string;
	/** Optional trailing control (e.g. Projects "+") — its own button, not the row. */
	trailing?: ReactNode;
	/** When false, render a static label row with no chevron, toggle, or hover fill. */
	collapsible?: boolean;
}) {
	const labelRow = (
		<>
			{icon}
			<span className="truncate">{label}</span>
			{collapsible ? (
				<ChevronRight
					aria-hidden="true"
					className={cn("size-3.5! shrink-0 transition-transform duration-150", open && "rotate-90")}
					strokeWidth={2}
				/>
			) : null}
		</>
	);

	if (!collapsible) {
		return (
			<div className={cn(SECTION_ROW_CLASS, trailing && "pr-1", className)}>
				<div className="flex min-w-0 flex-1 items-center gap-2">
					{labelRow}
				</div>
				{trailing}
			</div>
		);
	}

	if (trailing) {
		return (
			<div
				className={cn(
					SECTION_ROW_CLASS,
					NAV_ROW_HIGHLIGHT_HOST_CLASS,
					"rounded-lg pr-1",
					className,
				)}
			>
				<NavRowHighlight />
				<button
					aria-expanded={open}
					aria-label={label}
					className="relative z-[1] flex min-w-0 flex-1 self-stretch items-center gap-2 text-left"
					onClick={onToggle}
					type="button"
				>
					{labelRow}
				</button>
				<span className="relative z-[1] shrink-0">{trailing}</span>
			</div>
		);
	}

	return (
		<button
			aria-expanded={open}
			aria-label={label}
			className={cn(
				SECTION_ROW_CLASS,
				NAV_ROW_HIGHLIGHT_HOST_CLASS,
				"rounded-lg text-left",
				className,
			)}
			onClick={onToggle}
			type="button"
		>
			<NavRowHighlight />
			<span className="relative z-[1] flex min-w-0 flex-1 items-center gap-2">
				{labelRow}
			</span>
		</button>
	);
}
