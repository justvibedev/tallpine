import { useTabListTabStop } from "../../hooks/useTabListTabStop";
import { useEffect, useRef, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from "react";
import { motion, Reorder } from "motion/react";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import { useTabScrollEdges } from "../../hooks/useTabScrollEdges";
import { useSidebarChromeClearanceRef } from "../../hooks/useSidebarChromeGeometry";
import { isLinuxPlatform, isMacPlatform } from "../../lib/platform";
import { handleTerminalTabListKeyDown } from "../../lib/terminal-tabs";
import { agentLabel } from "../../lib/agent-options";
import { sidebarOccupiesLayout, useUiStore } from "../../stores/ui-store";
import { isOrchestratorSession, type SessionKind, type WorkspaceSession } from "../../types/workspace";
import { AgentAvatar } from "../AgentAvatar";
import { SessionPaneTab } from "../CenterPaneTabs";
import { SessionTopbarPortal } from "../SessionTopbarPortal";
import { type ConversationSnapshot } from "../../types/conversation";
import { ChatAuxiliaryTab, DraggableChatTab, ShellTerminalTabEntry } from "./ChatWorkspaceTabs";
import { useStableCallback } from "./useStableCallback";
import { JustMeToggle } from "./JustMeToggle";

const isMac = isMacPlatform();

const isLinux = isLinuxPlatform();

export function ChatHeader({
	snapshot,
	sessionTitle,
	sessionRole,
	onOpenReviewerTerminal,
	onOpenReviewerChat,
	reviewerActive,
	onSelectChat,
	shellActiveHandleId,
	onSelectShellTerminal,
	onCloseShellTerminal,
	onRenameShellTerminal,
	onTabsKeyDown,
	headerActions,
	sessionTabAction,
	sessionTabActionWide = false,
	tabStripAction,
	workspaceTabActions,
	workspaceActiveTabKey,
	orderedAuxiliaryTabs,
	onReorderAuxiliaryTabs,
	inline,
	session,
	onSessionRenamed,
}: {
	snapshot: ConversationSnapshot;
	sessionTitle?: string;
	sessionRole: SessionKind;
	onOpenReviewerTerminal?: (target: { handleId: string; harness: string }) => void;
	onOpenReviewerChat?: (target: { reviewId: string; harness: string }) => void;
	/** The reviewer tab is selected; the chat tab is the clickable alternative. */
	reviewerActive?: boolean;
	/** Return the tab strip to the chat tab. */
	onSelectChat?: () => void;
	/** The selected shell tab, if any. */
	shellActiveHandleId?: string;
	onSelectShellTerminal?: (handleId: string) => void;
	onCloseShellTerminal?: (handleId: string) => void;
	onRenameShellTerminal?: (handleId: string, title: string) => void;
	onTabsKeyDown?: (event: ReactKeyboardEvent<HTMLDivElement>) => void;
	headerActions?: ReactNode;
	sessionTabAction?: ReactNode;
	sessionTabActionWide?: boolean;
	tabStripAction?: ReactNode;
	workspaceTabActions?: ReactNode;
	workspaceActiveTabKey?: string;
	orderedAuxiliaryTabs: ChatAuxiliaryTab[];
	onReorderAuxiliaryTabs: (keys: string[]) => void;
	session?: WorkspaceSession;
	onSessionRenamed?: () => void | Promise<void>;
	/** Fullscreen content cannot see the normal topbar portal outside its subtree. */
	inline?: boolean;
}) {
	const { t } = useTranslation();
	const providerLabel = agentLabel(snapshot.harness);
	const sessionIsOrchestrator = session
		? isOrchestratorSession(session)
		: sessionRole === "orchestrator";
	const label = sessionIsOrchestrator
		? t("shell.orchestrator")
		: (sessionTitle || session?.title || snapshot.title || snapshot.sessionId);
	const tabScrollWatch = `${session?.id ?? ""}|${orderedAuxiliaryTabs.map((tab) => tab.key).join("|")}`;
	const {
		scrollRef: tabsOverflowRef,
		scrollToEnd: scrollTabsToEnd,
		showLeftFade,
		showRightFade,
	} = useTabScrollEdges([tabScrollWatch]);
	const previousTabCountRef = useRef(orderedAuxiliaryTabs.length);
	useEffect(() => {
		if (orderedAuxiliaryTabs.length > previousTabCountRef.current) scrollTabsToEnd();
		previousTabCountRef.current = orderedAuxiliaryTabs.length;
	}, [orderedAuxiliaryTabs.length, scrollTabsToEnd]);
	const selectShellTerminal = useStableCallback(onSelectShellTerminal);
	const closeShellTerminal = useStableCallback(onCloseShellTerminal);
	const renameShellTerminal = useStableCallback(onRenameShellTerminal);
	// The chat tab is "selected" only when neither terminal pane is the body.
	const timelineActive = !workspaceActiveTabKey && !reviewerActive && !shellActiveHandleId;
	// Match CenterPane: when the sidebar is off-canvas, the fixed TitlebarNav
	// cluster sits over the session tab strip. Terminal already reserves that
	// space; chat must too or the back/forward buttons land on the tab label.
	const isSidebarOpen = useUiStore(sidebarOccupiesLayout);
	const clearanceRef = useSidebarChromeClearanceRef<HTMLDivElement>(isMac);
	const tabListRef = useTabListTabStop<HTMLDivElement>();
	const header = (
		<header className="flex h-inspector-tabs w-full shrink-0 items-stretch bg-sidebar">
			<div
				className="session-topbar-surface flex min-w-0 flex-1"
				data-testid="session-workspace-topbar"
			>
				<div
					className={cn(
						"flex min-w-0 shrink items-stretch",
						isMac && "session-topbar-titlebar-clearance-mac",
						!isSidebarOpen && isLinux && "session-topbar-titlebar-clearance-linux",
					)}
					data-testid="session-terminal-region"
					ref={clearanceRef}
					style={{ width: "100%" }}
				>
					<div
						aria-label={t("chat.tabsAria")}
						className="flex h-full min-w-0 flex-1 items-stretch"
						onKeyDown={onTabsKeyDown ?? handleTerminalTabListKeyDown}
						ref={tabListRef}
						role="tablist"
					>
						{session ? (
							<SessionPaneTab
								isActive={timelineActive}
								label={label}
								onSelect={timelineActive ? undefined : onSelectChat}
								onRenamed={onSessionRenamed}
								session={session}
								tabAction={sessionTabAction}
								tabActionWide={sessionTabActionWide}
							/>
						) : (
							<button
								aria-current={timelineActive ? true : undefined}
								aria-label={`${label} · ${providerLabel}`}
								aria-selected={timelineActive}
								data-terminal-role="primary"
								className={cn(
									"group relative inline-flex min-w-shell-tab-min max-w-shell-tab-max shrink-0 self-stretch cursor-pointer items-center gap-1.5 border-r border-border px-3 text-control font-medium leading-none transition-colors focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent/50",
									timelineActive
										? "bg-overlay text-foreground after:absolute after:inset-x-0 after:bottom-0 after:h-0.5 after:bg-foreground/80"
										: "text-muted-foreground hover:bg-raised hover:text-foreground",
								)}
								onClick={timelineActive ? undefined : onSelectChat}
								role="tab"
								tabIndex={timelineActive || orderedAuxiliaryTabs.length === 0 ? 0 : -1}
								title={label}
								type="button"
							>
								<AgentAvatar className="size-icon-base" decorative provider={snapshot.harness} />
								<span className="truncate">{label}</span>
							</button>
						)}
						<div className="relative min-w-0 flex-1 self-stretch overflow-hidden">
							<motion.div ref={tabsOverflowRef} className="scrollbar-none flex h-full min-w-flex-min min-w-0 items-stretch overflow-x-auto" layoutScroll>
								<div className="flex w-max items-stretch">
								<Reorder.Group
									as="div"
									axis="x"
									className="flex items-stretch self-stretch"
									onReorder={onReorderAuxiliaryTabs}
									values={orderedAuxiliaryTabs.map((tab) => tab.key)}
								>
									{orderedAuxiliaryTabs.map((tab) => tab.kind === "shell" ? (
										<ShellTerminalTabEntry
											isActive={tab.terminal.handleId === shellActiveHandleId && !workspaceActiveTabKey}
											key={tab.key}
											onClose={closeShellTerminal}
											onRename={onRenameShellTerminal ? renameShellTerminal : undefined}
											onSelect={selectShellTerminal}
											shell={tab.terminal}
											tabKey={tab.key}
										/>
									) : (
										<DraggableChatTab key={tab.key} value={tab.key}>
											{tab.kind === "reviewer" || tab.kind === "reviewer-chat" ? (
												<button
													aria-current={reviewerActive && !workspaceActiveTabKey ? true : undefined}
													aria-label={t("terminal.reviewer")}
													aria-selected={Boolean(reviewerActive && !workspaceActiveTabKey)}
													className={cn(
														"group relative inline-flex min-w-shell-tab-min max-w-shell-tab-max self-stretch cursor-pointer items-center gap-1.5 border-r border-border px-3 text-control font-medium leading-none transition-colors focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent/50",
														reviewerActive && !workspaceActiveTabKey
															? "bg-overlay text-foreground after:absolute after:inset-x-0 after:bottom-0 after:h-0.5 after:bg-foreground/80"
															: "text-muted-foreground hover:bg-raised hover:text-foreground",
													)}
													onClick={() => tab.kind === "reviewer" ? onOpenReviewerTerminal?.(tab.terminal) : onOpenReviewerChat?.(tab.terminal)}
													role="tab"
													tabIndex={reviewerActive && !workspaceActiveTabKey ? 0 : -1}
													title={tab.terminal.harness}
													type="button"
												>
													<AgentAvatar className="size-icon-base" decorative provider={tab.terminal.harness} />
													<span className="truncate">{t("terminal.reviewer")}</span>
												</button>
											) : (
												tab.tab.content
											)}
										</DraggableChatTab>
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
				<div
					className="ml-auto flex shrink-0 items-center gap-1 pl-2 pr-3"
					data-testid="session-action-region"
				>
					{timelineActive ? <JustMeToggle /> : null}
					{tabStripAction ? <div data-testid="session-tab-strip-action">{tabStripAction}</div> : null}
					{headerActions}
				</div>
			</div>
		</header>
	);
	return inline ? header : <SessionTopbarPortal>{header}</SessionTopbarPortal>;
}
