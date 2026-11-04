import type { WorkspaceSession } from "../../types/workspace";
import type { ShellTerminal } from "../../hooks/useShellTerminals";
import { useCallback, useEffect, useMemo, useState } from "react";
import { ChatAuxiliaryTab } from "./ChatWorkspaceTabs";
import { WorkspaceTab } from "./ChatWorkspaceTabs";

/**
 * Reviewer, shell and workspace tabs next to the chat, in the user's order.
 */
export function useAuxiliaryTabs({
	reviewerChatSelected,
	reviewerTarget,
	session,
	shellTarget,
	reviewerTerminal,
	reviewerChat,
	shellTerminals,
	workspaceTabs,
	auxiliaryTabOrder,
	uiSessionId,
	workspaceActiveTabKey,
	onAuxiliaryTabOrderChange,
}: {
	reviewerChatSelected: boolean;
	reviewerTarget: { kind: "reviewer"; handleId: string; harness: string; sessionId: string; reviewStatus?: "running" | "complete" | "delivered" | "failed" | "cancelled"; } | undefined;
	session: WorkspaceSession | undefined;
	shellTarget: { generation: string; kind: "shell"; handleId: string; sessionId?: string; title: string; } | undefined;
	reviewerTerminal: { handleId: string; harness: string; } | undefined;
	reviewerChat: { reviewId: string; harness: string; } | undefined;
	shellTerminals: ShellTerminal[] | undefined;
	workspaceTabs: WorkspaceTab[] | undefined;
	auxiliaryTabOrder: string[] | undefined;
	uiSessionId: string;
	workspaceActiveTabKey: string | undefined;
	onAuxiliaryTabOrderChange: ((keys: string[]) => void) | undefined;
}) {
	// Selection is durable UI state; availability only controls whether the tab is
	// offered. Keeping these separate preserves a selected reviewer while an active
	// session temporarily becomes terminated and later returns.
	const reviewerActive = reviewerChatSelected || Boolean(reviewerTarget && session);

	const shellActive = Boolean(shellTarget && session);

	const auxiliaryTabs = useMemo<ChatAuxiliaryTab[]>(
		() => [
			...(reviewerTerminal
				? [{ key: `reviewer:${reviewerTerminal.handleId}`, kind: "reviewer" as const, terminal: reviewerTerminal }]
				: []),
			...(!reviewerTerminal && reviewerChat
				? [{ key: `reviewer-chat:${reviewerChat.reviewId}`, kind: "reviewer-chat" as const, terminal: reviewerChat }]
				: []),
			...(shellTerminals ?? []).map((terminal) => ({ key: terminal.handleId, kind: "shell" as const, terminal })),
			...(workspaceTabs ?? []).map((tab) => ({ key: tab.key, kind: "workspace" as const, tab })),
		],
		[reviewerChat, reviewerTerminal, shellTerminals, workspaceTabs],
	);

	const availableTabKeys = useMemo(() => auxiliaryTabs.map((tab) => tab.key), [auxiliaryTabs]);

	const [tabOrderBySession, setTabOrderBySession] = useState<Record<string, string[]>>({});

	const orderedAuxiliaryTabs = useMemo(() => {
		const preferred = auxiliaryTabOrder ?? tabOrderBySession[uiSessionId] ?? [];
		const byKey = new Map(auxiliaryTabs.map((tab) => [tab.key, tab]));
		const ordered = preferred.flatMap((key) => {
			const tab = byKey.get(key);
			if (!tab) return [];
			byKey.delete(key);
			return [tab];
		});
		return [...ordered, ...byKey.values()];
	}, [auxiliaryTabOrder, auxiliaryTabs, uiSessionId, tabOrderBySession]);

	const activeWorkspaceTab = workspaceActiveTabKey
		? workspaceTabs?.find((tab) => tab.key === workspaceActiveTabKey)
		: undefined;

	const reorderAuxiliaryTabs = useCallback(
		(nextKeys: string[]) => {
			const available = new Set(availableTabKeys);
			const next = nextKeys.filter((key, index) => available.has(key) && nextKeys.indexOf(key) === index);
			for (const key of availableTabKeys) {
				if (!next.includes(key)) next.push(key);
			}
			if (onAuxiliaryTabOrderChange) onAuxiliaryTabOrderChange(next);
			else setTabOrderBySession((current) => ({ ...current, [uiSessionId]: next }));
		},
		[availableTabKeys, onAuxiliaryTabOrderChange, uiSessionId],
	);

	useEffect(() => {
		if (auxiliaryTabOrder) {
			const available = new Set(availableTabKeys);
			const next = auxiliaryTabOrder.filter((key) => available.has(key));
			for (const key of availableTabKeys) {
				if (!next.includes(key)) next.push(key);
			}
			if (!next.every((key, index) => key === auxiliaryTabOrder[index]) || next.length !== auxiliaryTabOrder.length) {
				onAuxiliaryTabOrderChange?.(next);
			}
			return;
		}
		setTabOrderBySession((current) => {
			const currentOrder = current[uiSessionId] ?? [];
			const available = new Set(availableTabKeys);
			const next = currentOrder.filter((key) => available.has(key));
			for (const key of availableTabKeys) {
				if (!next.includes(key)) next.push(key);
			}
			if (next.length === currentOrder.length && next.every((key, index) => key === currentOrder[index])) return current;
			if (next.length === 0) {
				const { [uiSessionId]: _removed, ...rest } = current;
				return rest;
			}
			return { ...current, [uiSessionId]: next };
		});
	}, [auxiliaryTabOrder, availableTabKeys, onAuxiliaryTabOrderChange, uiSessionId]);

	return {
		reviewerActive,
		shellActive,
		orderedAuxiliaryTabs,
		activeWorkspaceTab,
		reorderAuxiliaryTabs,
	};
}
