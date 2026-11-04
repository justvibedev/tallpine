import type { TimelineProps } from "./TimelineProps";
import { useChatDraftTranslation } from "../../lib/chat-draft-messages";
import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { useTranslation } from "react-i18next";
import { useChatSelectionPosition } from "../../hooks/useChatSelectionPosition";
import { chatDraftScopeKey, readChatSessionDraft, type ChatDraftExcerptReference } from "../../lib/chat-drafts";
import { getApiBaseUrl, subscribeApiBaseUrl } from "../../lib/api-client";
import { useStableCallback } from "./useStableCallback";
import { useChatScrollMemory } from "./useChatScrollMemory";
import { useTimelineScroll } from "./useTimelineScroll";
import { useAnnotationNavigation } from "./useAnnotationNavigation";
import { useInlineEditState } from "./useInlineEditState";
import { useTranscriptSelection } from "./useTranscriptSelection";
import { useTimelineMinimap } from "./useTimelineMinimap";
import { useTurnEligibility } from "./useTurnEligibility";
import { useMinimapInspectorSync } from "./useMinimapInspectorSync";
import { useInlineEditRecovery } from "./useInlineEditRecovery";
import { useMessageEditActions } from "./useMessageEditActions";
import { useTimelineItems } from "./useTimelineItems";
import { useJustMeGroups } from "./useJustMeGroups";
import { useUnreadReplies } from "./useUnreadReplies";
import { useTimelineVirtualizer } from "./useTimelineVirtualizer";
import { useAnnotationScroll } from "./useAnnotationScroll";
import { useTimelineScrollLayout } from "./useTimelineScrollLayout";
import { useTimelineScrollInput } from "./useTimelineScrollInput";
import { inspectorIsOpen, useUiStore } from "../../stores/ui-store";
import { activeTurn, queuedTurnIds } from "../../types/conversation";

/** State, effects and derived values behind the Timeline's JSX. */
export function useTimelineController(props: TimelineProps) {
	const {
		annotationNavigationRef,
		arriving = false,
		assetBaseUrl,
		draftPersistenceAvailable = true,
		draftScope,
		editPending,
		excerptsEnabled,
		hasOlder,
		hasPlanTab,
		loadingOlder,
		localEchos = [],
		newWorkDisabled,
		onActivateBranch,
		onAddToChat,
		onDecide,
		onEditHumanMessage,
		onForeignAnnotation,
		onLoadOlder,
		onOpenFile,
		onOpenFiles,
		onOpenPlan,
		onRollback,
		remoteHost,
		retryControl,
		snapshot,
		startup,
		workerUpdates = false,
	} = props;
	const hasTranscript = snapshot.items.length > 0;
	const arrivedEmpty = useRef(false);
	if (arriving && !hasTranscript) arrivedEmpty.current = true;
	const revealTranscript = arrivedEmpty.current && hasTranscript;
	const translateDraft = useChatDraftTranslation();
	const uiSessionId = draftScope.sessionId;
	const {
		scroller,
		scrollContent,
		virtualContent,
		virtualScrollMargin,
		setVirtualScrollMargin,
		measuredVirtualGroups,
		pendingVirtualLayout,
		renderedVirtualHeight,
		promptSpacer,
		scrollTrack,
		drag,
		pinnedRef,
		pinned,
		glideToEnd,
		followEnd,
		cancelScrollFollow,
		holdScrollFollow,
		markWritten,
		isOwnScroll,
		isFlying,
		reaim,
		endFlight,
		glideRequested,
		initialLayoutDone,
		initialLayoutAt,
		lastScrollTop,
		skipRelatchFollow,
		setPinnedNow,
		releaseFollow,
		requestGlideToEnd,
		activityDisclosureOverrides,
		setActivityDisclosureOverrides,
		onActivityDisclosureChange,
	} = useTimelineScroll();
	const [hoveredMarker, setHoveredMarker] = useState<number | null>(null);
	const [selectionAction, setSelectionAction] = useState<{
		excerpt: ChatDraftExcerptReference;
		range: Range;
	} | null>(null);
	const selectionButton = useRef<HTMLDivElement>(null);
	const clearSelectionAction = useCallback(() => setSelectionAction(null), []);
	const selectionPosition = useChatSelectionPosition(selectionAction?.range, scroller, selectionButton, clearSelectionAction);
	const {
		annotationTarget,
		setAnnotationTarget,
		annotationNavigationError,
		setAnnotationNavigationError,
		annotationHighlightCleanup,
		annotationHighlightTimer,
		annotationRetryCount,
		annotationOlderRequestGroups,
		selectAnnotation,
	} = useAnnotationNavigation({
		onForeignAnnotation,
		snapshot,
		releaseFollow,
		scrollContent,
		annotationNavigationRef,
	});
	const hoveredMarkerRef = useRef<number | null>(null);
	hoveredMarkerRef.current = hoveredMarker;
	const {
		messageEdit,
		setMessageEdit,
		messageEditRef,
		durableInlineEditDelivery,
		setDurableInlineEditDelivery,
		setInlineEditUncertain,
		automaticInlineRecoveryAttempted,
		draftPersistenceError,
		setDraftPersistenceError,
		inlineEditRecoveryNotice,
		setInlineEditRecoveryNotice,
		inlineEditOutcomeNotice,
		setInlineEditOutcomeNotice,
		inlineEditMutation,
		appliedEditAcceptanceSequence,
		setAppliedEditAcceptanceSequence,
		inlineEditPending,
		inlineEditLocked,
		inlineEditSendBlocked,
		inlineEditRecoveryLabel,
		canAbandonInlineEditRecovery,
	} = useInlineEditState({ draftPersistenceAvailable, draftScope, editPending });
	const { captureTranscriptSelection, addSelectionToChat } = useTranscriptSelection({
		excerptsEnabled,
		setSelectionAction,
		scrollContent,
		snapshot,
		selectionAction,
		onAddToChat,
		draftScope,
		setDraftPersistenceError,
	});
	// The inspector changes the minimap's visibility, but it must not cause this
	// entire timeline to rerender. A live conversation can contain hundreds of
	// DOM nodes, and inspector toggles are otherwise a broad synchronous commit.
	// Keep that small accessibility/interaction boundary imperative instead.
	const inspectorOpenRef = useRef(
		inspectorIsOpen(useUiStore.getState().inspectorSessions, uiSessionId),
	);
	const turn = activeTurn(snapshot);
	const {
		scrollbar,
		setScrollbar,
		minimapEnabled,
		chatOpening,
		minimapRevealed,
	} = useTimelineMinimap();
	const { t } = useTranslation();
	const openingTurnId = startup?.openingTurnId;
	// The checklist holds the working slot until the agent is up and its first
	// turn is live; a clean start then leaves no trace.
	const showStartup = Boolean(
		startup &&
			(startup.failed ||
				!(turn?.state === "running" && turn.id === openingTurnId &&
					startup.steps.length > 0 && startup.steps.every((step) => step.status === "done"))),
	);
	const queued = useMemo(() => {
		const ids = queuedTurnIds(snapshot);
		if (openingTurnId) ids.delete(openingTurnId);
		return ids;
	}, [openingTurnId, snapshot]);
	const decide = useStableCallback(onDecide);
	const rollback = useStableCallback(onRollback);
	const openPlan = useStableCallback(onOpenPlan);
	const openFiles = useStableCallback(onOpenFiles);
	const openFile = useStableCallback(onOpenFile);
	const retryTurn = useStableCallback(retryControl?.retry);
	const localBaseUrl = useSyncExternalStore(subscribeApiBaseUrl, getApiBaseUrl, getApiBaseUrl);
	const apiBaseUrl = remoteHost ? assetBaseUrl ?? null : assetBaseUrl ?? localBaseUrl;
	const editHumanMessage = useStableCallback(onEditHumanMessage);
	const activateBranch = useStableCallback(onActivateBranch);
	const canEditHumanMessage = Boolean(onEditHumanMessage) && !newWorkDisabled;
	const canActivateBranch = Boolean(onActivateBranch) && !newWorkDisabled;
	const { branchPoints, reconstructedTurns, retryableTurns, editableTurns } = useTurnEligibility({ snapshot });

	// An ordinary branch change hides an edit that belongs to the previous branch.
	// An unresolved delivery is different: its exact persisted editor owns the only
	// Retry/Abandon controls, so keep it actionable when an ambiguous send activates
	// and refetches the replacement branch.
	useEffect(() => {
		if (draftPersistenceAvailable && readChatSessionDraft(draftScope).inlineEditDelivery) return;
		setMessageEdit(undefined);
	}, [draftPersistenceAvailable, draftScope, snapshot.activeBranchId, snapshot.sessionId]);
	useMinimapInspectorSync({
		inspectorOpenRef,
		scrollTrack,
		minimapEnabled,
		drag,
		hoveredMarkerRef,
		setHoveredMarker,
		uiSessionId,
	});

	useEffect(() => {
		pinnedRef.current = pinned;
	}, [pinned]);
	const { acceptAndClearInlineEditDelivery, abandonUncertainInlineEdit } = useInlineEditRecovery({
		draftPersistenceAvailable,
		draftScope,
		messageEditRef,
		setMessageEdit,
		setDurableInlineEditDelivery,
		setInlineEditUncertain,
		setDraftPersistenceError,
		setInlineEditRecoveryNotice,
		setAppliedEditAcceptanceSequence,
		setInlineEditOutcomeNotice,
		automaticInlineRecoveryAttempted,
		durableInlineEditDelivery,
		inlineEditMutation,
		appliedEditAcceptanceSequence,
	});
	const {
		updateMessageEdit,
		cancelMessageEdit,
		submitMessageEdit,
		stableStartMessageEdit,
		stableSubmitMessageEdit,
	} = useMessageEditActions({
		inlineEditLocked,
		draftScope,
		reconstructedTurns,
		messageEditRef,
		setMessageEdit,
		setInlineEditRecoveryNotice,
		setInlineEditOutcomeNotice,
		setDraftPersistenceError,
		onEditHumanMessage,
		inlineEditPending,
		durableInlineEditDelivery,
		setInlineEditUncertain,
		setDurableInlineEditDelivery,
		acceptAndClearInlineEditDelivery,
		editHumanMessage,
	});
	const {
		newHumanMessageIds,
		editedMessageVisible,
		timelineItems,
		groups: allGroups,
		workerUpdateIndex,
	} = useTimelineItems({
		snapshot,
		messageEdit,
		localEchos,
		requestGlideToEnd,
		openingTurnId,
		hasPlanTab,
		workerUpdates,
	});
	const { groups, toggleRun: toggleJustMeRun, justMe } = useJustMeGroups(allGroups);
	const {
		groupCountRef,
		navigableGroups,
		previews,
		virtualized,
		virtualizer,
		virtualRows,
		virtualHeight,
		renderedGroups,
	} = useTimelineVirtualizer({
		groups,
		scroller,
		virtualScrollMargin,
		pinned,
		measuredVirtualGroups,
		pendingVirtualLayout,
		isFlying,
		reaim,
		markWritten,
		messageEdit,
		renderedVirtualHeight,
	});
	const layoutKey = `${groups.length}:${virtualHeight}`;
	const { unreadCount } = useUnreadReplies({ memoryKey: chatDraftScopeKey(draftScope), groups, pinned, setPinnedNow, markWritten, scroller, scrollContent, virtualized, virtualizer, layoutKey });
	useChatScrollMemory({ memoryKey: chatDraftScopeKey(draftScope), scroller, setPinnedNow, markWritten, layoutKey });
	useAnnotationScroll({
		annotationTarget,
		groups,
		loadingOlder,
		hasOlder,
		onLoadOlder,
		annotationOlderRequestGroups,
		setAnnotationNavigationError,
		setAnnotationTarget,
		scrollContent,
		activityDisclosureOverrides,
		setActivityDisclosureOverrides,
		virtualized,
		virtualizer,
		annotationRetryCount,
		annotationHighlightTimer,
		annotationHighlightCleanup,
		scroller,
		virtualRows,
	});
	useEffect(() => {
		const node = scroller.current;
		const content = virtualized ? virtualContent.current
			: virtualContent.current?.querySelector<HTMLElement>('[data-index="0"]');
		if (node && content) {
			setVirtualScrollMargin(content.getBoundingClientRect().top - node.getBoundingClientRect().top + node.scrollTop);
		}
	}, [virtualized, hasOlder]);
	const { updateScrollbar } = useTimelineScrollLayout({
		scroller,
		scrollTrack,
		virtualized,
		scrollContent,
		groups,
		virtualizer,
		setScrollbar,
		promptSpacer,
		renderedVirtualHeight,
		initialLayoutDone,
		initialLayoutAt,
		markWritten,
		followEnd,
		skipRelatchFollow,
		pinnedRef,
		glideRequested,
		glideToEnd,
		groupCountRef,
		pinned,
		snapshot,
		messageEdit,
		pendingVirtualLayout,
		virtualHeight,
	});
	const {
		onScroll,
		onViewportWheel,
		onViewportKeyDown,
		onViewportPointerDown,
		onScrollbarPointerDown,
		onScrollbarPointerMove,
		stopScrollbarDrag,
		onScrollbarWheel,
		onScrollbarKeyDown,
	} = useTimelineScrollInput({
		scroller,
		lastScrollTop,
		isOwnScroll,
		pinnedRef,
		releaseFollow,
		skipRelatchFollow,
		setPinnedNow,
		updateScrollbar,
		holdScrollFollow,
		cancelScrollFollow,
		scrollTrack,
		scrollbar,
		minimapEnabled,
		inspectorOpenRef,
		navigableGroups,
		virtualized,
		virtualizer,
		groups,
		drag,
		setHoveredMarker,
	});

	return {
		abandonUncertainInlineEdit,
		activateBranch,
		activityDisclosureOverrides,
		addSelectionToChat,
		annotationNavigationError,
		apiBaseUrl,
		branchPoints,
		canAbandonInlineEditRecovery,
		canActivateBranch,
		canEditHumanMessage,
		cancelMessageEdit,
		captureTranscriptSelection,
		chatOpening,
		clearSelectionAction,
		decide,
		draftPersistenceError,
		editHumanMessage,
		editableTurns,
		editedMessageVisible,
		endFlight,
		groups,
		holdScrollFollow,
		hoveredMarker,
		inlineEditOutcomeNotice,
		inlineEditPending,
		inlineEditRecoveryLabel,
		inlineEditRecoveryNotice,
		inlineEditSendBlocked,
		inspectorOpenRef,
		messageEdit,
		minimapEnabled,
		minimapRevealed,
		newHumanMessageIds,
		onActivityDisclosureChange,
		onScroll,
		onScrollbarKeyDown,
		onScrollbarPointerDown,
		onScrollbarPointerMove,
		onScrollbarWheel,
		onViewportKeyDown,
		onViewportPointerDown,
		onViewportWheel,
		openFile,
		openFiles,
		openPlan,
		openingTurnId,
		pinned,
		previews,
		promptSpacer,
		queued,
		renderedGroups,
		requestGlideToEnd,
		retryTurn,
		retryableTurns,
		revealTranscript,
		rollback,
		scrollContent,
		scrollTrack,
		scrollbar,
		scroller,
		selectAnnotation,
		selectionAction,
		selectionButton,
		selectionPosition,
		setHoveredMarker,
		showStartup,
		stableStartMessageEdit,
		stableSubmitMessageEdit,
		stopScrollbarDrag,
		submitMessageEdit,
		t,
		timelineItems,
		toggleJustMeRun,
		justMe,
		unreadCount,
		translateDraft,
		turn,
		updateMessageEdit,
		virtualContent,
		virtualHeight,
		virtualScrollMargin,
		virtualized,
		virtualizer,
		workerUpdateIndex,
	};
}
