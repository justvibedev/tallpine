import { useMemo } from "react";
import { useTranslation } from "react-i18next";
import { latestPendingInteraction } from "../pendingInteraction";
import { useComposerProps } from "../useComposerProps";
import { useComposerCenterFlip } from "../useComposerCenterFlip";
import { orchestratorSetupPlaceholder } from "../chat-setup-copy";
import { useChatContentData } from "./useChatContentData";
import { ChatWorkspaceContentProps } from "./chat-content-props";

/** Pending interaction, banners and startup state for ChatWorkspaceContent. */
export function useChatContentStatus(props: ChatWorkspaceContentProps, data: ReturnType<typeof useChatContentData>) {
	const {
		snapshot,
		sessionRole = "worker",
		controllerTransitioning,
		accountMoving = false,
		loadingQuietly,
		agentResuming = false,
		settingsReady = true,
		settingsLoading = false,
		lastModel,
		newWorkDisabled = false,
		session,
		onDecide,
		onResolveInput,
		busy,
		models,
		agentSwitch,
		onChooseSettings,
		onRememberPermissions,
		showApprovalMode,
		approvalModes,
		rememberPermissionsPending,
		rememberPermissionsError,
		rememberedPermissionMode,
		configOptions,
		onChooseConfigOption,
		configOptionPending,
		configOptionError,
		localEchos,
		onSteer,
		onEditQueuedTurn,
		onReorderQueuedTurns,
		promoteQueuedTurnPendingTurnId,
		cancelQueuedTurnPendingTurnId,
		draftPersistenceAvailable,
	} = props;
	const {
		activeRemoteHostId,
		backgroundWakeQueuedTurnIds,
		beginQueuedEdit,
		handleCancelQueuedTurn,
		hasStartup,
		openingTurnId,
		promoteQueuedTurn,
		provisionSteps,
		queueEdit,
		startup,
		startupState,
		turn,
		uiSessionId,
		visibleQueuedMessages,
	} = data;
	const pendingApproval = useMemo(
		() => latestPendingInteraction(snapshot.items, "approval", turn),
		[snapshot.items, turn],
	);
	const pendingUserInput = useMemo(
		() => latestPendingInteraction(snapshot.items, "user_input", turn),
		[snapshot.items, turn],
	);
	const {
		stableSettings,
		compactsAt,
		composerSettings,
		composerApproval,
		composerElicitation,
		composerQueuedDock,
		composerDraftSeed,
		sentMessages,
	} = useComposerProps({
		hostId: activeRemoteHostId,
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
	});
	// Empty chats center the prompt; once a turn or item exists the composer docks
	// at the bottom and stays there for the rest of the session.
	const orchestratorStarting = sessionRole === "orchestrator" && startupState !== "failed" && (
		startupState === "provisioning" || agentResuming ||
		(!loadingQuietly && (snapshot.controller.state === "connecting" || snapshot.controller.state === "recovering"))
	);
	const setupPlaceholder = orchestratorSetupPlaceholder(provisionSteps);
	// Arriving from the terminal reuses the startup composer: the shimmer plus a
	// placeholder that follows the controller. Leaving for the terminal
	// (newWorkDisabled) stays quiet, since that screen is about to go away.
	const arrivingInChat = Boolean(controllerTransitioning) && !accountMoving && !newWorkDisabled && startupState !== "provisioning";
	const arrivingPlaceholder = snapshot.controller.state === "connecting" || snapshot.controller.state === "recovering"
		? "Restoring your conversation"
		: "Starting the chat agent";
	const compactStartup = sessionRole === "orchestrator" && startupState === "failed" ? startup : undefined;
	// Arriving in a chat that already has messages docks the composer at the
	// bottom while they load; a chat with none opens centered like any new chat.
	const arrivingWithHistory = arrivingInChat && Boolean(session?.lastUserMessageAt);
	const conversationEmpty = !arrivingWithHistory && snapshot.items.length === 0 && !turn && (localEchos?.length ?? 0) === 0 && (!hasStartup || sessionRole === "orchestrator");
	const { t } = useTranslation();
	const { emptyChatPlaceholder, composerDockRef, composerBoxRef, welcomeLeaving } = useComposerCenterFlip({ sessionRole, conversationEmpty, uiSessionId });

	return {
		arrivingInChat,
		arrivingPlaceholder,
		arrivingWithHistory,
		compactStartup,
		compactsAt,
		composerApproval,
		composerBoxRef,
		composerDockRef,
		composerDraftSeed,
		composerElicitation,
		composerQueuedDock,
		composerSettings,
		conversationEmpty,
		emptyChatPlaceholder,
		orchestratorStarting,
		sentMessages,
		setupPlaceholder,
		stableSettings,
		t,
		welcomeLeaving,
	};
}
