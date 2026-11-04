import { isSettingsPageOpen } from "../hooks/useAppShortcut";
import { useCallback, useEffect, useMemo, useState } from "react";
import { tallpineBridge } from "../lib/bridge";
import type { CenterPaneProps } from "./CenterPane";
import type { TerminalTarget } from "../types/terminal";
import { type AuxiliaryTab } from "./CenterPaneTabs";

type AuxiliaryTabsOptions = Pick<
	CenterPaneProps,
	| "reviewerTerminal"
	| "reviewerChat"
	| "shellTerminals"
	| "workspaceTabs"
	| "auxiliaryTabOrder"
	| "onAuxiliaryTabOrderChange"
	| "reviewerChatSelected"
	| "workspaceActiveTabKey"
	| "onSelectSessionTerminal"
	| "onSelectReviewerTerminal"
	| "onSelectReviewerChat"
	| "onSelectShellTerminal"
> & { sessionId: string | undefined; target: TerminalTarget };

export function useAuxiliaryTabs({ sessionId, target, reviewerTerminal, reviewerChat, shellTerminals = [], workspaceTabs, auxiliaryTabOrder, onAuxiliaryTabOrderChange, reviewerChatSelected, workspaceActiveTabKey, onSelectSessionTerminal, onSelectReviewerTerminal, onSelectReviewerChat, onSelectShellTerminal }: AuxiliaryTabsOptions) {
	const [tabOrderBySession, setTabOrderBySession] = useState<Record<string, string[]>>({});
	const auxiliaryTabs = useMemo<AuxiliaryTab[]>(
		() => [
			...(reviewerTerminal
				? [
						{
							key: `reviewer:${reviewerTerminal.handleId}`,
							kind: "reviewer" as const,
							terminal: reviewerTerminal,
						},
					]
				: []),
			...(!reviewerTerminal && reviewerChat
				? [{ key: `reviewer-chat:${reviewerChat.reviewId}`, kind: "reviewer-chat" as const, terminal: reviewerChat }]
				: []),
			...shellTerminals.map((terminal) => ({ key: terminal.handleId, kind: "shell" as const, terminal })),
			...(workspaceTabs ?? []).map((tab) => ({ key: tab.key, kind: "workspace" as const, tab })),
		],
		[reviewerChat, reviewerTerminal, shellTerminals, workspaceTabs],
	);
	const availableAuxiliaryKeys = useMemo(() => auxiliaryTabs.map((tab) => tab.key), [auxiliaryTabs]);
	const orderedAuxiliaryTabs = useMemo(() => {
		const preferred = auxiliaryTabOrder ?? (sessionId ? (tabOrderBySession[sessionId] ?? []) : []);
		const byKey = new Map(auxiliaryTabs.map((tab) => [tab.key, tab]));
		const ordered = preferred.flatMap((key) => {
			const tab = byKey.get(key);
			if (!tab) return [];
			byKey.delete(key);
			return [tab];
		});
		return [...ordered, ...byKey.values()];
	}, [auxiliaryTabOrder, auxiliaryTabs, sessionId, tabOrderBySession]);
	const reorderAuxiliaryTabs = useCallback(
		(nextKeys: string[]) => {
			if (!sessionId) return;
			const available = new Set(availableAuxiliaryKeys);
			const next = nextKeys.filter((key, index) => available.has(key) && nextKeys.indexOf(key) === index);
			for (const key of availableAuxiliaryKeys) {
				if (!next.includes(key)) next.push(key);
			}
			if (onAuxiliaryTabOrderChange) onAuxiliaryTabOrderChange(next);
			else setTabOrderBySession((current) => ({ ...current, [sessionId]: next }));
		},
		[availableAuxiliaryKeys, onAuxiliaryTabOrderChange, sessionId],
	);
	const selectAdjacentTab = useCallback(
		(direction: -1 | 1) => {
			const activeKey =
				reviewerChatSelected && reviewerChat
					? `reviewer-chat:${reviewerChat.reviewId}`
					: workspaceActiveTabKey ?? (target.kind === "shell"
						? target.handleId
						: target.kind === "reviewer"
							? `reviewer:${target.handleId}`
							: "worker");
			const tabKeys = ["worker", ...orderedAuxiliaryTabs.map((tab) => tab.key)];
			const activeIndex = Math.max(0, tabKeys.indexOf(activeKey));
			const nextIndex = (activeIndex + direction + tabKeys.length) % tabKeys.length;
			if (nextIndex === 0) {
				onSelectSessionTerminal?.();
				return;
			}
			const nextTab = orderedAuxiliaryTabs[nextIndex - 1];
			if (nextTab?.kind === "reviewer") onSelectReviewerTerminal?.(nextTab.terminal);
			if (nextTab?.kind === "reviewer-chat") onSelectReviewerChat?.(nextTab.terminal);
			if (nextTab?.kind === "shell") onSelectShellTerminal?.(nextTab.terminal.handleId);
			if (nextTab?.kind === "workspace") nextTab.tab.onSelect();
		},
		[
			onSelectReviewerChat,
			onSelectReviewerTerminal,
			onSelectSessionTerminal,
			onSelectShellTerminal,
			orderedAuxiliaryTabs,
			reviewerChat,
			reviewerChatSelected,
			target,
			workspaceActiveTabKey,
		],
	);

	useEffect(() => {
		if (!sessionId) return;
		if (auxiliaryTabOrder) {
			const available = new Set(availableAuxiliaryKeys);
			const keys = auxiliaryTabOrder.filter((key) => available.has(key));
			for (const key of availableAuxiliaryKeys) {
				if (!keys.includes(key)) keys.push(key);
			}
			if (!keys.every((key, index) => key === auxiliaryTabOrder[index]) || keys.length !== auxiliaryTabOrder.length) {
				onAuxiliaryTabOrderChange?.(keys);
			}
			return;
		}
		setTabOrderBySession((current) => {
			const currentOrder = current[sessionId] ?? [];
			const available = new Set(availableAuxiliaryKeys);
			const keys = currentOrder.filter((key) => available.has(key));
			for (const key of availableAuxiliaryKeys) {
				if (!keys.includes(key)) keys.push(key);
			}
			if (keys.length === currentOrder.length && keys.every((key, index) => key === currentOrder[index])) return current;
			if (keys.length === 0) {
				const { [sessionId]: _removed, ...rest } = current;
				return rest;
			}
			return { ...current, [sessionId]: keys };
		});
	}, [auxiliaryTabOrder, availableAuxiliaryKeys, onAuxiliaryTabOrderChange, sessionId]);

	useEffect(() => {
		const disposePrevious = tallpineBridge.app.onPreviousTabShortcut(() => !isSettingsPageOpen() && selectAdjacentTab(-1));
		const disposeNext = tallpineBridge.app.onNextTabShortcut(() => !isSettingsPageOpen() && selectAdjacentTab(1));
		return () => {
			disposePrevious();
			disposeNext();
		};
	}, [selectAdjacentTab]);

	return { availableAuxiliaryKeys, orderedAuxiliaryTabs, reorderAuxiliaryTabs };
}
