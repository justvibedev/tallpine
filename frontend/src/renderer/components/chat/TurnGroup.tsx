import { TurnWorkSummary } from "./TurnWorkSummary";
import { memo, useEffect, useMemo, useState, type ComponentProps, type ReactNode } from "react";
import { Undo2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import { cn } from "../../lib/utils";
import {
	TurnChangedFiles,
	TurnOutcome,
	LiveResponseStatus,
	type TurnOutcomeRetryControl,
} from "./ChatTimelineItems";
import {
	isTaskTitleUpdate,
	isWorkerLead,
	OrchestratorReply,
	piggybackedUpdates,
	quietWorkerReply,
	renamedTitle,
	TaskNamedLine,
	WorkerUpdateRows,
	workerUpdatesOf,
} from "./WorkerUpdates";
import { SpawnedAgents, spawnedSessions } from "./SpawnedAgents";
import { SessionStartup } from "./SessionStartup";
import { ActivityRun } from "./ActivityRun";
import { TurnPlan } from "./TurnPlan";
import { PlanUpdateRow } from "./PlanUpdateRow";
import {
	type ConversationActivity,
	type ConversationBranchPoint,
	type ConversationMessage,
} from "../../types/conversation";
import { ChatWorkspaceProps, MessageEditDraft } from "./ChatWorkspaceProps";
import { TimelineItem, TurnLiveStatus } from "./TimelineItem";
import { TimelineGroup, TimelineRun, compactionRuns, isHumanRun, isNoticeRun, rendersNothing, runsOf } from "./timeline-groups";

/**
 * One turn, and the memo boundary that keeps a poll from re-rendering the whole
 * conversation. A turn is the right granularity because it is what changes: while
 * the agent works, one group grows and every other group is finished history.
 */
export const TurnGroup = memo(function TurnGroup({
	group,
	activityDisclosureOverrides,
	onActivityDisclosureChange,
	sessionId,
	apiBaseUrl,
	onDecide,
	onRollback,
	onOpenPlan,
	onOpenFiles,
	onOpenFile,
	onEditHumanMessage,
	messageEdit,
	onStartMessageEdit,
	onUpdateMessageEdit,
	onCancelMessageEdit,
	onAbandonEditRecovery,
	onSubmitMessageEdit,
	editPending,
	editSendBlocked,
	editRecoveryLabel,
	editBusy,
	editError,
	branchPoints,
	editableTurns,
	onActivateBranch,
	activateBranchPending,
	activateBranchError,
	canRollback,
	rollbackDisabled,
	retry,
	busy,
	queued,
	newHumanMessageIds,
	startup,
	onSelectAnnotation,
	workerUpdates = false,
	tight = false,
}: {
	group: TimelineGroup;
	activityDisclosureOverrides: Readonly<Record<string, boolean>>;
	onActivityDisclosureChange: (key: string, open: boolean) => void;
	sessionId: string;
	apiBaseUrl: string | null;
	onDecide: (requestId: string, decisionId: string) => void;
	onRollback: (turnId: string) => void;
	onOpenPlan?: () => void;
	onOpenFiles?: () => void;
	onOpenFile?: (path: string, line?: number) => void;
	onEditHumanMessage?: ChatWorkspaceProps["onEditMessage"];
	messageEdit?: MessageEditDraft;
	onStartMessageEdit: (message: ConversationMessage) => void;
	onUpdateMessageEdit: (text: string) => void;
	onCancelMessageEdit: () => void;
	onAbandonEditRecovery?: () => void;
	onSubmitMessageEdit: (text: string) => Promise<void>;
	editPending?: boolean;
	editSendBlocked?: boolean;
	editRecoveryLabel?: string;
	editBusy?: boolean;
	editError?: string;
	branchPoints: Map<string, ConversationBranchPoint>;
	editableTurns: Set<string>;
	onActivateBranch?: (branchId: string) => Promise<unknown> | void;
	activateBranchPending?: boolean;
	activateBranchError?: string;
	/** The daemon would accept a rollback of this turn, so offer the affordance. */
	canRollback: boolean;
	/** Keep rollback mounted but inert while another turn is active. */
	rollbackDisabled: boolean;
	/** Present only when this failed turn is eligible for a new attempt. */
	retry?: TurnOutcomeRetryControl;
	busy?: boolean;
	/** This turn was recorded but not sent, so its message can say so. */
	queued: boolean;
	newHumanMessageIds: ReadonlySet<string>;
	/** The opening turn of a starting session: its setup checklist. */
	startup?: ComponentProps<typeof SessionStartup>;
	onSelectAnnotation?: (annotation: { text: string; messageId?: string; revision?: number }) => void;
	workerUpdates?: boolean;
	tight?: boolean;
}) {
	const { t } = useTranslation();
	const hasTerminalFailure =
		group.outcome?.state === "failed" && Boolean(group.outcome.error);
	const runs = useMemo(
		() =>
			runsOf(
				group.items.filter((item) => {
					if (item.id === group.liveProviderFailure?.id) return false;
					if (hasTerminalFailure && item.kind === "activity" && item.activityKind === "error" && item.summary === group.outcome?.error) return false;
					return !(
						hasTerminalFailure &&
						item.kind === "activity" &&
						item.detail?.event === "provider.failure" &&
						item.status === "failed"
					);
				}),
			),
		[group.items, group.liveProviderFailure, group.outcome?.error, hasTerminalFailure],
	);
	const lastAssistantId = [...group.items]
		.reverse()
		.find((item) => item.kind === "message" && item.role === "assistant" && item.text.trim() !== "")?.id;
	// A running turn offers copy only while its newest item is the reply itself, so the
	// action does not flash under prose the agent is about to follow with more work.
	const copyableMessageId = group.outcome
		? lastAssistantId
		: group.live && group.items.at(-1)?.id === lastAssistantId
			? lastAssistantId
			: undefined;
	let finalAssistantRunIndex = -1;
	if (group.outcome) {
		for (let index = runs.length - 1; index >= 0; index -= 1) {
			const item = runs[index]?.items[0];
			if (item?.kind === "message" && item.role === "assistant" && item.text.trim() !== "") {
				finalAssistantRunIndex = index;
				break;
			}
		}
	}
	// Only the prompt that opened the turn sits above the Working row. A steer or a
	// later message stays where it happened among the work, so the reader sees
	// what the agent had done when it arrived.
	const firstWorkIndex = runs.findIndex((run) => !isHumanRun(run.items[0]));
	const leadingHumanCount = firstWorkIndex < 0 ? runs.length : firstWorkIndex;
	const humanRuns = runs.slice(0, leadingHumanCount);
	const workedRuns = runs.filter(
		(_run, index) => index >= leadingHumanCount && index !== finalAssistantRunIndex,
	);
	const finalRun = finalAssistantRunIndex >= 0 ? runs[finalAssistantRunIndex] : undefined;
	// Questions, errors, and system warnings stay readable after the turn settles, and
	// so does anything the user said mid-turn: folding a steer away would leave the
	// reply under the Worked row answering a message the reader can no longer see.
	const compaction = compactionRuns(runs, Boolean(group.outcome));
	const noticeRuns = group.outcome
		? workedRuns.filter((run) => isHumanRun(run.items[0]) || isNoticeRun(run) || compaction.includes(run))
		: [];
	const foldedRuns = workedRuns.filter((run) => !noticeRuns.includes(run) && !rendersNothing(run));
	// A turn whose only content is its compaction has no work to fold or roll back to.
	const compactionOnly = compaction.length > 0 && foldedRuns.length === 0;
	// Keep the live row mounted while its spinner animates out, then hand over to the settled row.
	const [showSettledStatus, setShowSettledStatus] = useState(!group.live);
	useEffect(() => {
		if (group.live) {
			setShowSettledStatus(false);
			return;
		}
		const timer = window.setTimeout(() => setShowSettledStatus(true), 440);
		return () => window.clearTimeout(timer);
	}, [group.live]);
	// Only the run the agent is working in stays open while the turn runs; earlier
	// runs fold to their summary, so a long turn does not keep every call mounted.
	const liveRunKey = group.live ? runs.findLast((run) => run.kind === "activities")?.key : undefined;
	const renderRun = (run: TimelineRun) =>
		run.kind === "activities" ? (
			<ActivityRun
				key={run.key}
				disclosureKey={`${group.key}:${run.key}`}
				disclosureOverrides={activityDisclosureOverrides}
				onDisclosureChange={onActivityDisclosureChange}
				activities={run.items.filter(
					(item): item is ConversationActivity => item.kind === "activity",
				)}
				live={run.key === liveRunKey}
			/>
		) : (
			<TimelineItem
				key={run.key}
				item={run.items[0]!}
				sessionId={sessionId}
				apiBaseUrl={apiBaseUrl}
				onDecide={onDecide}
				onEditHumanMessage={onEditHumanMessage}
				messageEdit={messageEdit}
				onStartMessageEdit={onStartMessageEdit}
				onUpdateMessageEdit={onUpdateMessageEdit}
				onCancelMessageEdit={onCancelMessageEdit}
				onAbandonEditRecovery={onAbandonEditRecovery}
				onSubmitMessageEdit={onSubmitMessageEdit}
				editPending={editPending}
				editSendBlocked={editSendBlocked}
				editRecoveryLabel={editRecoveryLabel}
				editBusy={editBusy}
				editError={editError}
				branchPoints={branchPoints}
				editableTurns={editableTurns}
				onActivateBranch={onActivateBranch}
				activateBranchPending={activateBranchPending}
				activateBranchError={activateBranchError}
				busy={busy}
				queued={queued}
				newHumanMessageIds={newHumanMessageIds}
				showCopy={run.items[0]?.id === copyableMessageId}
				live={group.live}
				onRollback={
					canRollback && run.items[0]?.id === copyableMessageId
						? () => onRollback(group.turnId as string)
						: undefined
				}
				rollbackDisabled={rollbackDisabled}
				onSelectAnnotation={onSelectAnnotation}
			/>
		);
	// One flat keyed list rather than a slot per section, so a run keeps its element
	// when the turn settles: the final answer leaving the worked runs, or a notice
	// moving out from among them, does not remount it or drop the reader's selection.
	// Orchestrator chat: whatever a worker sent (a message, a mid-turn steer, a
	// report batch, or reports folded into the user's message) draws as rows, and
	// Tallpine's task-naming prompt as the name it produced.
	const renderWorkerRun = (run: TimelineRun): ReactNode | ReactNode[] => {
		const item = run.kind === "single" ? run.items[0] : undefined;
		if (!workerUpdates || !item) return renderRun(run);
		const updates = workerUpdatesOf(item);
		if (updates.length > 0) return <WorkerUpdateRows key={run.key} updates={updates} />;
		const namedTitle = isTaskTitleUpdate(item) ? renamedTitle(group.items) : undefined;
		if (namedTitle) return <TaskNamedLine key={run.key} title={namedTitle} />;
		const piggybacked = piggybackedUpdates(item);
		if (piggybacked.length === 0) return renderRun(run);
		return [<WorkerUpdateRows key={`${run.key}:reports`} updates={piggybacked} />, renderRun(run)];
	};
	const body: ReactNode[] = humanRuns.flatMap(renderWorkerRun);
	// A live provider failure replaces the Working row with its reconnect card, and a
	// turn that stops without an outcome (cancelled) has no settled row to hand over to.
	// The setup checklist sits exactly where the Working row will, so the handoff
	// does not move anything.
	if (startup || (!group.liveProviderFailure && (group.live || (group.outcome && !showSettledStatus)))) {
		body.push(
			<LiveResponseStatus
				key="turn-working"
				startedAt={group.liveStartedAt}
				settling={!startup && !group.live}
				startupLabel={startup ? t(startup.failed ? "chat.startup.failed" : "chat.startup.running") : undefined}
				failed={startup?.failed}
			>
				{startup ? <SessionStartup {...startup} /> : null}
			</LiveResponseStatus>,
		);
	}
	const outcome = showSettledStatus ? group.outcome : undefined;
	// A settled worker turn whose answer asks the user nothing draws that answer as
	// one line under the rows, with the work folded into it instead of "Worked for".
	const quietReply = workerUpdates && outcome?.state === "completed" ? quietWorkerReply(group.items) : undefined;
	// A worker turn the orchestrator only read has nothing to unfold; its duration alone is noise.
	const onlyRead = workerUpdates && foldedRuns.length === 0 && !finalRun && isWorkerLead(group.items[0]);
	const disclosureKey = `${group.key}:worked`;
	if (!outcome) {
		body.push(...workedRuns.flatMap(renderWorkerRun));
	} else if (!quietReply && !onlyRead && !compactionOnly) {
		body.push(
			<TurnWorkSummary
				key="turn-worked"
				durationMs={outcome.durationMs}
				open={Boolean(activityDisclosureOverrides[disclosureKey])}
				onOpenChange={(open) => onActivityDisclosureChange(disclosureKey, open)}
			>
				{foldedRuns.length > 0 ? foldedRuns.flatMap(renderWorkerRun) : undefined}
			</TurnWorkSummary>,
		);
	}
	if (outcome) body.push(...noticeRuns.map(renderRun));
	if (quietReply && finalRun) {
		body.push(
			<OrchestratorReply
				key={finalRun.key}
				message={quietReply}
				work={foldedRuns.length > 0 ? foldedRuns.flatMap(renderWorkerRun) : undefined}
				workOpen={Boolean(activityDisclosureOverrides[disclosureKey])}
				onWorkOpenChange={(open) => onActivityDisclosureChange(disclosureKey, open)}
				onRollback={canRollback ? () => onRollback(group.turnId as string) : undefined}
				rollbackDisabled={rollbackDisabled}
			/>,
		);
	} else if (finalRun) {
		body.push(renderRun(finalRun));
	}
	const spawned = workerUpdates && outcome ? spawnedSessions(group.items) : [];
	if (spawned.length > 0) body.push(<SpawnedAgents key="spawned" spawns={spawned} />);
	return (
		<div className={cn("flex min-w-0 flex-col gap-2.5", tight && "-mt-3.5")}>
			{body}
			{/* Both of these are current state of the turn rather than steps in it, which
			    is why they sit at its end: a checklist that ticks itself off and a file
			    list that grows both change while the reader watches, and at the end of a
			    turn neither pushes anything the reader is already looking at. */}
			{group.plan ? <TurnPlan plan={group.plan} live={group.live} /> : null}
			{group.planUpdate ? <PlanUpdateRow update={group.planUpdate} onOpen={onOpenPlan} /> : null}
			{/* Changed-files review is a settled result, not live activity. Keep it
			    below the completed assistant response and its bottom action row. */}
			{group.diff && group.outcome?.state === "completed" && copyableMessageId ? (
				<TurnChangedFiles
					diff={group.diff}
					items={group.items}
					onReview={onOpenFiles}
					onOpenFile={onOpenFile}
				/>
			) : null}
			{group.live ? <TurnLiveStatus blocked={group.blocked} providerFailure={group.liveProviderFailure} /> : null}
			{/* No assistant prose to hang the undo / duration on — still offer them
			    before the outcome divider so a tool-only turn is not stuck without a
			    way back or a record of how long it took. */}
			{!copyableMessageId && !group.live && canRollback && !compactionOnly ? (
				<div className="flex h-7 items-center gap-0.5">
					{canRollback ? (
						<button
							type="button"
							onClick={() => onRollback(group.turnId as string)}
							disabled={rollbackDisabled}
							aria-label={t("chat.rollBackToHere")}
							title={t("chat.rollBackToHere")}
							className="flex items-center rounded px-1.5 py-0.5 text-muted-foreground transition-colors hover:bg-interactive-hover hover:text-foreground disabled:pointer-events-none disabled:opacity-40"
						>
							<Undo2 aria-hidden="true" className="size-3" />
						</button>
					) : null}
				</div>
			) : null}
			{group.outcome && group.outcome.state !== "completed" && !group.accountSwitched ? (
				<TurnOutcome
					state={group.outcome.state}
					error={group.outcome.error}
					usageLimit={group.outcome.usageLimit}
					retry={group.outcome.state === "failed" ? retry : undefined}
				/>
			) : null}
		</div>
	);
});
