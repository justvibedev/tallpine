import { type ChatComposerHandle } from "../composer/composer-model";
import { useCallback, useRef, type KeyboardEvent as ReactKeyboardEvent, type MouseEvent as ReactMouseEvent } from "react";
import { isDialogOrMenuOpen } from "../../../lib/dom-selectors";
import { useChatStartup } from "../useChatStartup";
import { useAuxiliaryTabs } from "../useAuxiliaryTabs";
import { useQueuedMessageList } from "../useQueuedMessageList";
import { useQueuedMessageEdit } from "../useQueuedMessageEdit";
import { useComposerSend } from "../useComposerSend";
import { ChatWorkspaceContentProps } from "./chat-content-props";

/** Draft scope, snapshot-derived state and chat surface key/click handlers for ChatWorkspaceContent. */
export function useChatContentData(props: ChatWorkspaceContentProps) {
	const {
		snapshot,
		assetBaseUrl,
		remoteHostId,
		workspaceTabs,
		workspaceActiveTabKey,
		auxiliaryTabOrder,
		onAuxiliaryTabOrderChange,
		startingSteps,
		newWorkDisabled = false,
		reviewerTerminal,
		reviewerChat,
		reviewerChatSelected = false,
		session,
		reviewerTarget,
		shellTerminals,
		shellTarget,
		onSend,
		onInterrupt,
		onContinueTurn,
		onResumeAgent,
		resumingAgent,
		resumeError,
		localEchos,
		nativeImages,
		onSteer,
		onPromoteQueuedTurn,
		onEditQueuedTurn,
		onCancelQueuedTurn,
		draftScope,
		draftPersistenceAvailable,
	} = props;
	const surfaceRef = useRef<HTMLElement | null>(null);
	const composerFocusRef = useRef<ChatComposerHandle>(null);
	const {
		draftScopeKey,
		uiSessionId,
		activeRemoteHostId,
		remoteCreateMux,
		turn,
		canContinue,
		limitedTurn,
		suppressStopped,
		startupReconnecting,
		startupState,
		provisionSteps,
		hasStartup,
		openingTurnId,
		startup,
	} = useChatStartup({
		draftScope,
		remoteHostId,
		session,
		assetBaseUrl,
		snapshot,
		onContinueTurn,
		newWorkDisabled,
		resumingAgent,
		startingSteps,
		onResumeAgent,
		resumeError,
	});
	const hasPendingInteraction = snapshot.items.some(
		(item) =>
			item.kind === "activity" &&
			(item.activityKind === "approval" || item.activityKind === "user_input") &&
			item.status === "pending" &&
			(!item.turnId || item.turnId === turn?.id),
	);
	const handleChatKeyDown = useCallback(
		(event: ReactKeyboardEvent<HTMLElement>) => {
			if (
				newWorkDisabled ||
				event.key !== "Escape" ||
				event.defaultPrevented ||
				isDialogOrMenuOpen() ||
				event.altKey ||
				event.ctrlKey ||
				event.metaKey ||
				turn?.state !== "running" ||
				hasPendingInteraction ||
				!onInterrupt
			)
				return;
			event.preventDefault();
			onInterrupt();
		},
		[hasPendingInteraction, newWorkDisabled, onInterrupt, turn],
	);
	const handleChatSurfaceClick = useCallback((event: ReactMouseEvent<HTMLElement>) => {
		const target = event.target;
		if (!(target instanceof Element)) return;
		// Portal menus (the agent picker) still bubble clicks here; focusing the composer would close them.
		if (!event.currentTarget.contains(target)) return;
		// A click fires after a drag selection ends. Focusing the composer here would
		// collapse the range the user just selected in the transcript.
		if (window.getSelection()?.isCollapsed === false) return;
		// The focusable context tooltip must not redirect focus to the composer.
		if (
			target.closest(
				"button, a, input, textarea, select, [contenteditable='true'], [role='button'], [role='option'], [data-context-meter], [role='menuitem'], [role='dialog'], [data-testid='session-terminal'], .xterm, .terminal-surface",
			)
		)
			return;

		const composer = surfaceRef.current?.querySelector<HTMLElement>(
			'[aria-label="Message the agent"]',
		);
		if (composer?.getAttribute("aria-disabled") !== "true") composerFocusRef.current?.focus();
	}, []);
	const {
		reviewerActive,
		shellActive,
		orderedAuxiliaryTabs,
		activeWorkspaceTab,
		reorderAuxiliaryTabs,
	} = useAuxiliaryTabs({
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
	});
	const {
		queuedMessages,
		backgroundWakeQueuedTurnIds,
		visibleQueuedMessages,
		stablePromoteQueuedTurn,
		stableCancelQueuedTurn,
	} = useQueuedMessageList({
		snapshot,
		openingTurnId,
		localEchos,
		onPromoteQueuedTurn,
		onCancelQueuedTurn,
	});
	const {
		queueEdit,
		queueEditRef,
		queueDraftError,
		updateQueueDraft,
		changeQueuedDraft,
		changeQueuedStagedAttachments,
		changeQueuedRetainedAttachments,
		handleCancelQueuedTurn,
	} = useQueuedMessageEdit({
		draftPersistenceAvailable,
		draftScope,
		onCancelQueuedTurn,
		stableCancelQueuedTurn,
	});
	const {
		handleComposerSend,
		stableInterrupt,
		beginQueuedEdit,
		cancelQueuedEdit,
		promoteQueuedTurn,
		steer,
		acceptedClientMessageIds,
	} = useComposerSend({
		onSend,
		queueEdit,
		onEditQueuedTurn,
		queuedMessages,
		queueEditRef,
		updateQueueDraft,
		nativeImages,
		draftScope,
		onInterrupt,
		onSteer,
		newWorkDisabled,
		stablePromoteQueuedTurn,
		snapshot,
	});
	// The turn a confirmation is open for. Undo is not reversible and it changes what
	// the agent knows, so it is never one click.
	return {
		surfaceRef,
		composerFocusRef,
		acceptedClientMessageIds,
		activeRemoteHostId,
		activeWorkspaceTab,
		backgroundWakeQueuedTurnIds,
		beginQueuedEdit,
		canContinue,
		cancelQueuedEdit,
		changeQueuedDraft,
		changeQueuedRetainedAttachments,
		changeQueuedStagedAttachments,
		draftScopeKey,
		handleCancelQueuedTurn,
		handleChatKeyDown,
		handleChatSurfaceClick,
		handleComposerSend,
		hasStartup,
		limitedTurn,
		openingTurnId,
		orderedAuxiliaryTabs,
		promoteQueuedTurn,
		provisionSteps,
		queueDraftError,
		queueEdit,
		queuedMessages,
		remoteCreateMux,
		reorderAuxiliaryTabs,
		reviewerActive,
		shellActive,
		stableInterrupt,
		startup,
		startupReconnecting,
		startupState,
		steer,
		suppressStopped,
		turn,
		uiSessionId,
		visibleQueuedMessages,
	};
}
