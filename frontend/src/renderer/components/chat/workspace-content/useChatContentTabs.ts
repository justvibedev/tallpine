import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTerminalZoomFullscreen } from "../useTerminalZoomFullscreen";
import { useChatTabKeyboard } from "../useChatTabKeyboard";
import { useUiStore } from "../../../stores/ui-store";
import { brokenMcpServers } from "../../../types/conversation";
import { useChatContentData } from "./useChatContentData";
import { ChatWorkspaceContentProps } from "./chat-content-props";

/** Surface refs, composer wiring, annotation navigation and tab shortcuts for ChatWorkspaceContent. */
export function useChatContentTabs(props: ChatWorkspaceContentProps, data: ReturnType<typeof useChatContentData>) {
	const {
		annotationNavigationRequest,
		snapshot,
		workspaceActiveTabKey,
		newWorkDisabled = false,
		reviewerTerminal,
		onOpenReviewerTerminal,
		reviewerChat,
		onOpenReviewerChat,
		session,
		onSelectChat,
		shellTarget,
		onSelectShellTerminal,
		onCloseShellTerminal,
		onSend,
		onRollback,
		onEditMessage,
		onRetryDraftPersistence,
	} = props;
	const {
		activeWorkspaceTab,
		orderedAuxiliaryTabs,
		reviewerActive,
		shellActive,
		uiSessionId,
		surfaceRef,
		composerFocusRef,
	} = data;
	const [confirming, setConfirming] = useState<string | undefined>(undefined);
	const artifactSession = session?.requestedOutput === "artifact";
	// The sample prompts show only while the composer is empty: picking one hides
	// them, and clearing the draft brings them back.
	const [composerEmpty, setComposerEmpty] = useState(true);
	// Stable identity across renders: latest onSend via ref, defined-ness via hasOnSend.
	const onSendRef = useRef(onSend);
	onSendRef.current = onSend;
	const hasOnSend = Boolean(onSend);
	const promptSender = useMemo(() => (hasOnSend ? (text: string) => onSendRef.current?.(text) : undefined), [hasOnSend]);
	const annotationNavigationRef = useRef<((annotation: { text: string; messageId?: string; revision?: number }) => void) | null>(null);
	const navigateToAnnotation = useCallback((annotation: { text: string; messageId?: string; revision?: number }) => {
		annotationNavigationRef.current?.(annotation);
	}, []);
	useEffect(() => { if (annotationNavigationRequest) navigateToAnnotation(annotationNavigationRequest); }, [annotationNavigationRequest, navigateToAnnotation]);
	// Storage failures can be transient, so try again when the user comes back. Only while
	// the composer is empty: recovering remounts it, which would drop unsaved text.
	useEffect(() => {
		if (!onRetryDraftPersistence) return;
		const retry = () => {
			if (composerFocusRef.current?.isEmpty() !== false) onRetryDraftPersistence();
		};
		window.addEventListener("focus", retry);
		return () => window.removeEventListener("focus", retry);
	}, [onRetryDraftPersistence]);
	const {
		terminalFontSize,
		isFullscreen,
		updateTerminalFontSize,
		handleWheelZoom,
		toggleFullscreen,
	} = useTerminalZoomFullscreen({ surfaceRef });
	const { handleChatTabsKeyDown } = useChatTabKeyboard({
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
	});

	const openPlanTab = useCallback(() => {
		const ui = useUiStore.getState();
		ui.setInspectorView(uiSessionId, "plan");
		ui.setInspectorOpen(uiSessionId, true);
	}, [uiSessionId]);
	// Keep the rollback affordance mounted while a new turn runs; disable it until
	// the daemon can safely accept it so the action row never shifts.
	const rollbackEnabled = Boolean(onRollback) && !newWorkDisabled && snapshot.controller.state !== "hibernated";
	// Stable identity: an inline arrow would defeat the Timeline memo on every render.
	const rollbackTarget = useMemo(() => (rollbackEnabled ? (id: string) => setConfirming(id) : undefined), [rollbackEnabled]);
	const discarded = snapshot.turns.filter((t) => t.rolledBack).length;

	const brokenServers = useMemo(() => brokenMcpServers(snapshot), [snapshot]);
	const reauthErrorInChat = snapshot.turns.some(
		(entry) => entry.state === "failed" && Boolean(entry.errorMessage?.trim()) &&
			entry.errorMessage?.trim() === snapshot.account?.reauthReason?.trim(),
	) || snapshot.items.some(
		(item) => item.kind === "activity" && item.activityKind === "error" &&
			Boolean(item.summary.trim()) && item.summary.trim() === snapshot.account?.reauthReason?.trim(),
	);
	const editHumanMessage = onEditMessage;
	return {
		annotationNavigationRef,
		artifactSession,
		brokenServers,
		composerEmpty,
		confirming,
		discarded,
		editHumanMessage,
		handleChatTabsKeyDown,
		handleWheelZoom,
		isFullscreen,
		navigateToAnnotation,
		openPlanTab,
		promptSender,
		reauthErrorInChat,
		rollbackTarget,
		setComposerEmpty,
		setConfirming,
		terminalFontSize,
		toggleFullscreen,
		updateTerminalFontSize,
	};
}
