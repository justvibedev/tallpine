import { Centered, unavailableConversationSnapshot } from "./SessionChatSurfaceStates";
import type { SessionChatSurfaceProps } from "./SessionChatSurfaceProps";
import { autoOpensAssistantLinks, useAutoOpenAssistantLinks } from "./useAutoOpenAssistantLinks";
import { useSettings } from "../../hooks/useSettings";
import { useTranslation } from "react-i18next";
import { useIndependentSideChats } from "./IndependentSideChats";
import type { ChatDraftExcerptReference } from "../../lib/chat-drafts";
import { useSideChatsAvailable } from "../../lib/side-chats";
import { AlertTriangle, Loader2 } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useChatPermissionModes } from "../../hooks/useChatPermissionModes";
import { useChatModelChoices } from "../../hooks/useChatModelChoices";
import { clearConversationProviderCatalogs, conversationQueryKey, invalidateConversationProviderCatalogs, useConversation, useConversationCommands, useConversationConfigOptions, useConversationModels, useConversationSkills, useProviderSwitchCommand, useStageAttachments, useWorkspaceFilePaths } from "../../hooks/conversation";
import { useConversationPromptSuggestion } from "../../hooks/useConversationPromptSuggestion";
import { useRememberProjectPermissions } from "../../hooks/useRememberProjectPermissions";
import { useLatestCallback } from "../../hooks/useStable";
import { useSessionBrowserLink } from "../../hooks/useSessionBrowserLink";
import { sessionUiKey } from "../../lib/hosts";
import { readChatLastModel, rememberChatLastModel } from "../../lib/chat-last-model";
import { clientForSessionHost } from "../../lib/host-clients";
import { useSessionLinkNavigation } from "../../lib/use-session-link-navigation";
import { can } from "../../types/conversation";
import type { TurnSettings } from "../../types/conversation";
import { type PendingAgentSwitch } from "./AgentSwitchPicker";
import { apiErrorMessage } from "../../lib/api-client";
import { useUiStore } from "../../stores/ui-store";
import { continuableTurnId } from "./ChatWorkspace";
import { startingConversationSnapshot } from "./OrchestratorStartingChat";
import { hasProviderPermissionMode } from "./turn-settings/config-options";
import { Spinner } from "../ui/spinner";

/** State, effects and derived values behind the chat surface's JSX. */
export function useSessionChatSurface(props: SessionChatSurfaceProps) {
	const {
		sideChatContainer,
		session,
		hostId,
		reviewerChatSelected,
		reviewerTarget,
		onSelectChat,
		shellTarget,
		onOpenFile,
		onOpenLinkInBrowser,
		workspaceActiveTabKey,
		workspaceFileActive,
		controllerTransitioning,
		agentResuming,
		newWorkDisabled,
		arriving,
		onConversationWorkChange,
		interfaceSwitch,
	} = props;
	const { t } = useTranslation();
	const uiSessionId = sessionUiKey(session.id, hostId);
	const {
		snapshot: queriedSnapshot,
		isLoading,
		unavailable,
		error,
		refreshError,
		hasOlder,
		isLoadingOlder,
		loadOlder,
	} = useConversation(session.id, hostId);
	// Route props can move to the destination before the old query observer drops
	// its data. Treat that snapshot as unknown everywhere, especially at the work
	// boundary that decides whether switching to Terminal needs user consent.
	const snapshot = queriedSnapshot?.sessionId === session.id ? queriedSnapshot : undefined;
	const commands = useConversationCommands(session.id, hostId);
	const projectPermissions = useRememberProjectPermissions(session.workspaceId, snapshot?.harness, hostId, session.kind);
	const {
		acknowledgeAcceptedTurn,
		acknowledgeLocalEcho,
		localEchos = [],
		pendingAcceptedTurnId,
	} = commands;
	const conversationWorkKnown = Boolean(snapshot);
	const [sideWork, setSideWork] = useState(false);
	const acceptedLocalTurnObserved = Boolean(
		pendingAcceptedTurnId && snapshot?.turns.some((turn) => turn.id === pendingAcceptedTurnId),
	);
	const acceptedLocalWorkPending = Boolean(pendingAcceptedTurnId && !acceptedLocalTurnObserved);
	const controllerBusy =
		snapshot?.controller?.state === "busy" || commands.busy || acceptedLocalWorkPending;
	const hasRunningTurn = Boolean(snapshot?.turns.some((turn) => turn.state === "running"));
	const queuedTurnCount = snapshot?.turns.filter((turn) => turn.state === "queued").length ?? 0;
	// Choosing another agent only records the choice; the switch runs with the next message.
	const providerSwitch = useProviderSwitchCommand(session.id, hostId);
	const [chosenSwitch, setChosenSwitch] = useState<PendingAgentSwitch>();
	// The chat is the tab in front: not a file, terminal or reviewer tab.
	const chatInFront = !workspaceFileActive && !workspaceActiveTabKey && !reviewerTarget && !reviewerChatSelected && !shellTarget;
	// Sending is the switch, so a pick is dropped once another tab comes forward: a message
	// typed on coming back could otherwise go to an agent no longer meant.
	useEffect(() => {
		if (!chatInFront) setChosenSwitch(undefined);
	}, [chatInFront]);
	const queryClient = useQueryClient();
	// After a daemon restart the daemon reattaches surviving Chats on its own, and
	// that reattach emits no change event. The session's recovery check settling
	// on "ready" is the signal to refresh the controller state this view shows.
	const statusReadiness = session.statusReadiness;
	const lastReadiness = useRef({ id: session.id, readiness: statusReadiness });
	useEffect(() => {
		const previous = lastReadiness.current;
		lastReadiness.current = { id: session.id, readiness: statusReadiness };
		if (hostId || previous.id !== session.id || statusReadiness !== "ready" ||
			!previous.readiness || previous.readiness === "ready") return;
		void queryClient.invalidateQueries({ queryKey: conversationQueryKey(session.id) });
	}, [hostId, queryClient, session.id, statusReadiness]);
	// A switch refused inside a send; the separate switch request keeps its own.
	const [sendSwitchError, setSendSwitchError] = useState<unknown>();
	const switchError = providerSwitch.error ?? sendSwitchError;
	const clearSwitchError = () => { setSendSwitchError(undefined); providerSwitch.reset(); };
	const currentHarness = snapshot?.harness;
	const pendingSwitch = currentHarness && chosenSwitch?.harness !== currentHarness ? chosenSwitch : undefined;
	// Catalogs are cached by session; a switch made elsewhere (another window or device)
	// leaves the outgoing agent's, so they follow the agent the chat reports.
	useEffect(() => {
		if (!currentHarness) return;
		const key = ["conversation-catalog-harness", hostId ?? "", session.id];
		const seen = queryClient.getQueryData<string>(key);
		queryClient.setQueryData(key, currentHarness);
		if (seen && seen !== currentHarness) {
			clearConversationProviderCatalogs(queryClient, session.id, hostId);
			invalidateConversationProviderCatalogs(queryClient, session.id, hostId);
		}
	}, [currentHarness, hostId, queryClient, session.id]);
	// A pick whose agent the chat now runs is done, however the switch's answer went.
	useEffect(() => {
		if (chosenSwitch && chosenSwitch.harness === currentHarness) setChosenSwitch(undefined);
	}, [chosenSwitch, currentHarness]);
	const pendingHarness = pendingSwitch?.harness;
	// The daemon's first-start chat check can take a second; run it now so the send does not wait.
	useEffect(() => {
		if (!pendingHarness) return;
		void clientForSessionHost(hostId)
			.POST("/api/v1/agents/{agent}/chat/prewarm", { params: { path: { agent: pendingHarness } } })
			.catch(() => undefined);
	}, [hostId, pendingHarness]);
	// Ultracode is asked for in each message, so it is kept here, not on the agent.
	const [ultracodeOn, setUltracodeOn] = useState(false);
	const ultracode = useMemo(() => ({ on: ultracodeOn, onChange: setUltracodeOn }), [ultracodeOn]);
	const autoCompactWindow = snapshot?.settings.autoCompactWindow;
	const [applyingAutoCompact, setApplyingAutoCompact] = useState(false);
	// Claude Code reads this when it starts, so saving it restarts the agent.
	const saveAutoCompact = useLatestCallback((value: TurnSettings["autoCompactWindow"]) => {
		if (!snapshot) return;
		setApplyingAutoCompact(true);
		void commands.chooseStartSettings({ ...snapshot.settings, autoCompactWindow: value })
			.catch((error) => useUiStore.getState().showGlobalToast("Auto-compact change failed", apiErrorMessage(error), "error"))
			.finally(() => setApplyingAutoCompact(false));
	});
	const agentBusy = controllerBusy || hasRunningTurn || queuedTurnCount > 0;
	// The restart is only safe while the agent is not working.
	const { settings: hostSettings } = useSettings(hostId);
	const compactDefault = snapshot?.harness ? hostSettings?.harnessDefaults?.[snapshot.harness]?.autoCompactWindow : undefined;
	const autoCompact = useMemo(
		() => ({ value: autoCompactWindow, disabled: applyingAutoCompact || agentBusy, onChange: saveAutoCompact, defaultChoice: compactDefault }),
		[agentBusy, applyingAutoCompact, autoCompactWindow, compactDefault, saveAutoCompact],
	);
	const switchBlocked = agentBusy || providerSwitch.isPending;
	// A new pick for the same agent keeps the permission mode picked for it.
	const choosePendingSwitch = useCallback((next: PendingAgentSwitch | undefined) => {
		setChosenSwitch((current) => next && current?.approvalMode && current.harness === next.harness && !next.approvalMode
			? { ...next, approvalMode: current.approvalMode }
			: next);
	}, []);
	// While the agent runs in a mode it needed (Pi only bypassed), a switch away goes back
	// to the chat's own, which its switch marker keeps.
	const chatPermissionMode = useMemo(() => {
		const marker = snapshot?.items.findLast((item) => item.kind === "activity" && item.detail?.event === "provider_switch");
		const detail = marker?.kind === "activity" ? marker.detail : undefined;
		return detail?.targetHarness === currentHarness && detail?.permissionMode === snapshot?.settings.approvalMode
			? detail?.chatPermissionMode
			: undefined;
	}, [currentHarness, snapshot?.items, snapshot?.settings.approvalMode]);
	const permissionModes = useChatPermissionModes(hostId, pendingSwitch?.harness);
	const pendingPermissionModes = pendingSwitch ? permissionModes?.get(pendingSwitch.harness) : undefined;
	const agentSwitch = useMemo(
		() => currentHarness && !newWorkDisabled
			? { projectId: session.workspaceId, hostId, pending: pendingSwitch, pendingPermissionModes, chatPermissionMode, onPendingChange: choosePendingSwitch, disabled: switchBlocked, ultracode, autoCompact }
			: undefined,
		[autoCompact, chatPermissionMode, choosePendingSwitch, currentHarness, hostId, newWorkDisabled, pendingPermissionModes, pendingSwitch, session.workspaceId, switchBlocked, ultracode],
	);
	useEffect(() => {
		if (acceptedLocalTurnObserved && pendingAcceptedTurnId) {
			acknowledgeAcceptedTurn(pendingAcceptedTurnId);
		}
	}, [acceptedLocalTurnObserved, acknowledgeAcceptedTurn, pendingAcceptedTurnId]);
	useEffect(() => {
		if (!snapshot) return;
		const durableHumanTurnIds = new Set(
			snapshot.items.flatMap((item) =>
				item.kind === "message" && item.role === "user" && item.origin === "human" && item.turnId
					? [item.turnId]
					: [],
			),
		);
		const queuedTurnIds = new Set(snapshot.turns.filter((turn) => turn.state === "queued").map((turn) => turn.id));
		for (const echo of localEchos) {
			if (
				echo.turnId && durableHumanTurnIds.has(echo.turnId) &&
				!(echo.backgroundWake && queuedTurnIds.has(echo.turnId))
			) acknowledgeLocalEcho?.(echo.turnId);
		}
	}, [acknowledgeLocalEcho, localEchos, snapshot]);
	useEffect(() => {
		if (!conversationWorkKnown) return;
		onConversationWorkChange?.({ controllerBusy: controllerBusy || sideWork, hasRunningTurn: hasRunningTurn || sideWork, queuedTurnCount });
	}, [controllerBusy, conversationWorkKnown, hasRunningTurn, onConversationWorkChange, queuedTurnCount, sideWork]);
	const targetChatControllerReady =
		snapshot?.harness === session.provider &&
		(snapshot.controller?.state === "ready" || snapshot.controller?.state === "busy");
	// Mode commits before the target controller starts. A cached ready snapshot
	// can also outlive the source, so wait for the handoff's final snapshot refresh.
	// A background resume keeps the last-known catalogs visible so the model
	// picker doesn't blank out while the agent spins up.
	// A sleeping, waking or reconnecting agent cannot report its settings yet; the bar shows the saved ones.
	const waking = snapshot?.harness === session.provider &&
		(snapshot.controller?.state === "hibernated" || snapshot.controller?.state === "connecting" || snapshot.controller?.state === "recovering");
	const controllerCatalogsEnabled = (targetChatControllerReady || agentResuming) && !controllerTransitioning && !newWorkDisabled;
	const configOptions = useConversationConfigOptions(
		session.id,
		Boolean(controllerCatalogsEnabled && snapshot && can(snapshot, "config_options")),
		hostId,
	);
	// A provider config catalog may cover only model, only mode, or both.
	// Suppress native controls only for dimensions the provider catalog replaces;
	// a model-only catalog must not hide the Approvals control.
	const providerOptions = configOptions.options ?? [];
	const hasProviderMode = hasProviderPermissionMode(providerOptions);
	const hasProviderModel = providerOptions.some(
		(option) => option.category === "model" || option.id === "model",
	);
	// Only asked for once the conversation is actually readable: the catalog comes
	// from the live controller, so there is nothing to fetch before then.
	const { models: controllerModels, loaded: controllerModelsLoaded } = useConversationModels(
		session.id,
		Boolean(controllerCatalogsEnabled && snapshot) && !hasProviderModel,
		hostId,
	);
	const { models, configOptions: offeredConfigOptions } = useChatModelChoices(snapshot?.harness, controllerModels, configOptions.options, snapshot?.settings?.model, hostId, controllerModelsLoaded);
	const { skills } = useConversationSkills(
		session.id,
		Boolean(controllerCatalogsEnabled && snapshot),
		hostId,
	);
	const { paths, truncated } = useWorkspaceFilePaths(session.id, Boolean(snapshot), hostId);
	const stageAttachments = useStageAttachments(session.id, hostId);
	const [annotationNavigationRequest, setAnnotationNavigationRequest] = useState<{ id: string; text: string; messageId?: string; revision?: number }>();
	const sideEnabled = useSideChatsAvailable(session, hostId) && !unavailable;
	const sideCreationEnabled = sideEnabled && Boolean(snapshot && can(snapshot, "resume"));
	const openSideInspector = () => {
		const ui = useUiStore.getState();
		ui.setInspectorView(uiSessionId, "sideChat");
		ui.setInspectorOpen(uiSessionId, true);
	};
	const sideChats = useIndependentSideChats(
		session.id, models, skills, stageAttachments, Boolean(snapshot && can(snapshot, "images")), sideEnabled,
		(annotation) => {
			onSelectChat?.();
			setAnnotationNavigationRequest({ ...annotation, id: crypto.randomUUID() });
		},
		() => useUiStore.getState().setInspectorOpen(uiSessionId, false),
		sideCreationEnabled,
	);
	useEffect(() => { if (sideChatContainer) sideChats.show(); }, [sideChatContainer]);
	useEffect(() => setSideWork(sideChats.sides.some((side) => side.hasWork)), [sideChats.sides]);
	const attachToSide = async (excerpt: ChatDraftExcerptReference) => {
		openSideInspector();
		if (sideChats.activeId) {
			sideChats.addReference(sideChats.activeId, excerpt);
			sideChats.show(sideChats.activeId);
		} else {
			await sideChats.create(excerpt);
		}
	};

	const openLinkInBrowser = useSessionBrowserLink(session, onOpenLinkInBrowser, paths, onOpenFile);
	// Links opened on the user's behalf stay quiet about a file the session does
	// not own; only a click earns the explanation in the file pane.
	const autoOpenLinkInBrowser = useSessionBrowserLink(session, onOpenLinkInBrowser, paths);
	const openSessionLink = useSessionLinkNavigation(hostId);
	useAutoOpenAssistantLinks({ snapshot, isLoading, uiSessionId, paths, autoOpenLinkInBrowser, enabled: autoOpensAssistantLinks(session) });
	const promptSuggestion = useConversationPromptSuggestion(snapshot, Boolean(
		targetChatControllerReady && !controllerBusy && !localEchos.length &&
		!commands.rollbackPending && !commands.editMessagePending && !commands.activateBranchPending && !commands.compacting &&
		!session.isTerminated && session.provisionState !== "failed" && session.provisionState !== "provisioning" &&
		!isLoading && !error && !refreshError && !unavailable &&
		!controllerTransitioning && !agentResuming && !newWorkDisabled && !arriving &&
		chatInFront,
	), hostId);
	const renderShellFallback = Boolean(shellTarget && session);
	// A switch from the terminal shows the chat before the daemon has a
	// conversation to read. The starting snapshot stands in for the whole
	// arrival, even over a cached transcript, so history appears once and fades
	// in instead of flashing, hiding, and reappearing as the controller restarts.
	const optimisticArrival = Boolean(arriving) && !renderShellFallback;
	const optimisticChat = (session.kind === "orchestrator" && isLoading && !renderShellFallback) || optimisticArrival;
	const continueIntent = useRef<{ turnId: string; clientMessageId: string; sequence: number } | null>(null);
	const renderSnapshot =
		(optimisticArrival
			? { ...startingConversationSnapshot(session.id, session.provider), controller: { state: "stopped" as const } }
			: snapshot) ??
		(optimisticChat ? startingConversationSnapshot(session.id, session.provider) : undefined) ??
		(renderShellFallback
			? unavailableConversationSnapshot(session)
			: undefined);
	const settingsLoading = waking || Boolean(renderSnapshot && can(renderSnapshot, "config_options") && !configOptions.loaded && !configOptions.error);
	// The model the running agent reports, kept so this chat names it at once the next time it loads.
	const liveModel = settingsLoading || !targetChatControllerReady || controllerTransitioning ? undefined
		: providerOptions.find((option) => option.category === "model" || option.id === "model")?.currentValue ?? snapshot?.settings?.model;
	useEffect(() => {
		if (liveModel && snapshot?.harness) rememberChatLastModel(uiSessionId, snapshot.harness, liveModel);
	}, [liveModel, snapshot?.harness, uiSessionId]);
	const lastModel = useMemo(
		() => settingsLoading ? readChatLastModel(uiSessionId, renderSnapshot?.harness) : undefined,
		[renderSnapshot?.harness, settingsLoading, uiSessionId],
	);
	const stoppedTurnId = renderSnapshot ? continuableTurnId(renderSnapshot) : "";
	useEffect(() => {
		// Keep an ambiguous request's identity until durable progress confirms
		// that intent ended. An undo can later make the same stop eligible again.
		if (!stoppedTurnId && renderSnapshot && continueIntent.current &&
			renderSnapshot.latestSequence > continueIntent.current.sequence) {
			continueIntent.current = null;
		}
	}, [stoppedTurnId, renderSnapshot?.latestSequence]);
	// Keep the project marked as starting until its controller is ready, so
	// sidebar actions cannot launch a duplicate orchestrator.
	useEffect(() => {
		if (session.kind !== "orchestrator" || session.provisionState === "provisioning") return;
		if (isLoading && !renderShellFallback) return;
		if (session.provisionState !== "failed" && !targetChatControllerReady && !renderShellFallback && !error && !unavailable) return;
		useUiStore.getState().setProjectProvisioning(session.workspaceId, false, hostId);
	}, [error, hostId, isLoading, renderShellFallback, session.kind, session.provisionState, session.workspaceId, targetChatControllerReady, unavailable]);

	if (isLoading && !renderShellFallback && !optimisticChat) {
		return {
			stateView: (
				<Centered>
					<Spinner icon={Loader2} className="size-4 text-muted-foreground" />
					<span className="text-xs text-muted-foreground">Loading conversation…</span>
				</Centered>
			),
		};
	}

	// A chat session whose controller has not started yet, or whose agent cannot
	// run Chat is a state to explain rather than an error to spin on. A compatible
	// session may switch interfaces, but retrying this failed controller by itself
	// cannot change the answer.
	if (unavailable && !renderShellFallback && !optimisticArrival) {
		return {
			stateView: (
				<Centered>
					<AlertTriangle aria-hidden="true" className="size-4 text-warning" />
					<strong className="text-xs text-foreground">Conversation unavailable</strong>
					<p className="max-w-sm text-center text-xs leading-relaxed text-muted-foreground">
						{unavailable.message}
					</p>
					<p className="max-w-sm text-center text-xs leading-relaxed text-muted-foreground">
						The worktree is untouched. Open a shell from the inspector to work in it directly.
					</p>
					{interfaceSwitch?.control}
				</Centered>
			),
		};
	}

	if ((error && !optimisticArrival) || !renderSnapshot) {
		return {
			stateView: (
				<Centered>
					<AlertTriangle aria-hidden="true" className="size-4 text-destructive" />
					<p className="max-w-sm text-center text-xs leading-relaxed text-muted-foreground">
						{error ?? "Could not load this conversation."}
					</p>
					{/* The terminal can still carry on from here. */}
					{interfaceSwitch?.control}
				</Centered>
			),
		};
	}

	return {
		agentSwitch,
		annotationNavigationRequest,
		attachToSide,
		autoCompactWindow,
		clearSwitchError,
		commands,
		configOptions,
		continueIntent,
		controllerBusy,
		controllerCatalogsEnabled,
		currentHarness,
		error,
		hasOlder,
		hasProviderMode,
		isLoadingOlder,
		lastModel,
		loadOlder,
		localEchos,
		models,
		offeredConfigOptions,
		openLinkInBrowser,
		openSessionLink,
		openSideInspector,
		optimisticArrival,
		optimisticChat,
		paths,
		pendingPermissionModes,
		pendingSwitch,
		projectPermissions,
		promptSuggestion,
		providerSwitch,
		queryClient,
		refreshError,
		renderSnapshot,
		setChosenSwitch,
		setSendSwitchError,
		settingsLoading,
		sideChats,
		sideCreationEnabled,
		sideEnabled,
		skills,
		snapshot,
		stageAttachments,
		stoppedTurnId,
		switchError,
		t,
		targetChatControllerReady,
		truncated,
		uiSessionId,
		ultracode,
		waking,
	};
}
