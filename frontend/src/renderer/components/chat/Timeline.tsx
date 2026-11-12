import { TimelineGroupRow } from "./TimelineGroupRow";
import { JustMeLiveLine } from "./JustMeRows";
import { useTimelineController } from "./useTimelineController";
import { TimelineTail } from "./TimelineTail";
import { PinnedPrompt } from "./PinnedPrompt";
import { TimelineMinimap } from "./TimelineMinimap";
import type { TimelineProps } from "./TimelineProps";
import { memo } from "react";
import { ArrowDown, Loader2, MessageSquarePlus } from "lucide-react";
import { actionMenuContentClass, actionMenuItemClass } from "../ui/menu-styles";
import { cn } from "../../lib/utils";
import { READER_INTENT_HOLD_MS } from "./timeline-constants";
import { Button } from "../ui/button";
import { ChatOpeningRef } from "./ChatMarkdown";
import { Spinner } from "../ui/spinner";

/**
 * The scrolling timeline.
 *
 * Auto-scroll only follows new items while the user is already near the bottom.
 * Once they scroll up to read, new output must not yank them away — it surfaces a
 * jump control instead.
 *
 * A trailing spacer sizes itself so the latest human prompt can sit near the top
 * of the viewport when following — the Cursor/ChatGPT "drop a prompt and the
 * history moves up" behaviour. As the reply grows the spacer shrinks, which keeps
 * the prompt in place without fighting the reader.
 *
 * Finished turns are memoized so a targeted live-event invalidation only redraws
 * the turn that changed. Older pages are prepended explicitly instead of keeping
 * an unbounded history in every snapshot response.
 */

/**
 * The conversation transcript: turn grouping, live status, "Worked for", and
 * scroll follow. Exported so a side chat renders its transcript with exactly
 * this behavior while keeping its own composer.
 */
function TimelineImpl(props: TimelineProps) {
	const {
		activateBranchError,
		activateBranchPending,
		busy,
		composerRecoveryTurnId,
		editBusy,
		editError,
		hasOlder,
		loadingOlder,
		newWorkDisabled,
		onAddToSideChat,
		onLoadOlder,
		onOpenFile,
		onOpenFiles,
		onOpenPlan,
		onRollback,
		retryControl,
		rollbackDisabled = false,
		snapshot,
		startup,
		workerUpdates = false,
	} = props;
	const {
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
		pinnedPrompt,
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
		toggleJustMeRun,
		justMe,
		unreadCount,
		t,
		timelineItems,
		translateDraft,
		turn,
		updateMessageEdit,
		virtualContent,
		virtualHeight,
		virtualScrollMargin,
		virtualized,
		virtualizer,
		workerUpdateIndex,
	} = useTimelineController(props);
	if (timelineItems.length === 0 && !messageEdit && !turn && !showStartup) {
		return null;
	}

	return (
		<ChatOpeningRef.Provider value={chatOpening}>
		<div
			className="relative min-h-0 flex-1"
			data-testid="chat-timeline"
			// The timeline is a fixed-size flex item with its own scroll viewport.
			// Isolating its layout and paint means Lexical selection/composer updates
			// cannot invalidate the complete mounted conversation history or shell.
			style={{ contain: "layout paint" }}
		>
			{selectionAction ? (
				<div
					ref={selectionButton}
					style={{ left: selectionPosition?.left ?? 0, top: selectionPosition?.top ?? 0, visibility: selectionPosition?.visible ? "visible" : "hidden" }}
					onMouseDown={(event) => event.preventDefault()}
					className={cn(actionMenuContentClass, "absolute flex-row min-w-0 w-max max-w-full shadow-lg")}
				>
					<button type="button" onClick={() => void addSelectionToChat()} className={cn(actionMenuItemClass, "shrink-0 whitespace-nowrap hover:bg-interactive-hover hover:text-foreground")}>
						<MessageSquarePlus aria-hidden="true" className="size-3.5" /> {t("sideChat.addToChat")}
					</button>
					{onAddToSideChat ? <button type="button" className={cn(actionMenuItemClass, "shrink-0 whitespace-nowrap hover:bg-interactive-hover")} onClick={() => { void onAddToSideChat(selectionAction.excerpt); clearSelectionAction(); window.getSelection()?.removeAllRanges(); }}><MessageSquarePlus aria-hidden="true" className="size-3.5" /> {t("sideChat.addToSideChat")}</button> : null}
				</div>
			) : null}
			<div
				ref={scroller}
				onMouseUp={captureTranscriptSelection}
				onKeyUp={captureTranscriptSelection}
				onScroll={onScroll}
				onScrollEnd={endFlight}
				onWheel={onViewportWheel}
				onTouchMove={() => holdScrollFollow(READER_INTENT_HOLD_MS)}
				onKeyDown={onViewportKeyDown}
				onPointerDown={onViewportPointerDown}
				className="chat-scroll-viewport cursor-chat-timeline h-full min-w-0 select-text overflow-x-hidden overflow-y-auto px-4 pt-5 pb-0"
				role="log"
				aria-live="polite"
				aria-label={t("chat.timeline.conversationAria")}
				style={virtualized ? { overflowAnchor: "none" } : undefined}
			>
				<div ref={scrollContent} className={cn("mx-auto flex w-full min-w-0 max-w-3xl flex-col gap-4.5", revealTranscript && "chat-transcript-reveal")}>
					{annotationNavigationError ? <p role="status" className="text-xs text-muted-foreground">{annotationNavigationError}</p> : null}
					{hasOlder ? (
						<div className="flex justify-center pb-1">
							<Button
								type="button"
								size="sm"
								variant="ghost"
								disabled={loadingOlder}
								onClick={onLoadOlder}
								className="gap-1.5 text-muted-foreground"
							>
								{loadingOlder ? (
									<Spinner icon={Loader2} className="size-3.5" />
								) : null}
								{t("sideChat.loadEarlierMessages")}
							</Button>
						</div>
					) : null}
					<div ref={virtualContent} className={virtualized ? "relative w-full" : "contents"}
						style={virtualized ? { height: virtualHeight } : undefined}>
					{renderedGroups.map((item) => (
						<TimelineGroupRow
							key={item.group.key}
							item={item}
							ctx={{
							snapshot,
							translateDraft,
							virtualScrollMargin,
							activityDisclosureOverrides,
							onActivityDisclosureChange,
							selectAnnotation,
							messageEdit,
							draftPersistenceError,
							inlineEditRecoveryNotice,
							inlineEditOutcomeNotice,
							inlineEditPending,
							inlineEditSendBlocked,
							inlineEditRecoveryLabel,
							canAbandonInlineEditRecovery,
							turn,
							openingTurnId,
							showStartup,
							queued,
							decide,
							rollback,
							openPlan,
							openFiles,
							openFile,
							retryTurn,
							apiBaseUrl,
							editHumanMessage,
							activateBranch,
							branchPoints,
							retryableTurns,
							editableTurns,
							abandonUncertainInlineEdit,
							updateMessageEdit,
							cancelMessageEdit,
							stableStartMessageEdit,
							stableSubmitMessageEdit,
							newHumanMessageIds,
							groups,
							workerUpdateIndex,
							virtualizer,
							canEditHumanMessage,
							canActivateBranch,
							startup,
							composerRecoveryTurnId,
							retryControl,
							newWorkDisabled,
							rollbackDisabled,
							editBusy,
							editError,
							onRollback,
							onOpenPlan,
							onOpenFiles,
							onOpenFile,
							activateBranchPending,
							activateBranchError,
							workerUpdates,
							busy,
							toggleJustMeRun,
							}}
						/>
					))}
					</div>
					{justMe ? <JustMeLiveLine items={snapshot.items} /> : null}
					<TimelineTail
						translateDraft={translateDraft}
						promptSpacer={promptSpacer}
						messageEdit={messageEdit}
						draftPersistenceError={draftPersistenceError}
						inlineEditRecoveryNotice={inlineEditRecoveryNotice}
						inlineEditOutcomeNotice={inlineEditOutcomeNotice}
						inlineEditPending={inlineEditPending}
						inlineEditSendBlocked={inlineEditSendBlocked}
						inlineEditRecoveryLabel={inlineEditRecoveryLabel}
						canAbandonInlineEditRecovery={canAbandonInlineEditRecovery}
						turn={turn}
						openingTurnId={openingTurnId}
						showStartup={showStartup}
						abandonUncertainInlineEdit={abandonUncertainInlineEdit}
						updateMessageEdit={updateMessageEdit}
						cancelMessageEdit={cancelMessageEdit}
						submitMessageEdit={submitMessageEdit}
						editedMessageVisible={editedMessageVisible}
						groups={groups}
						startup={startup}
						t={t}
						newWorkDisabled={newWorkDisabled}
						editBusy={editBusy}
						editError={editError}
					/>
				</div>
			</div>
			{pinnedPrompt.visible && pinnedPrompt.prompt ? <PinnedPrompt text={pinnedPrompt.prompt.text} onJump={pinnedPrompt.jump} /> : null}
			<TimelineMinimap
				scrollTrack={scrollTrack}
				inspectorOpenRef={inspectorOpenRef}
				minimapEnabled={minimapEnabled}
				minimapRevealed={minimapRevealed}
				scrollbar={scrollbar}
				previews={previews}
				hoveredMarker={hoveredMarker}
				setHoveredMarker={setHoveredMarker}
				onPointerDown={onScrollbarPointerDown}
				onPointerMove={onScrollbarPointerMove}
				stopDrag={stopScrollbarDrag}
				onWheel={onScrollbarWheel}
				onKeyDown={onScrollbarKeyDown}
			/>

			{!pinned && unreadCount > 0 ? (
				<Button
					type="button"
					size="sm"
					variant="outline"
					onClick={requestGlideToEnd}
					className="absolute bottom-3 left-1/2 -translate-x-1/2 gap-1.5 rounded-full border-border-strong bg-raised text-foreground shadow-sm hover:bg-surface dark:bg-raised dark:hover:bg-surface"
				>
					<ArrowDown aria-hidden="true" className="size-4" />
					{t("chat.timeline.newReplies", { count: unreadCount })}
				</Button>
			) : !pinned ? (
				<Button
					type="button"
					size="icon-sm"
					variant="outline"
					aria-label={t("chat.timeline.jumpToLatest")}
					title={t("chat.timeline.jumpToLatest")}
					onClick={requestGlideToEnd}
					className="absolute bottom-3 left-1/2 size-12 -translate-x-1/2 rounded-full border-border-strong bg-raised p-0 text-foreground shadow-sm hover:bg-surface dark:bg-raised dark:hover:bg-surface"
				>
					<ArrowDown aria-hidden="true" className="size-5" />
				</Button>
			) : null}
		</div>
		</ChatOpeningRef.Provider>
	);
}

/** Memoized so unrelated parent renders (snapshot churn elsewhere) skip the whole transcript. */
export const Timeline = memo(TimelineImpl);
