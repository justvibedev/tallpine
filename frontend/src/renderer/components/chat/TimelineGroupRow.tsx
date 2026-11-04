import type { ComponentProps } from "react";
import type { ChatDraftInlineEdit } from "../../lib/chat-drafts";
import type { ConversationBranchPoint, ConversationMessage, ConversationSnapshot, ConversationTurn } from "../../types/conversation";
import type { ChatWorkspaceProps } from "./ChatWorkspaceProps";
import type { TimelineGroup } from "./timeline-groups";
import type { WorkerUpdateIndex } from "./WorkerUpdates";
import type { TimelineProps } from "./TimelineProps";
import { TurnGroup } from "./TurnGroup";
import { JustMeRunRow } from "./JustMeRows";
import { groupHasHumanPrompt, isQuietWorkerGroup } from "./timeline-groups";
import { useTimelineVirtualizer } from "./useTimelineVirtualizer";
import { WorkerUpdateIndexContext } from "./WorkerUpdates";

export interface TimelineGroupRowContext {
	snapshot: ConversationSnapshot;
	translateDraft: (message: string | null | undefined) => string | undefined;
	virtualScrollMargin: number;
	activityDisclosureOverrides: Record<string, boolean>;
	onActivityDisclosureChange: (key: string, open: boolean) => void;
	selectAnnotation: ComponentProps<typeof TurnGroup>["onSelectAnnotation"];
	messageEdit: ChatDraftInlineEdit | undefined;
	draftPersistenceError: string | undefined;
	inlineEditRecoveryNotice: string | undefined;
	inlineEditOutcomeNotice: string | undefined;
	inlineEditPending: boolean;
	inlineEditSendBlocked: boolean;
	inlineEditRecoveryLabel: string | undefined;
	canAbandonInlineEditRecovery: boolean;
	turn: ConversationTurn | undefined;
	openingTurnId: string | undefined;
	showStartup: boolean;
	queued: Set<string>;
	decide: (requestId: string, decisionId: string) => void;
	rollback: (turnId: string) => void;
	openPlan: () => void;
	openFiles: () => void;
	openFile: (path: string, line?: number) => void;
	retryTurn: (turnId: string) => void | Promise<unknown>;
	apiBaseUrl: string | null;
	editHumanMessage: ChatWorkspaceProps["onEditMessage"] extends infer F ? NonNullable<F> : never;
	activateBranch: (branchId: string) => Promise<unknown> | void;
	branchPoints: Map<string, ConversationBranchPoint>;
	retryableTurns: Map<string, ConversationTurn>;
	editableTurns: Set<string>;
	abandonUncertainInlineEdit: () => void;
	updateMessageEdit: (text: string) => void;
	cancelMessageEdit: () => void;
	stableStartMessageEdit: (message: ConversationMessage) => void;
	stableSubmitMessageEdit: (text: string) => Promise<void>;
	newHumanMessageIds: ReadonlySet<string>;
	groups: TimelineGroup[];
	workerUpdateIndex: WorkerUpdateIndex | null;
	virtualizer: ReturnType<typeof useTimelineVirtualizer>["virtualizer"];
	canEditHumanMessage: boolean;
	canActivateBranch: boolean;
	startup: TimelineProps["startup"];
	composerRecoveryTurnId: TimelineProps["composerRecoveryTurnId"];
	retryControl: TimelineProps["retryControl"];
	newWorkDisabled: TimelineProps["newWorkDisabled"];
	rollbackDisabled: boolean;
	editBusy: TimelineProps["editBusy"];
	editError: TimelineProps["editError"];
	onRollback: TimelineProps["onRollback"];
	onOpenPlan: TimelineProps["onOpenPlan"];
	onOpenFiles: TimelineProps["onOpenFiles"];
	onOpenFile: TimelineProps["onOpenFile"];
	activateBranchPending: TimelineProps["activateBranchPending"];
	activateBranchError: TimelineProps["activateBranchError"];
	workerUpdates: TimelineProps["workerUpdates"];
	busy: TimelineProps["busy"];
	toggleJustMeRun: (key: string) => void;
}

/** One turn group row, absolutely positioned when the timeline is virtualized. */
export function TimelineGroupRow({ item: { group, row, index }, ctx }: { item: { group: TimelineGroup; row?: { start: number }; index: number }; ctx: TimelineGroupRowContext }) {
	const {
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
	} = ctx;
						const retrySelected = !retryControl?.turnId || retryControl.turnId === group.turnId;
		const retryableTurn = group.turnId ? retryableTurns.get(group.turnId) : undefined;
		const retry =
			group.turnId &&
			retryControl &&
			retryableTurn &&
			group.turnId !== composerRecoveryTurnId &&
				group.outcome?.state === "failed"
				? {
						onRetry: () => {
							void Promise.resolve(retryTurn(group.turnId as string)).catch(
								() => undefined,
							);
						},
						pending: retryControl.pending && retrySelected,
						error: retrySelected ? retryControl.error : undefined,
						disabled:
							Boolean(turn) ||
							Boolean(retryControl.pending) ||
							Boolean(newWorkDisabled),
					}
				: undefined;
		return (
			<div
				key={group.key}
				ref={virtualizer.measureElement}
				data-index={index}
				style={row ? {
					position: "absolute", top: 0, left: 0, width: "100%",
					transform: `translateY(${row.start - virtualScrollMargin}px)`,
				} : undefined}
				data-chat-scroll-anchor={groupHasHumanPrompt(group) ? "" : undefined}
			>
				{group.collapsedRun ? (
					<JustMeRunRow
						count={group.collapsedRun.count}
						open={group.collapsedRun.open}
						live={Boolean(group.live)}
						onToggle={() => toggleJustMeRun(group.key)}
					/>
				) : (
				<WorkerUpdateIndexContext.Provider value={workerUpdateIndex}>
				<TurnGroup
					group={group}
					workerUpdates={workerUpdates}
					// Consecutive quiet worker turns read as one list of rows, not separate turns.
					tight={workerUpdates && isQuietWorkerGroup(group) && isQuietWorkerGroup(groups[index - 1])}
					startup={showStartup && group.turnId === openingTurnId ? startup : undefined}
					onSelectAnnotation={selectAnnotation}
					activityDisclosureOverrides={activityDisclosureOverrides}
					onActivityDisclosureChange={onActivityDisclosureChange}
					sessionId={snapshot.sessionId}
					apiBaseUrl={apiBaseUrl}
					onDecide={decide}
					onRollback={rollback}
					onOpenPlan={onOpenPlan ? openPlan : undefined}
					onOpenFiles={onOpenFiles ? openFiles : undefined}
					onOpenFile={onOpenFile ? openFile : undefined}
					retry={retry}
					onEditHumanMessage={canEditHumanMessage ? editHumanMessage : undefined}
					messageEdit={messageEdit}
					onStartMessageEdit={stableStartMessageEdit}
					onUpdateMessageEdit={updateMessageEdit}
					onCancelMessageEdit={cancelMessageEdit}
					onAbandonEditRecovery={
						canAbandonInlineEditRecovery ? abandonUncertainInlineEdit : undefined
					}
					onSubmitMessageEdit={stableSubmitMessageEdit}
					editPending={inlineEditPending}
					editSendBlocked={inlineEditSendBlocked}
					editRecoveryLabel={translateDraft(inlineEditRecoveryLabel)}
					editBusy={Boolean(editBusy || newWorkDisabled)}
					editError={translateDraft(editError ??
						draftPersistenceError ??
						inlineEditRecoveryNotice ??
						inlineEditOutcomeNotice)}
					branchPoints={branchPoints}
					editableTurns={editableTurns}
					newHumanMessageIds={newHumanMessageIds}
					onActivateBranch={canActivateBranch ? activateBranch : undefined}
					activateBranchPending={activateBranchPending}
					activateBranchError={activateBranchError}
					// Only a turn the provider actually accepted can be undone: a turn it
					// never saw holds no history to discard, and the daemon refuses it
					// rather than hiding rows the agent still remembers.
					canRollback={Boolean(onRollback && group.turnId && group.rollbackable)}
					rollbackDisabled={rollbackDisabled}
					busy={busy}
					queued={Boolean(group.turnId && queued.has(group.turnId))}
				/>
				</WorkerUpdateIndexContext.Provider>
				)}
			</div>
		);
}
