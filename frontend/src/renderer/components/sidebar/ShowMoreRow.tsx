import { useTranslation } from "react-i18next";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { cn } from "../../lib/utils";
import { NAV_ROW_HIGHLIGHT_HOST_CLASS, NavRowHighlight } from "../NavRowHighlight";
import { SECTION_ROW_CLASS } from "./constants";

/** Releases a section's initial cap. Sits below its section's scroller so the
 *  cap can never hide the control that lifts it. */
export function ShowMoreRow({
	label,
	expanded,
	onClick,
	className,
}: {
	label: string;
	expanded: boolean;
	onClick: () => void;
	className?: string;
}) {
	const { t } = useTranslation();
	const prefersReducedMotion = useReducedMotion();
	return (
		<AnimatePresence initial={false} mode="wait">
			<motion.button
				key={expanded ? "show-less" : "show-more"}
				aria-label={label}
				className={cn(
					SECTION_ROW_CLASS,
					NAV_ROW_HIGHLIGHT_HOST_CLASS,
					"mb-1 shrink-0 rounded-lg text-left text-muted-foreground",
					className,
				)}
				initial={{ opacity: 0, y: 4 }}
				animate={{ opacity: 1, y: 0 }}
				exit={{ opacity: 0, y: -4 }}
				onClick={onClick}
				transition={prefersReducedMotion ? { duration: 0 } : { duration: 0.14, ease: [0.25, 0.46, 0.45, 0.94] }}
				type="button"
			>
				<NavRowHighlight />
				<span className="relative z-[1] truncate">{t(expanded ? "shell.showLess" : "shell.showMore")}</span>
			</motion.button>
		</AnimatePresence>
	);
}
