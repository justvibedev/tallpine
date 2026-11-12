import type { ApprovalMode, ChatConfigOption, ChatConfigOptionValue, ChatModel, ChatSteerOutcome, ConversationActivity, ConversationSnapshot, ConversationTurn, QueuedMessageEditOptions, TurnSettings } from "../../types/conversation";
import type { WorkspaceSession } from "../../types/workspace";
import type { ConversationLocalEcho } from "../../hooks/conversation";
import { useMemo } from "react";
import { type ChatDraftQueuedEdit } from "../../lib/chat-drafts";
import { sentMessageHistory } from "./timeline-echoes";
import { useStableValue } from "./useStableCallback";
import { ApprovalCard } from "./ChatTimelineItems";
import { SessionContextMeter } from "./SessionContextMeter";
import { QueuedMessageDock, type QueuedMessage } from "./QueuedMessageDock";
import { TurnSettingsBar } from "./TurnSettingsBar";
import { ElicitationDock } from "./ElicitationDock";
import { liveModel } from "./liveModel";
import { answeringModel, can } from "../../types/conversation";
import { AgentSwitchProps } from "./AgentSwitchPicker";

/**
 * Memoized settings, approval, elicitation and queue-dock props for the composer.
 */
export function useComposerProps({
	hostId,
	snapshot,
	pendingApproval,
	pendingUserInput,
	settingsReady,
	onChooseSettings,
	onChooseConfigOption,
	agentSwitch,
	settingsLoading,
	lastModel,
	models,
	onRememberPermissions,
	rememberPermissionsPending,
	rememberPermissionsError,
	rememberedPermissionMode,
	showApprovalMode,
	approvalModes,
	newWorkDisabled,
	configOptions,
	configOptionPending,
	turn,
	configOptionError,
	agentResuming,
	session,
	controllerTransitioning,
	onDecide,
	busy,
	onResolveInput,
	onSteer,
	visibleQueuedMessages,
	queueEdit,
	promoteQueuedTurn,
	draftPersistenceAvailable,
	onEditQueuedTurn,
	beginQueuedEdit,
	handleCancelQueuedTurn,
	onReorderQueuedTurns,
	promoteQueuedTurnPendingTurnId,
	cancelQueuedTurnPendingTurnId,
	localEchos,
	openingTurnId,
	backgroundWakeQueuedTurnIds,
}: {
	/** The remote host the session runs on; its Settings supply the compaction default. */
	hostId?: string;
	snapshot: ConversationSnapshot;
	pendingApproval: ConversationActivity | undefined;
	pendingUserInput: ConversationActivity | undefined;
	settingsReady: boolean;
	onChooseSettings: ((settings: TurnSettings, picked?: keyof TurnSettings) => void) | undefined;
	onChooseConfigOption: ((optionId: string, value: ChatConfigOptionValue) => Promise<unknown> | void) | undefined;
	agentSwitch: AgentSwitchProps | undefined;
	settingsLoading: boolean;
	lastModel: string | undefined;
	models: ChatModel[] | undefined;
	onRememberPermissions: ((mode: ApprovalMode | null) => Promise<unknown> | void) | undefined;
	rememberPermissionsPending: boolean | undefined;
	rememberPermissionsError: string | undefined;
	rememberedPermissionMode: ApprovalMode | undefined;
	showApprovalMode: boolean | undefined;
	approvalModes: ApprovalMode[] | undefined;
	newWorkDisabled: boolean;
	configOptions: ChatConfigOption[] | undefined;
	configOptionPending: boolean | undefined;
	turn: ConversationTurn | undefined;
	configOptionError: string | undefined;
	agentResuming: boolean;
	session: WorkspaceSession | undefined;
	controllerTransitioning: boolean | undefined;
	onDecide: ((requestId: string, decisionId: string) => void) | undefined;
	busy: boolean | undefined;
	onResolveInput: ((requestId: string, action: "accept" | "decline" | "cancel", content?: Record<string, unknown>) => Promise<unknown> | void) | undefined;
	onSteer: ((text: string, attachments?: { mimeType: string; data: string; }[], clientMessageId?: string, recoverOnly?: boolean) => Promise<ChatSteerOutcome | void>) | undefined;
	visibleQueuedMessages: QueuedMessage[];
	queueEdit: ChatDraftQueuedEdit | undefined;
	promoteQueuedTurn: (turnId: string) => Promise<unknown>;
	draftPersistenceAvailable: boolean;
	onEditQueuedTurn: ((turnId: string, text: string, options?: QueuedMessageEditOptions) => Promise<unknown>) | undefined;
	beginQueuedEdit: (turnId: string, text: string) => void;
	handleCancelQueuedTurn: (turnId: string) => Promise<void>;
	onReorderQueuedTurns: ((turnIds: string[]) => Promise<unknown>) | undefined;
	promoteQueuedTurnPendingTurnId: string | undefined;
	cancelQueuedTurnPendingTurnId: string | undefined;
	localEchos: ConversationLocalEcho[] | undefined;
	openingTurnId: string | undefined;
	backgroundWakeQueuedTurnIds: Set<string>;
}) {
	const stableSettings = useStableValue(snapshot.settings);

	// Claude Code compacts at the window picked for it, well before its model's full context.
	const compactsAt = snapshot.harness === "claude-code" ? snapshot.settings?.autoCompactWindow : undefined;

	const stableUsage = useStableValue(snapshot.usage);

	const stableRateLimits = useStableValue(snapshot.rateLimits);

	// A reroute describes the model it was recorded against; once the user picks a
	// different model it no longer says anything about the next turn.
	const currentModelReroute =
		snapshot.modelReroute &&
		(!snapshot.modelReroute.fromModel ||
			!snapshot.settings.model ||
			snapshot.modelReroute.fromModel === snapshot.settings.model)
			? snapshot.modelReroute
			: undefined;
	const stableModelReroute = useStableValue(currentModelReroute);

	const stablePendingApproval = useStableValue(pendingApproval);

	const stablePendingUserInput = useStableValue(pendingUserInput);

	const running = useMemo(() => liveModel(snapshot.harness, configOptions), [snapshot.harness, configOptions]);

	const stopped = snapshot.controller.state === "stopped" && !agentResuming && !controllerTransitioning && !newWorkDisabled &&
		session?.provisionState !== "provisioning";
	const composerSettings = useMemo(
		() =>
			(settingsReady || stopped) && (onChooseSettings || onChooseConfigOption) ? (
				<TurnSettingsBar
					agentSwitch={agentSwitch}
					beforePicker={agentSwitch ? <SessionContextMeter
						usage={stableUsage}
						hostId={hostId}
						harness={snapshot.harness}
						model={answeringModel(snapshot) || running?.id}
						modelLabel={answeringModel(snapshot) ? undefined : running?.label}
						sessionId={snapshot.sessionId}
						settings={stableSettings}
						onChooseSettings={newWorkDisabled ? undefined : onChooseSettings}
						compactedAt={snapshot.compactedAt}
						rateLimits={stableRateLimits}
						lastCompaction={snapshot.threadState?.compaction}
					/> : undefined}
					// While the agent wakes or starts; a chat reads "hibernated" until ready.
					modelsLoading={settingsLoading}
					stopped={stopped}
					lastModel={lastModel}
					models={models ?? []}
					settings={stableSettings}
					onRememberPermissions={onRememberPermissions}
					rememberPermissionsPending={rememberPermissionsPending}
					rememberPermissionsError={rememberPermissionsError}
					rememberedPermissionMode={rememberedPermissionMode}
					harness={snapshot.harness}
					showApprovalMode={showApprovalMode ?? true}
					approvalModes={approvalModes}
					reroute={stableModelReroute}
					onChange={newWorkDisabled ? undefined : onChooseSettings}
					configOptions={configOptions ?? []}
					onChangeConfigOption={newWorkDisabled ? undefined : onChooseConfigOption}
					configPending={configOptionPending}
					autoSelectEffortOnOpen={snapshot.items.length === 0 && !turn}
					error={configOptionError}
					// Turn settings require a live controller even while messages can queue.
				// A background resume keeps the controls visible with their last values.
					disabled={
							(!agentResuming && (
								snapshot.controller.state === "connecting" ||
								(snapshot.controller.state === "stopped" && !stopped)
							)) ||
							settingsLoading ||
							session?.provisionState === "provisioning" ||
							controllerTransitioning || configOptionPending || newWorkDisabled
						}
				/>
			) : null,
		[
			settingsReady,
			stopped,
			settingsLoading,
			lastModel,
			configOptionError,
			configOptionPending,
			configOptions,
			running,
			controllerTransitioning,
			models,
			agentSwitch,
			stableUsage,
			newWorkDisabled,
			session?.provisionState,
			onChooseConfigOption,
			onChooseSettings,
			onRememberPermissions,
			rememberPermissionsPending,
			rememberPermissionsError,
			rememberedPermissionMode,
			showApprovalMode,
			approvalModes,
			snapshot.controller.state,
			snapshot.items.length,
			turn,
			stableModelReroute,
			stableSettings,
			stableRateLimits,
			snapshot.harness,
			snapshot.compactedAt,
			snapshot.threadState?.compaction,
			snapshot.modelReroute?.toModel,
			hostId,
		],
	);

	const composerApproval = useMemo(
		() =>
			stablePendingApproval ? (
				<ApprovalCard
					activity={stablePendingApproval}
					onDecide={onDecide}
					busy={busy}
					embedded
				/>
			) : undefined,
		[busy, onDecide, stablePendingApproval],
	);

	const composerElicitation = useMemo(
		() =>
			stablePendingUserInput ? (
				<ElicitationDock key={stablePendingUserInput.id} activity={stablePendingUserInput} onResolve={onResolveInput} />
			) : undefined,
		[onResolveInput, stablePendingUserInput],
	);

	const canSteerQueuedMessage =
		Boolean(onSteer) && can(snapshot, "steer") && turn?.state === "running";

	const composerQueuedDock = useMemo(
		() =>
			visibleQueuedMessages.length > 0 ? (
				<QueuedMessageDock
					messages={visibleQueuedMessages}
					editingTurnId={queueEdit?.turnId}
					disabled={Boolean(queueEdit?.clientMessageId)}
					canSteer={canSteerQueuedMessage}
					onPromoteQueuedTurn={newWorkDisabled ? undefined : promoteQueuedTurn}
					onBeginQueuedEdit={
						newWorkDisabled || !draftPersistenceAvailable || !onEditQueuedTurn ? undefined : beginQueuedEdit
					}
					onCancelQueuedTurn={newWorkDisabled ? undefined : handleCancelQueuedTurn}
					onReorderQueuedTurns={newWorkDisabled ? undefined : onReorderQueuedTurns}
					promotePendingTurnId={promoteQueuedTurnPendingTurnId}
					cancelPendingTurnId={cancelQueuedTurnPendingTurnId}
				/>
			) : null,
		[
			beginQueuedEdit,
			canSteerQueuedMessage,
			cancelQueuedTurnPendingTurnId,
			draftPersistenceAvailable,
			handleCancelQueuedTurn,
			newWorkDisabled,
			onEditQueuedTurn,
			onReorderQueuedTurns,
			promoteQueuedTurn,
			promoteQueuedTurnPendingTurnId,
			queueEdit?.clientMessageId,
			queueEdit?.turnId,
			visibleQueuedMessages,
		],
	);

	const composerDraftSeed = useMemo(
		() => (queueEdit ? { id: `${queueEdit.turnId}:${queueEdit.ownerId ?? queueEdit.expectedRevision ?? "legacy"}`, text: queueEdit.text, attachments: queueEdit.attachments, stagedAttachments: queueEdit.stagedAttachments } : undefined),
		[queueEdit],
	);

	const sentMessageList = useMemo(
		() => sentMessageHistory(snapshot, localEchos ?? [], openingTurnId, backgroundWakeQueuedTurnIds),
		[backgroundWakeQueuedTurnIds, localEchos, openingTurnId, snapshot],
	);

	// Stable across streaming deltas, so the memoized composer only renders for a new prompt.
	const sentMessages = useStableValue(sentMessageList);

	return {
		stableSettings,
		compactsAt,
		composerSettings,
		composerApproval,
		composerElicitation,
		composerQueuedDock,
		composerDraftSeed,
		sentMessages,
	};
}
