import { motion, Reorder } from "motion/react";
import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { useTabListTabStop } from "../hooks/useTabListTabStop";
import { useTabScrollEdges } from "../hooks/useTabScrollEdges";
import { useSidebarChromeClearanceRef } from "../hooks/useSidebarChromeGeometry";
import { handleTerminalTabListKeyDown } from "../lib/terminal-tabs";
import { cn } from "../lib/utils";
import { sidebarOccupiesLayout, useUiStore } from "../stores/ui-store";
import { isLinuxPlatform, isMacPlatform } from "../lib/platform";
import type { TerminalTarget } from "../types/terminal";
import type { CenterPaneProps } from "./CenterPane";
import { AuxiliaryTabItem } from "./CenterPaneAuxTab";
import { DraggableWorkspaceTab, SessionPaneTab, type AuxiliaryTab } from "./CenterPaneTabs";

const isMac = isMacPlatform();
const isLinux = isLinuxPlatform();

export type CenterPaneTopbarProps = Pick<
	CenterPaneProps,
	| "session"
	| "reviewerChat"
	| "onSelectSessionTerminal"
	| "onSelectReviewerTerminal"
	| "onSelectReviewerChat"
	| "onSelectShellTerminal"
	| "onCloseShellTerminal"
	| "onRenameShellTerminal"
	| "sessionTabAction"
	| "workspaceTabActions"
	| "tabStripAction"
	| "topbarActions"
	| "workspaceActiveTabKey"
> & {
	reviewerChatSelected: boolean;
	sessionTabActionWide: boolean;
	sessionId: string | undefined;
	target: TerminalTarget;
	sessionTabLabel: string;
	isFullscreen: boolean;
	terminalBounds: { leftInset: number; rightInset: number; width: number };
	availableAuxiliaryKeys: string[];
	orderedAuxiliaryTabs: AuxiliaryTab[];
	reorderAuxiliaryTabs: (keys: string[]) => void;
	refreshWorkspaces: () => void | Promise<void>;
};

export function CenterPaneTopbar({ reviewerChat, session, sessionId, target, workspaceActiveTabKey, reviewerChatSelected, sessionTabLabel, isFullscreen, terminalBounds, availableAuxiliaryKeys, orderedAuxiliaryTabs, reorderAuxiliaryTabs, refreshWorkspaces, onSelectSessionTerminal, onSelectReviewerTerminal, onSelectReviewerChat, onSelectShellTerminal, onCloseShellTerminal, onRenameShellTerminal, sessionTabAction, sessionTabActionWide, workspaceTabActions, tabStripAction, topbarActions }: CenterPaneTopbarProps) {
	const { t } = useTranslation();
	const tabListRef = useTabListTabStop<HTMLDivElement>();
	const isSidebarOpen = useUiStore(sidebarOccupiesLayout);
	const tabOverflowWatch = `${sessionId ?? ""}|${availableAuxiliaryKeys.join("|")}`;
	const {
		scrollRef: tabsOverflowRef,
		scrollToEnd: scrollTabsToEnd,
		showLeftFade,
		showRightFade,
	} = useTabScrollEdges([isFullscreen, tabOverflowWatch]);
	const previousTabCountRef = useRef(availableAuxiliaryKeys.length);

	useEffect(() => {
		if (availableAuxiliaryKeys.length > previousTabCountRef.current) scrollTabsToEnd();
		previousTabCountRef.current = availableAuxiliaryKeys.length;
	}, [availableAuxiliaryKeys.length, scrollTabsToEnd]);

	const activeAuxiliaryTabKey =
		reviewerChatSelected && reviewerChat
			? `reviewer-chat:${reviewerChat.reviewId}`
			: workspaceActiveTabKey ?? (target.kind === "shell"
				? target.handleId
				: target.kind === "reviewer"
					? `reviewer:${target.handleId}`
					: undefined);
	const orderedAuxiliaryTabKeys = orderedAuxiliaryTabs.map((tab) => tab.key).join("|");
	// Reveal the active tab only when the selection or tab set changes. Keyed on
	// strings, not the tab objects, which are rebuilt on every render and would
	// otherwise yank a manually scrolled strip back to the active tab.
	useEffect(() => {
		const activeKey = activeAuxiliaryTabKey;
		if (!activeKey) return;
		const scrollRegion = tabsOverflowRef.current;
		if (!scrollRegion) return;
		const activeTab = Array.from(
			scrollRegion.querySelectorAll<HTMLElement>("[data-terminal-tab-key]"),
		).find((element) => element.dataset.terminalTabKey === activeKey);
		if (!activeTab) return;
		const scrollRect = scrollRegion.getBoundingClientRect();
		const tabRect = activeTab.getBoundingClientRect();
		let nextScrollLeft = scrollRegion.scrollLeft;
		if (tabRect.left < scrollRect.left) nextScrollLeft -= scrollRect.left - tabRect.left;
		if (tabRect.right > scrollRect.right) nextScrollLeft += tabRect.right - scrollRect.right;
		if (nextScrollLeft === scrollRegion.scrollLeft) return;
		scrollRegion.scrollTo({ behavior: "smooth", left: Math.max(0, nextScrollLeft) });
	}, [activeAuxiliaryTabKey, orderedAuxiliaryTabKeys]);

	const clearanceRef = useSidebarChromeClearanceRef<HTMLDivElement>(!isFullscreen && isMac);
	return (
		<div className="flex h-inspector-tabs w-full shrink-0 items-stretch bg-sidebar">

			<div className="session-topbar-surface flex min-w-0 flex-1" data-testid="session-workspace-topbar">
				<div
					className={cn(
						"flex min-w-0 shrink items-stretch",
						!isFullscreen && isMac && "session-topbar-titlebar-clearance-mac",
						!isFullscreen && !isSidebarOpen && isLinux && "session-topbar-titlebar-clearance-linux",
					)}
					data-testid="session-terminal-region"
					ref={clearanceRef}
					style={{
						width: terminalBounds.width > 0 ? terminalBounds.width : "100%",
					}}
				>
					<div
							aria-label={t("terminal.tabsAria")}
							className="flex h-full min-w-0 flex-1 items-stretch"
							onKeyDown={handleTerminalTabListKeyDown}
							ref={tabListRef}
							role="tablist"
						>
							<div className="relative min-w-0 flex-1 self-stretch overflow-hidden">
								<motion.div
									ref={tabsOverflowRef}
									className="session-tab-scroll-region scrollbar-none flex h-full min-w-flex-min min-w-0 items-stretch overflow-x-auto"
									layoutScroll
								>
								<div className="flex w-max items-stretch">
									{/* The owning session scrolls with its auxiliary terminals, but remains fixed in order. */}
									{session ? (
										<SessionPaneTab
											isActive={target.kind === "worker" && !workspaceActiveTabKey && !reviewerChatSelected}
											label={sessionTabLabel}
											onSelect={onSelectSessionTerminal}
											onRenamed={refreshWorkspaces}
											session={session}
											tabAction={sessionTabAction}
											tabActionWide={sessionTabActionWide}
										/>
									) : (
										<SessionPaneTab isActive={target.kind === "worker"} label={sessionTabLabel} />
									)}
									<Reorder.Group
										as="div"
										axis="x"
										className="flex items-stretch self-stretch"
										onReorder={reorderAuxiliaryTabs}
										values={orderedAuxiliaryTabs.map((tab) => tab.key)}
									>
										{orderedAuxiliaryTabs.map((tab) => (
											<DraggableWorkspaceTab key={tab.key} value={tab.key}>
												<AuxiliaryTabItem
													tab={tab}
													target={target}
													workspaceActiveTabKey={workspaceActiveTabKey}
													reviewerChatSelected={reviewerChatSelected}
													onSelectReviewerTerminal={onSelectReviewerTerminal}
													onSelectReviewerChat={onSelectReviewerChat}
													onSelectShellTerminal={onSelectShellTerminal}
													onCloseShellTerminal={onCloseShellTerminal}
													onRenameShellTerminal={onRenameShellTerminal}
												/>
											</DraggableWorkspaceTab>
										))}
									</Reorder.Group>
									{workspaceTabActions}
								</div>
							</motion.div>
								{showLeftFade ? <div aria-hidden="true" className="session-tab-scroll-fade session-tab-scroll-fade--left" /> : null}
								{showRightFade ? <div aria-hidden="true" className="session-tab-scroll-fade" /> : null}
							</div>
					</div>
				</div>
				{isFullscreen ? null : (
					<div
						className="ml-auto flex shrink-0 items-center gap-1 pl-2 pr-3"
			data-testid="session-action-region"
		>
			{tabStripAction ? <div data-testid="session-tab-strip-action">{tabStripAction}</div> : null}
			{topbarActions}
					</div>
				)}
			</div>
		</div>
	);
}
