import { isSettingsPageOpen } from "../../hooks/useAppShortcut";
import { useCallback, useEffect, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { tallpineBridge } from "../../lib/bridge";
import { handleTerminalTabListKeyDown } from "../../lib/terminal-tabs";
import { ChatAuxiliaryTab } from "./ChatWorkspaceTabs";
import { WorkspaceTab } from "./ChatWorkspaceTabs";

/**
 * Keyboard and desktop-shortcut navigation between the chat's tabs.
 */
export function useChatTabKeyboard({
	orderedAuxiliaryTabs,
	workspaceActiveTabKey,
	shellActive,
	shellTarget,
	reviewerActive,
	reviewerTerminal,
	reviewerChat,
	onSelectChat,
	onOpenReviewerTerminal,
	onOpenReviewerChat,
	onSelectShellTerminal,
	activeWorkspaceTab,
	onCloseShellTerminal,
}: {
	orderedAuxiliaryTabs: ChatAuxiliaryTab[];
	workspaceActiveTabKey: string | undefined;
	shellActive: boolean;
	shellTarget: { generation: string; kind: "shell"; handleId: string; sessionId?: string; title: string; } | undefined;
	reviewerActive: boolean;
	reviewerTerminal: { handleId: string; harness: string; } | undefined;
	reviewerChat: { reviewId: string; harness: string; } | undefined;
	onSelectChat: (() => void) | undefined;
	onOpenReviewerTerminal: ((target: { handleId: string; harness: string; }) => void) | undefined;
	onOpenReviewerChat: ((target: { reviewId: string; harness: string; }) => void) | undefined;
	onSelectShellTerminal: ((handleId: string) => void) | undefined;
	activeWorkspaceTab: WorkspaceTab | undefined;
	onCloseShellTerminal: ((handleId: string) => void) | undefined;
}) {
	// Cycle chat → reviewer → shells in strip order, wrapping. With no auxiliary
	// tabs there is nothing to cycle to and the shortcut is a no-op.
	const selectAdjacentTab = useCallback(
		(direction: -1 | 1) => {
			const tabs = [{ key: "chat", kind: "chat" as const }, ...orderedAuxiliaryTabs];
			if (tabs.length <= 1) return;
			const activeKey = workspaceActiveTabKey ?? (shellActive
				? shellTarget?.handleId
				: reviewerActive
					? reviewerTerminal
						? `reviewer:${reviewerTerminal.handleId}`
						: reviewerChat
							? `reviewer-chat:${reviewerChat.reviewId}`
							: "chat"
					: "chat");
			const activeIndex = tabs.findIndex((tab) => tab.key === activeKey);
			const currentIndex = activeIndex >= 0 ? activeIndex : 0;
			const next = tabs[(currentIndex + direction + tabs.length) % tabs.length];
			if (!next) return;
			if (next.kind === "chat") {
				onSelectChat?.();
				return;
			}
			if (next.kind === "reviewer") {
				onOpenReviewerTerminal?.(next.terminal);
				return;
			}
			if (next.kind === "reviewer-chat") {
				onOpenReviewerChat?.(next.terminal);
				return;
			}
			if (next.kind === "shell") {
				onSelectShellTerminal?.(next.terminal.handleId);
				return;
			}
			next.tab.onSelect();
		},
		[
			onOpenReviewerTerminal,
			onOpenReviewerChat,
			onSelectChat,
			onSelectShellTerminal,
			reviewerActive,
			reviewerChat,
			reviewerTerminal,
			orderedAuxiliaryTabs,
			shellActive,
			shellTarget,
			workspaceActiveTabKey,
		],
	);

	const handleChatTabsKeyDown = useCallback(
		(event: ReactKeyboardEvent<HTMLDivElement>) => {
			if (
				event.target instanceof HTMLElement &&
				event.target.getAttribute("role") === "tab" &&
				event.key === "Tab" &&
				event.ctrlKey &&
				!event.altKey &&
				!event.metaKey
			) {
				event.preventDefault();
				selectAdjacentTab(event.shiftKey ? -1 : 1);
				return;
			}
			handleTerminalTabListKeyDown(event);
		},
		[selectAdjacentTab],
	);

	useEffect(
		() =>
			tallpineBridge.app.onCloseShellTerminalShortcut(() => {
				if (activeWorkspaceTab?.onClose) activeWorkspaceTab.onClose();
				else if (shellTarget) onCloseShellTerminal?.(shellTarget.handleId);
			}),
		[activeWorkspaceTab, onCloseShellTerminal, shellTarget],
	);

	useEffect(() => {
		const disposePrevious = tallpineBridge.app.onPreviousTabShortcut(() => !isSettingsPageOpen() && selectAdjacentTab(-1));
		const disposeNext = tallpineBridge.app.onNextTabShortcut(() => !isSettingsPageOpen() && selectAdjacentTab(1));
		return () => {
			disposePrevious();
			disposeNext();
		};
	}, [selectAdjacentTab]);

	useEffect(() => {
		tallpineBridge.app.setCloseShellTerminalShortcutEnabled(
			Boolean(activeWorkspaceTab?.onClose) || Boolean(shellTarget && onCloseShellTerminal),
		);
		return () => tallpineBridge.app.setCloseShellTerminalShortcutEnabled(false);
	}, [activeWorkspaceTab, onCloseShellTerminal, shellTarget]);

	return { handleChatTabsKeyDown };
}
