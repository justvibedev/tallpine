/** Inspector shell, section and summary views. */
import { type KeyboardEvent, type ReactNode, useId, useState } from "react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { ChevronIcon } from "../icons";
import { NAV_ROW_HIGHLIGHT_HOST_CLASS, NavRowHighlight } from "../NavRowHighlight";
import { cn } from "../utils";
import { InspectorView, InspectorTab, inspectorShellClass, inspectorBodyBaseClass, inspectorScrollableBodyClass, inspectorEmptyClass } from "./inspector-types";

export function SessionInspectorShellView({
	activeView,
	ariaLabel,
	artifactsView,
	browserPoppedOut,
	browserView,
	planView,
	sideChatView,
	filesView,
	headerActions,
	isVisible = true,
	loadingText,
	onViewChange,
	reviewsView,
	summaryView,
	tabs,
}: {
	activeView: InspectorView;
	ariaLabel: string;
	artifactsView?: ReactNode;
	browserPoppedOut: boolean;
	browserView?: ReactNode;
	planView?: ReactNode;
	sideChatView?: ReactNode;
	filesView?: ReactNode;
	headerActions?: ReactNode;
	isVisible?: boolean;
	loadingText?: string;
	onViewChange: (view: InspectorView) => void;
	reviewsView?: ReactNode;
	summaryView?: ReactNode;
	tabs: InspectorTab[];
}) {
	const prefersReducedMotion = useReducedMotion();
	const tabIndicatorTransition = prefersReducedMotion
		? { duration: 0 }
		: { type: "spring" as const, duration: 0.3, bounce: 0 };

	if (loadingText) {
		return (
			<aside className={inspectorShellClass} aria-label={ariaLabel}>
				<div className={cn(inspectorBodyBaseClass, inspectorScrollableBodyClass)}>
					<p className={inspectorEmptyClass}>{loadingText}</p>
				</div>
			</aside>
		);
	}

	const selectAdjacentTab = (event: KeyboardEvent<HTMLButtonElement>, index: number) => {
		let nextIndex: number;
		switch (event.key) {
			case "ArrowLeft":
				nextIndex = (index - 1 + tabs.length) % tabs.length;
				break;
			case "ArrowRight":
				nextIndex = (index + 1) % tabs.length;
				break;
			case "Home":
				nextIndex = 0;
				break;
			case "End":
				nextIndex = tabs.length - 1;
				break;
			default:
				return;
		}
		event.preventDefault();
		onViewChange(tabs[nextIndex].id);
		event.currentTarget.parentElement
			?.querySelectorAll<HTMLButtonElement>('[role="tab"]')
			.item(nextIndex)
			.focus();
	};

	return (
		<aside className={inspectorShellClass} aria-label={ariaLabel}>
			{/* The top-bar row. Browser and Plan switch from the pinned switcher at its right end; this row keeps only the tabs the switcher doesn't own. */}
			<div
				className="session-inspector__topbar flex h-inspector-tabs shrink-0 items-center border-b border-border-strong pl-1"
				data-testid="session-inspector-topbar"
			>
				{isVisible && tabs.length > 0 ? (
					<div
						className="session-inspector__tablist flex min-w-0 flex-1 items-center justify-start gap-1"
						role="tablist"
					>
						{tabs.map((tab, index) => (
							<button
								aria-label={tab.label}
								key={tab.id}
								type="button"
								role="tab"
								aria-selected={activeView === tab.id}
								tabIndex={activeView === tab.id || (index === 0 && !tabs.some((entry) => entry.id === activeView)) ? 0 : -1}
								className={cn(
									"session-inspector__tab-button relative inline-flex shrink-0 items-center justify-center rounded-md font-semibold text-passive transition-[color] duration-fast hover:bg-interactive-hover hover:text-foreground",
									// Active = icon + label; the rest stay square icon-only buttons (titled).
									activeView === tab.id ? "h-control-md gap-1.5 px-2 text-sm text-foreground" : "size-control-md p-0",
								)}
								onClick={() => onViewChange(tab.id)}
								onKeyDown={(event) => selectAdjacentTab(event, index)}
								title={tab.label}
							>
								{activeView === tab.id ? (
									<motion.span
										aria-hidden="true"
										className="absolute inset-0 rounded-md bg-interactive-active"
										initial={false}
										layoutId="session-inspector-tab-indicator"
										transition={tabIndicatorTransition}
									/>
								) : null}
								<span className="relative z-[1] inline-flex shrink-0 [&_svg]:size-icon-md">
									{tab.icon}
									{tab.badge ? (
										<span
											aria-hidden="true"
											className="absolute right-0 top-0 inline-flex size-dot-sm"
											data-testid="browser-unseen-indicator"
										>
											<span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-primary opacity-75" />
											<span className="relative inline-flex size-dot-sm rounded-full bg-primary ring-2 ring-background" />
										</span>
									) : null}
								</span>
								{tab.count ? (
									// Same corner count as the notification bell (NotificationCenter).
									<span
										aria-hidden="true"
										className="pointer-events-none absolute right-px top-px z-[2] grid h-3 min-w-3 place-items-center rounded-full bg-accent-strong px-0.5 font-mono text-[7px] font-semibold leading-none text-accent-foreground shadow-sm ring-1 ring-background"
									>
										{tab.count > 99 ? "99+" : tab.count}
									</span>
								) : null}
								{activeView === tab.id ? (
									<span className="relative z-[1] max-w-32 truncate @max-[300px]/inspector:hidden" data-testid="inspector-active-tab-label">{tab.displayLabel ?? tab.label}</span>
								) : (
									<span className="sr-only">{tab.displayLabel ?? tab.label}</span>
								)}
							</button>
						))}
					</div>
				) : null}
				{isVisible ? headerActions : null}
			</div>

			<div
				aria-hidden={!isVisible}
				className={cn(
					inspectorBodyBaseClass,
					!isVisible && "invisible pointer-events-none",
					activeView !== "browser" && activeView !== "files" && activeView !== "sideChat" && activeView !== "artifacts" && inspectorScrollableBodyClass,
					(activeView === "sideChat" || activeView === "artifacts") && "p-0 overflow-hidden",
					activeView === "browser" &&
						!browserPoppedOut &&
						"session-inspector__body--browser p-0 overflow-hidden [&>[role=tabpanel]]:border-0 [&>[role=tabpanel]]:rounded-none",
					activeView === "files" && "p-0 overflow-hidden [&>[role=tabpanel]]:h-full",
				)}
				inert={!isVisible}
			>
				{activeView === "summary" ? summaryView : null}
				{activeView === "reviews" ? reviewsView : null}
				{activeView === "browser" ? browserView : null}
				{activeView === "sideChat" ? sideChatView : null}
				{activeView === "files" ? filesView : null}
				{activeView === "artifacts" ? artifactsView : null}
				{activeView === "plan" ? planView : null}
			</div>
		</aside>
	);
}

export function InspectorSection({
	children,
	defaultOpen = true,
	surface = true,
	title,
}: {
	children: ReactNode;
	/** Off for reference detail the reader opens on demand (a Setup log). */
	defaultOpen?: boolean;
	surface?: boolean;
	title: string;
}) {
	const contentId = useId();
	const [open, setOpen] = useState(defaultOpen);
	const reduceMotion = useReducedMotion();
	// Header: 6px slot + 6px button inset puts the label 12px from the edge; collapsed headers sit 4px apart.
	return (
		<section className="group/inspector-section flex flex-col" data-testid="inspector-section">
			<div className="px-1.5 py-0.5">
				<button
					aria-controls={contentId}
					aria-expanded={open}
					className={cn(NAV_ROW_HIGHLIGHT_HOST_CLASS, "relative flex w-full items-center rounded-lg p-1.5 text-left text-muted-foreground")}
					onClick={() => setOpen((current) => !current)}
					type="button"
				>
					<NavRowHighlight />
					<span className="relative z-[1] flex w-full min-w-0 items-center justify-between gap-2 text-sm">
						<span className="min-w-0 flex-1">{title}</span>
						<ChevronIcon aria-hidden="true" className="size-icon-2xs shrink-0 text-passive" direction={open ? "down" : "right"} />
					</span>
				</button>
			</div>
			<AnimatePresence initial={false}>
				{open ? (
					// Clips only while animating, so open sections never cut off action menus.
					<motion.div
						animate={{ height: "auto", opacity: 1, transitionEnd: { overflow: "visible" } }}
						exit={{ height: 0, opacity: 0, overflow: "hidden" }}
						initial={{ height: 0, opacity: 0, overflow: "hidden" }}
						transition={reduceMotion ? { duration: 0 } : { duration: 0.22, ease: [0.4, 0, 0.2, 1] }}
					>
						<div className="min-w-0 px-3 pt-0.5 pb-3.5 group-last/inspector-section:pb-0" id={contentId}>
							{surface ? <div className="overflow-hidden bg-settings-row py-1">{children}</div> : children}
						</div>
					</motion.div>
				) : null}
			</AnimatePresence>
		</section>
	);
}

export function SessionInspectorSummaryView({
	activity,
	activityTitle,
	artifactCards,
	artifactTitle,
	branch,
	completion,
	context,
	pullRequestCards,
	pullRequestTitle,
	reviews,
	usage,
}: {
	activity: ReactNode;
	activityTitle: string;
	/** Omit alongside {@link artifactTitle} to skip the artifacts section entirely. */
	artifactCards?: ReactNode;
	artifactTitle?: string;
	branch?: ReactNode;
	completion?: ReactNode;
	context?: ReactNode;
	/** Omit alongside {@link pullRequestTitle} to skip the PR section entirely (e.g. no known PR output). */
	pullRequestCards?: ReactNode;
	pullRequestTitle?: string;
	reviews?: ReactNode;
	usage?: ReactNode;
}) {
	return (
		<div role="tabpanel">
			{context}
			{branch}
			{pullRequestTitle ? (
				<InspectorSection surface={false} title={pullRequestTitle}>
					<div className="flex flex-col gap-1.5">{pullRequestCards}</div>
				</InspectorSection>
			) : null}
			{artifactTitle ? (
				<InspectorSection surface={false} title={artifactTitle}>
					<div className="flex flex-col gap-1.5">{artifactCards}</div>
				</InspectorSection>
			) : null}
			{reviews}
			{completion}
			<InspectorSection title={activityTitle}>{activity}</InspectorSection>
			{usage}
		</div>
	);
}
