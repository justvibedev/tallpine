import { type ReactNode } from "react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";

export function AnimatedSectionBody({ open, children, className }: { open: boolean; children: ReactNode; className?: string }) {
	const prefersReducedMotion = useReducedMotion();
	return (
		<AnimatePresence initial={false}>
			{open ? (
				<motion.div
					key="section-body"
					initial={{ gridTemplateRows: "0fr", opacity: 0 }}
					animate={{ gridTemplateRows: "1fr", opacity: 1 }}
					exit={{ gridTemplateRows: "0fr", opacity: 0 }}
					transition={prefersReducedMotion ? { duration: 0 } : { duration: 0.18, ease: [0.25, 0.46, 0.45, 0.94] }}
					style={{ display: "grid" }}
					className={className}
				>
					<div className="flex min-h-0 flex-col overflow-hidden">{children}</div>
				</motion.div>
			) : null}
		</AnimatePresence>
	);
}
