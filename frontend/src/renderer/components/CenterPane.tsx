import { useEffect, useRef, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { centerPaneLabels } from "./center-pane-labels";
import { useCenterPaneHost } from "./useCenterPaneHost";
import { useAuxiliaryTabs } from "./useAuxiliaryTabs";
import { useCenterPaneChrome } from "./useCenterPaneChrome";
import { CenterPaneTopbar } from "./CenterPaneTopbar";
import type { ShellTerminal } from "../hooks/useShellTerminals";
import { tallpineBridge } from "../lib/bridge";
import { type Theme } from "../stores/ui-store";
import type { TerminalTarget } from "../types/terminal";
import { type WorkspaceSession } from "../types/workspace";
import { TerminalPane } from "./TerminalPane";
import { SessionTopbarPortal } from "./SessionTopbarPortal";

export type CenterPaneProps = {
	session?: WorkspaceSession;
	hostId?: string;
	terminalGeneration?: string;
	theme: Theme;
	daemonReady: boolean;
	terminalTarget?: TerminalTarget;
	reviewerTerminal?: { handleId: string; harness: string };
	onSelectReviewerTerminal?: (target: { handleId: string; harness: string }) => void;
	reviewerChat?: { reviewId: string; harness: string };
	reviewerChatSelected?: boolean;
	reviewerChatContent?: ReactNode;
	onSelectReviewerChat?: (target: { reviewId: string; harness: string }) => void;
	/** Standalone shells to render as tabs beside the session's own pane. */
	shellTerminals?: ShellTerminal[];
	onSelectSessionTerminal?: () => void;
	onSelectShellTerminal?: (handleId: string) => void;
	onCloseShellTerminal?: (handleId: string) => void;
	onRenameShellTerminal?: (handleId: string, title: string) => void;
	/** Workspace-level controls (e.g. shell topbar) rendered beside the tab strip. */
	topbarActions?: ReactNode;
	/** Agent-session actions (interface switch, handoff) on the primary session tab. */
	sessionTabAction?: ReactNode;
	/** Widen the primary tab's action slot while an interface switch spinner is showing. */
	sessionTabActionWide?: boolean;
	/** Pinned beside the tab strip, before the workspace topbar actions. */
	tabStripAction?: ReactNode;
	workspaceTabs?: CenterPaneWorkspaceTab[];
	workspaceTabActions?: ReactNode;
	workspaceActiveTabKey?: string;
	/** Session-owned order shared with the Chat surface. */
	auxiliaryTabOrder?: string[];
	onAuxiliaryTabOrderChange?: (keys: string[]) => void;
	/** Stop forwarding the agent pane's keystrokes while its controller drains. */
	agentInputDisabled?: boolean;
	/** The agent terminal is coming up after an interface switch. */
	terminalBooting?: boolean;
	/** An account move is restarting the worker agent; see TerminalPane. */
	terminalSwitchingLabel?: string;
};

export type CenterPaneWorkspaceTab = {
	key: string;
	content: ReactNode;
	onSelect: () => void;
	onClose?: () => void;
};



export function CenterPane({
	session,
	hostId,
	terminalGeneration,
	theme,
	daemonReady,
	terminalTarget,
	reviewerTerminal,
	reviewerChat,
	reviewerChatSelected = false,
	reviewerChatContent,
	onSelectReviewerTerminal,
	onSelectReviewerChat,
	shellTerminals = [],
	onSelectSessionTerminal,
	onSelectShellTerminal,
	onCloseShellTerminal,
	onRenameShellTerminal,
	topbarActions,
	sessionTabAction,
	sessionTabActionWide = false,
	tabStripAction,
	workspaceTabs,
	workspaceTabActions,
	workspaceActiveTabKey,
	auxiliaryTabOrder,
	onAuxiliaryTabOrderChange,
	agentInputDisabled = false,
	terminalBooting = false,
	terminalSwitchingLabel,
}: CenterPaneProps) {
	const { t } = useTranslation();
	const paneRef = useRef<HTMLDivElement | null>(null);
	const { fontSize, isFullscreen, terminalBounds, updateFontSize, toggleFullscreen, handleWheelZoom } = useCenterPaneChrome(paneRef);
	const { refreshWorkspaces, remoteBase, remoteCreateMux } = useCenterPaneHost(hostId);
	const sessionId = session?.id;
	const target = terminalTarget ?? { kind: "worker" };
	const { availableAuxiliaryKeys, orderedAuxiliaryTabs, reorderAuxiliaryTabs } = useAuxiliaryTabs({
		sessionId, target, reviewerTerminal, reviewerChat, shellTerminals, workspaceTabs, auxiliaryTabOrder,
		onAuxiliaryTabOrderChange, reviewerChatSelected, workspaceActiveTabKey, onSelectSessionTerminal,
		onSelectReviewerTerminal, onSelectReviewerChat, onSelectShellTerminal,
	});
	const activeWorkspaceTab = workspaceActiveTabKey
		? workspaceTabs?.find((tab) => tab.key === workspaceActiveTabKey)
		: undefined;
	const workerInputDisabled = target.kind === "worker" && agentInputDisabled;
	const { sessionTabLabel, activeTerminalLabel } = centerPaneLabels({ t, session, target, reviewerChat, reviewerChatSelected, shellTerminals });



	useEffect(
		() =>
			tallpineBridge.app.onCloseShellTerminalShortcut(() => {
				if (activeWorkspaceTab?.onClose) activeWorkspaceTab.onClose();
				else if (target.kind === "shell") onCloseShellTerminal?.(target.handleId);
			}),
		[activeWorkspaceTab, target, onCloseShellTerminal],
	);


	useEffect(() => {
		tallpineBridge.app.setCloseShellTerminalShortcutEnabled(
			Boolean(activeWorkspaceTab?.onClose) || (target.kind === "shell" && Boolean(onCloseShellTerminal)),
		);
		return () => tallpineBridge.app.setCloseShellTerminalShortcutEnabled(false);
	}, [activeWorkspaceTab, target.kind, onCloseShellTerminal]);



	const terminalTopbar = (
		<CenterPaneTopbar
			session={session}
			sessionId={sessionId}
			reviewerChat={reviewerChat}
			target={target}
			workspaceActiveTabKey={workspaceActiveTabKey}
			reviewerChatSelected={reviewerChatSelected}
			sessionTabLabel={sessionTabLabel}
			isFullscreen={isFullscreen}
			terminalBounds={terminalBounds}
			availableAuxiliaryKeys={availableAuxiliaryKeys}
			orderedAuxiliaryTabs={orderedAuxiliaryTabs}
			reorderAuxiliaryTabs={reorderAuxiliaryTabs}
			refreshWorkspaces={refreshWorkspaces}
			onSelectSessionTerminal={onSelectSessionTerminal}
			onSelectReviewerTerminal={onSelectReviewerTerminal}
			onSelectReviewerChat={onSelectReviewerChat}
			onSelectShellTerminal={onSelectShellTerminal}
			onCloseShellTerminal={onCloseShellTerminal}
			onRenameShellTerminal={onRenameShellTerminal}
			sessionTabAction={sessionTabAction}
			sessionTabActionWide={sessionTabActionWide}
			workspaceTabActions={workspaceTabActions}
			tabStripAction={tabStripAction}
			topbarActions={topbarActions}
		/>
	);

	return (
		<div
			ref={paneRef}
			className="terminal-pane-frame flex h-full min-h-0 min-w-flex-min flex-col"
			onWheelCapture={handleWheelZoom}
		>
			{isFullscreen ? terminalTopbar : <SessionTopbarPortal>{terminalTopbar}</SessionTopbarPortal>}
			<div
				aria-label={t("terminal.panelAria", { title: activeTerminalLabel })}
				className="relative min-h-0 flex-1"
				role="tabpanel"
			>
				{reviewerChatSelected && reviewerChatContent ? reviewerChatContent : (
					<div
						className="h-full min-h-0"
						data-testid="terminal-interaction-surface"
						inert={workerInputDisabled ? true : undefined}
					>
						{hostId && !remoteBase ? null : <TerminalPane
							key={hostId ? `${hostId}:${remoteBase}` : undefined}
							createMux={hostId ? remoteCreateMux : undefined}
							daemonReady={hostId ? Boolean(remoteBase) : daemonReady}
							fontSize={fontSize}
						// A terminal you can type into should already hold the caret when you
						// open or switch to the session, the same way the chat composer does.
						// Worker input is off during an interface transition; every other target
						// is interactive as soon as it is on screen.
							focusRequested={target.kind !== "worker" || !workerInputDisabled}
							isFullscreen={isFullscreen}
							inputDisabled={workerInputDisabled}
							booting={terminalBooting && target.kind === "worker"}
							switchingLabel={target.kind === "worker" ? terminalSwitchingLabel : undefined}
							onChangeFontSize={updateFontSize}
							onToggleFullscreen={toggleFullscreen}
							session={session}
							terminalGeneration={terminalGeneration}
							terminalTarget={target}
							theme={theme}
						/>}
					</div>
				)}
			</div>
		</div>
	);
}
