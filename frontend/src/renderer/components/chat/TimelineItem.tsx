import { TriangleAlert } from "lucide-react";
import {
	ActivityRow,
	ApprovalCard,
	AssistantMessage,
	CompactionMarker,
	ProviderSwitchMarker,
	ContinuedMarker,
	HumanMessage,
	OriginMessage,
	SteerMessage,
} from "./ChatTimelineItems";
import { useTranslation } from "react-i18next";
import { AutomationMessage } from "./AutomationMessage";
import { FromAgent, hasNeedsYouMarker, relayedVia } from "./WorkerUpdates";
import { TurnPlan } from "./TurnPlan";
import { ElicitationHistory } from "./ElicitationDock";
import {
	activityPlan,
	isCompaction,
	isSteer,
	type ConversationActivity,
	type ConversationBranchPoint,
	type ConversationItem,
	type ConversationMessage,
} from "../../types/conversation";
import { ChatWorkspaceProps, MessageEditDraft } from "./ChatWorkspaceProps";
import { itemKey } from "./timeline-echoes";

export function TurnLiveStatus({
	blocked,
	providerFailure,
}: {
	blocked?: boolean;
	providerFailure?: ConversationActivity;
}) {
	const { t } = useTranslation();
	if (blocked) {
		return (
			<span role="alert" className="sr-only">
				{t("chat.waitingForDecision")}
			</span>
		);
	}
	if (providerFailure) {
		return (
			<div
				role="status"
				aria-live="polite"
				className="flex min-h-8 items-start gap-2 px-1 py-1"
				data-testid="live-turn-status"
			>
				<TriangleAlert
					aria-hidden="true"
					className="mt-0.5 size-3.5 shrink-0 text-warning"
				/>
				<span className="flex min-w-0 flex-col gap-0.5">
					<strong className="text-xs font-medium text-warning">
						{providerFailure.summary}
					</strong>
					{providerFailure.detail?.text ? (
						<span className="text-xs leading-snug text-muted-foreground">
							{providerFailure.detail.text}
						</span>
					) : null}
				</span>
			</div>
		);
	}

	return null;
}

export function TimelineItem({
	item,
	sessionId,
	apiBaseUrl,
	onDecide,
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
	busy,
	queued,
	newHumanMessageIds,
	showCopy,
	live,
	onRollback,
	rollbackDisabled,
	onSelectAnnotation,
}: {
	item: ConversationItem;
	sessionId: string;
	apiBaseUrl: string | null;
	onDecide?: (requestId: string, decisionId: string) => void;
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
	busy?: boolean;
	/**
	 * The enclosing turn was recorded but not yet sent, so a waiting message can say
	 * so. A group is one turn, so this holds for every item in it.
	 */
	queued?: boolean;
	newHumanMessageIds: ReadonlySet<string>;
	/** This is the final assistant response of a turn that has finished. */
	showCopy?: boolean;
	live?: boolean;
	/** Undo this finished turn from the answer that owns its copy action. */
	onRollback?: () => void;
	/** Keep the action row mounted while another turn is running. */
	rollbackDisabled?: boolean;
	onSelectAnnotation?: (annotation: { text: string; messageId?: string; revision?: number }) => void;
	/** This message is the live edge of its turn, rather than an earlier fragment
	 * followed by tool activity. */
}) {
	if (item.kind === "message") {
		if (item.role === "assistant") {
			const message = (
				<AssistantMessage
					message={item}
					showCopy={showCopy}
					live={live}
					onRollback={onRollback}
					rollbackDisabled={rollbackDisabled}
				/>
			);
			// The orchestrator marked this reply as needing the owner: highlight it.
			return hasNeedsYouMarker(item.text)
				? <div data-needs-you className="border-l-2 border-amber-500 pl-3">{message}</div>
				: message;
		}
		// A continue after a stop reaches the agent as a message, but the user did
		// not write it: mark where the turn picked back up instead of a bubble.
		if (item.origin === "human" && item.continuation) return <ContinuedMarker />;
		// A user-role message that did not come from this human is an automation or
		// worker relay, and is attributed differently.
		if (item.origin === "human" && !relayedVia(item)) {
			const editAvailable = Boolean(
				item.editAvailable && item.turnId && editableTurns.has(item.turnId) && onEditHumanMessage,
			);
			const editing = Boolean(item.turnId && messageEdit?.turnId === item.turnId);
			return (
				<HumanMessage
					message={item}
					sessionId={sessionId}
					apiBaseUrl={apiBaseUrl}
					queued={queued}
					animateIn={newHumanMessageIds.has(itemKey(item))}
					onEdit={editAvailable ? (_turnID, text) => onSubmitMessageEdit(text) : undefined}
					editing={editing}
					editText={editing ? messageEdit?.text : undefined}
					editReconstructedContext={editing && messageEdit?.reconstructedContext}
					onEditStart={editAvailable ? () => onStartMessageEdit(item) : undefined}
					onEditDraftChange={onUpdateMessageEdit}
					onEditCancel={onCancelMessageEdit}
					onEditAbandonRecovery={onAbandonEditRecovery}
					editPending={editPending}
					editSendBlocked={editSendBlocked}
					editRecoveryLabel={editRecoveryLabel}
					editBusy={editBusy}
					editError={editError}
					branchPoint={item.turnId ? branchPoints.get(item.turnId) : undefined}
					onActivateBranch={onActivateBranch}
					activateBranchPending={activateBranchPending}
					activateBranchError={activateBranchError}
					onSelectAnnotation={onSelectAnnotation}
				/>
			);
		}
		return (
			<FromAgent item={item} queued={queued}>
				<AutomationMessage message={item} queued={queued} fallback={<OriginMessage message={item} />} />
			</FromAgent>
		);
	}
	if (item.activityKind === "approval") {
		// Pending approval owns the composer. Keeping it in the grouping model still
		// marks the live turn as blocked, while omitting the timeline card itself.
		if (item.status === "pending") return null;
		return <ApprovalCard activity={item} onDecide={onDecide} busy={busy} />;
	}
	if (item.activityKind === "user_input") {
		return item.status === "pending" ? null : <ElicitationHistory activity={item} />;
	}
	if (isCompaction(item)) {
		return <CompactionMarker activity={item} />;
	}
	if (item.detail?.event === "provider_switch") {
		return <ProviderSwitchMarker activity={item} />;
	}
	// Read by the event, not the kind: a steer is stored as a `system` activity
	// because that is Tallpine's only durable write that can attach to a turn in flight,
	// but it is the user speaking and the timeline shows it that way.
	if (isSteer(item)) {
		return (
			<FromAgent item={item}>
				<SteerMessage activity={item} sessionId={sessionId} apiBaseUrl={apiBaseUrl} />
			</FromAgent>
		);
	}
	// A plan whose turn Tallpine never correlated — one from before this controller
	// started. The turn-level checklist cannot show it, so the row carries it.
	if (item.activityKind === "plan") {
		const plan = activityPlan(item);
		return plan ? <TurnPlan plan={plan} /> : <ActivityRow activity={item} />;
	}
	return <ActivityRow activity={item} />;
}
