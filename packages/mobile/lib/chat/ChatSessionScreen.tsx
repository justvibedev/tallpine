import { Feather } from "../icons";
import { AskStrip } from "../ask/AskStrip";
import { askLaneOf } from "../orchestratorView";
import { PlanCards } from "../plan/PlanCards";
import { isOrchestratorChat } from "./chatSessionKind";
import { useNavigation, useRouter } from "expo-router";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { KeyboardAvoidingView } from "react-native-keyboard-controller";
import { Pressable, View } from "react-native";
import { type DashboardSession, type OrchestratorLink } from "../api";
import { machineIdentity } from "../config";
import { haptics } from "../haptics";
import { resetHeaderRightForSwap } from "../headerRightSwap";
import { glassHeaderControl } from "../native-header-items";
import { NativeHeaderButton } from "../native-header-button";
import { useApp } from "../store";
import { mobileInterfaceTransitionIsActive, mobileInterfaceTransitionIsBusy, useInterfaceTransition } from "../session/useInterfaceTransition";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { requestDockModel } from "./requestDockModel";
import type { ResponseQuoteRequest } from "./responseQuotes";
import { ChatComposer } from "./ChatComposer";
import { ChatLinkProvider } from "./ChatMarkdown";
import { ChatTimeline } from "./ChatTimeline";
import { useChatKeyboard } from "./useChatKeyboard";
import { ConversationTitle } from "./ConversationTitle";
import { type ConversationActionsEntry } from "./chatSheetRegistry";
import { quotaWarning } from "./conversationChrome";
import { errorBanner, quotaBanner, rolledBackBanner } from "./conversationBanners";
import { conversationActionUnsupported } from "./conversationErrors";
import { turnSettingsModelLabel } from "./turnSettingsModel";
import { brokenMcpServers, can } from "./types";
import { useMobileConversation } from "./useConversation";
import { SessionStartupStatus } from "./SessionStartup";
import { useStartupSession } from "../session/useStartupSession";
import { useChatViewLease } from "./useChatViewLease";
import { useOpenPage } from "../pageNavigation";
import { reviewRouteForPR } from "../reviewView";
import { iconSize } from "../tokens";
import { changedPathCount, fileRoute, filesRoute, FilesNavigationContext, type FilesNavigation } from "../files/navigation";
import { useChangeSummary } from "../files/useChangeSummary";
import { PRODUCT_NAME } from "../brand";
import { useInterfaceTransitionBanner } from "./useInterfaceTransitionBanner";
import { useConversationActionsMenu } from "./useConversationActionsMenu";
import { useInterfaceSwitchRequest } from "./useInterfaceSwitchRequest";
import { useChatStartup } from "./useChatStartup";
import { useChatOpeners } from "./useChatOpeners";
import { useReviewPromptState } from "./useReviewPromptState";
import { Centered, ConversationBanners, DismissibleBanner, InlineBanner, Unavailable } from "./ChatSessionBanners";
import { makeStyles } from "./chatSessionStyles";

export type MobileChatSession = DashboardSession | OrchestratorLink;

export function ChatSessionScreen({ session: boardSession }: { session: MobileChatSession }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const navigation = useNavigation();
	const router = useRouter();
	const openPage = useOpenPage();
	const [headerRightReady, setHeaderRightReady] = useState(false);
	useLayoutEffect(
		() => resetHeaderRightForSwap(
			() => navigation.setOptions(glassHeaderControl("right")),
			() => setHeaderRightReady(true),
		),
		[navigation],
	);
	const { config, currentHostId, connection, unreachable, projects, sessions, refresh: refreshBoard, setWorkerPinned, renameWorker, kill } = useApp();
	// The board's row, with its provisioning facts re-read every second while it starts.
	const session = useStartupSession(config, boardSession);
	const conversation = useMobileConversation(config, session.id);
	useChatViewLease(config, session.id);
	// A load that failed while the desktop was unreachable retries as soon as the
	// board's poll reconnects, which is what the offline state promises.
	// Keyed on a failed load, not a missing one, so the first mount doesn't send a
	// second request alongside the hook's own initial load.
	const loadFailed = !conversation.snapshot && Boolean(conversation.error);
	const refreshConversation = conversation.refresh;
	useEffect(() => {
		if (connection === "open" && loadFailed) void refreshConversation();
	}, [connection, loadFailed, refreshConversation]);
	const actionsEntryRef = useRef<ConversationActionsEntry | undefined>(undefined);
	const actionsListeners = useRef(new Set<(entry: ConversationActionsEntry) => void>());
	const interfaceSwitch = useInterfaceTransition(config, session.id, refreshBoard);
	const [menuOpen, setMenuOpen] = useState(false);
	const [collapsedReviewPRKey, setCollapsedReviewPRKey] = useState<string>();
	const [loadedReviewPromptStateKey, setLoadedReviewPromptStateKey] = useState<string>();
	const [jumpToSequence, setJumpToSequence] = useState<number>();
	const clearJumpToSequence = useCallback(() => setJumpToSequence(undefined), []);
	// An earlier prompt open in its inline editor takes the keyboard, so the
	// composer steps aside (still mounted, keeping its draft and attachments).
	const [editingMessage, setEditingMessage] = useState(false);
	// Which request the user pushed aside to type instead. It lives here because
	// both the composer and the timeline change shape depending on it.
	const [dismissedRequest, setDismissedRequest] = useState<number>();
	const restoreRequest = useCallback(() => setDismissedRequest(undefined), []);
	const [filePaths, setFilePaths] = useState<string[]>([]);
	const [filePathsTruncated, setFilePathsTruncated] = useState(false);
	const filePathsRequest = useRef<Promise<{ paths: string[]; truncated: boolean }> | null>(null);
	const [openingShell, setOpeningShell] = useState(false);
	const [resuming, setResuming] = useState(false);
	const [resumeError, setResumeError] = useState<string | undefined>();
	// Banners closed this visit, by what they report. See conversationBanners.
	const [dismissedBanners, setDismissedBanners] = useState<ReadonlySet<string>>(() => new Set());
	const dismissBanner = useCallback((key: string) => setDismissedBanners((current) => new Set(current).add(key)), []);
	// keyboard-controller's early events, corrected by React Native's when it
	// misses a transition. See useChatKeyboard.
	const chatKeyboard = useChatKeyboard();
	const keyboardVisible = chatKeyboard.keyboardVisible;
	const turnOptionsRequestedFor = useRef<string | undefined>(undefined);
	const terminated = "projectName" in session ? Boolean(session.isTerminal) : Boolean(session.isTerminated);
	const failedStart = "projectName" in session || session.provisionState !== "failed"
		? undefined
		: session.provisionError || "The session did not finish starting.";
	const interfaceTransitionActive = mobileInterfaceTransitionIsActive(interfaceSwitch.transition);
	useLayoutEffect(() => {
		const entry = actionsEntryRef.current;
		const snapshot = conversation.snapshot;
		if (!entry || entry.sessionId !== session.id || !snapshot) return;
		const liveEntry = {
			...entry,
			snapshot,
			modelLabel: turnSettingsModelLabel(snapshot, conversation.models, conversation.configOptions),
			openingShell,
			compacting: conversation.pendingActions.includes("compact"),
			mcpReloading: conversation.pendingActions.includes("mcp"),
			refreshing: conversation.refreshing,
			compactSupported: can(snapshot, "compaction") && !conversationActionUnsupported("compact", conversation.actionCodes.compact),
			mcpReloadSupported: can(snapshot, "mcp_reload") && !conversationActionUnsupported("mcp", conversation.actionCodes.mcp),
			interfaceSupported: Boolean(interfaceSwitch.status?.supported),
			interfaceReason: interfaceSwitch.status?.reason || interfaceSwitch.error,
			interfaceSwitching: interfaceTransitionActive || interfaceSwitch.starting,
		};
		actionsEntryRef.current = liveEntry;
		actionsListeners.current.forEach((listener) => listener(liveEntry));
	}, [conversation.snapshot, conversation.models, conversation.configOptions, conversation.pendingActions, conversation.refreshing, conversation.actionCodes, openingShell, interfaceSwitch.status, interfaceSwitch.error, interfaceSwitch.starting, interfaceTransitionActive, session.id]);
	const interfaceTransitionNotice =
		!interfaceTransitionActive &&
		!interfaceSwitch.transition?.noticeAcknowledgedAt &&
		(interfaceSwitch.transition?.phase === "failed" ||
			interfaceSwitch.transition?.phase === "recovery_required")
			? interfaceSwitch.transition
			: undefined;
	const interfaceTransitionRecovered =
		interfaceTransitionNotice?.phase === "recovery_required" &&
		interfaceTransitionNotice.errorCode === "DAEMON_RESTARTED";
	const interfaceTransitionNoticeText =
		interfaceTransitionNotice?.errorDetail ||
		(interfaceTransitionRecovered
			? `${PRODUCT_NAME} restored the session in its last committed interface.`
			: interfaceTransitionNotice?.phase === "recovery_required"
				? "The interface switch needs recovery before more work is sent."
				: "The interface switch failed; the current interface remains available.");
	const turnActive = Boolean(
		conversation.snapshot?.turns.some((turn) => turn.state === "running" || turn.state === "queued"),
	);
	const turnWaiting = Boolean(
		conversation.snapshot?.items.some(
			(item) =>
				item.kind === "activity" &&
				(item.activityKind === "approval" || item.activityKind === "user_input") &&
				item.status === "pending",
		),
	);

	useEffect(() => {
		if (!conversation.snapshot || turnOptionsRequestedFor.current === session.id) return;
		turnOptionsRequestedFor.current = session.id;
		void conversation.loadTurnOptions().catch(() => {});
	}, [conversation.loadTurnOptions, conversation.snapshot, session.id]);

	const sessionName = sessionTitle(session);
	// Codex-style: every surface that shows the agent changed files can open them.
	const filesNavigation = useMemo<FilesNavigation>(() => ({
		openChanges: () => router.push(filesRoute(session.id, currentHostId, sessionName)),
		openFile: (path, previousPath) => router.push(fileRoute(session.id, path, currentHostId, previousPath)),
	}), [currentHostId, router, session.id, sessionName]);
	// Re-read git's totals when a turn reports edits, not on a timer.
	const lastTurn = conversation.snapshot?.turns.at(-1);
	const changeSignal = `${changedPathCount(conversation.snapshot?.turns)}:${lastTurn?.id ?? ""}:${lastTurn?.state ?? ""}:${lastTurn?.diff?.files.length ?? 0}`;
	const changeSummary = useChangeSummary(config, session.id, changeSignal);
	// Desktop derives one name from the session (useWorkspaceQuery: displayName ??
	// issueId ?? id) and its chat header prefers it over the conversation's own
	// title (ChatWorkspace: sessionTitle || session.title || snapshot.title). This
	// had that precedence inverted, so renaming from the board changed the row but
	// left this header on the agent's auto-generated conversation title.
	const title = sessionName || conversation.snapshot?.title || session.id;
	const projectName = "projectName" in session
		? session.projectName
		: session.projectId ? projects.find((project) => project.id === session.projectId)?.name : "Standalone";
	const headerHarness = conversation.snapshot?.harness || session.harness || "Agent";
	const headerState = conversation.snapshot?.controller.state;
	const { reviewPromptPR, reviewPromptCollapsed, collapseReviewPrompt, expandReviewPrompt, reviewPromptSummary } = useReviewPromptState({ session, config, currentHostId, setLoadedReviewPromptStateKey, setCollapsedReviewPRKey, loadedReviewPromptStateKey, collapsedReviewPRKey });

	// The blocking request. It takes the composer's place until it is answered.
	// Computed here rather than at render because the back-swipe below is a hook
	// and cannot sit after this screen's early returns.
	const request = requestDockModel(conversation.snapshot, {
		approval: conversation.pendingActions.includes("approval"),
		input: conversation.pendingActions.includes("input"),
	});
	const requestDismissed = request ? dismissedRequest === request.sequence : false;
	// The timeline collapses a request the card is answering to a record of what
	// was asked — one live set of controls, never two.
	const answeredBelow = request && !requestDismissed && request.canAnswerInline ? request.sequence : undefined;
	// On iOS a left-to-right swipe is the screen's back gesture, and it beat the
	// card's own swipe — you got the board instead of the previous question. The
	// whole horizontal axis goes to the card while it is up; the header's back
	// button still leaves the session, and the card's ✕ still returns the
	// composer, so nothing becomes unreachable.
	const cardShowing = Boolean(request && !requestDismissed);
	useLayoutEffect(() => {
		navigation.setOptions({ gestureEnabled: !cardShowing });
	}, [cardShowing, navigation]);
	useLayoutEffect(() => {
		navigation.setOptions({
			headerTitle: () => (
				<ConversationTitle
					title={title}
					subtitle={[projectName, headerHarness].filter(Boolean).join(" · ")}
					harness={headerHarness}
					state={headerState}
				/>
			),
		});
	}, [headerHarness, headerState, navigation, projectName, title, t]);

	// The title is set on the first commit, above. Only the trailing control waits:
	// native-stack measures the replacement for a frame, and deferring the whole
	// options object meant the title arrived with it — iOS then animated the
	// header in from the top, after the screen had already landed.
	useLayoutEffect(() => {
		navigation.setOptions(
			headerRightReady
				? glassHeaderControl("right", (
					<View style={{ flexDirection: "row", alignItems: "center", gap: 4 }}>
						{reviewPromptCollapsed && reviewPromptPR ? (
							<Pressable
								testID="header-pullRequest"
								accessibilityRole="button"
								accessibilityLabel="Show PR card"
								onPress={() => { haptics.tap(); expandReviewPrompt(); }}
								style={({ pressed }) => [styles.reviewHeaderPRButton, { backgroundColor: pressed ? t.accentTint : t.bgElevatedHover }]}
							>
								<Feather name="git-pull-request" size={iconSize.lg} color={t.textSecondary} />
							</Pressable>
						) : null}
						<NativeHeaderButton icon="more" label="Conversation actions" onPress={() => { haptics.tap(); setMenuOpen(true); }} />
					</View>
				))
				: glassHeaderControl("right"),
		);
	}, [expandReviewPrompt, headerRightReady, navigation, reviewPromptCollapsed, reviewPromptPR, t]);

	const composerKey = config ? JSON.stringify([machineIdentity(config), session.id]) : session.id;
	const [quoteRequest, setQuoteRequest] = useState<ResponseQuoteRequest>();
	const quoteSequence = useRef(0);
	const addResponseToChat = useCallback((text: string, source?: ResponseQuoteRequest["source"]) => {
		if (!text.length || !source) return;
		setDismissedRequest(request?.sequence);
		setQuoteRequest({ id: `${Date.now()}-${++quoteSequence.current}`, text, source, composerKey });
	}, [composerKey, request?.sequence]);
	const quoteHandled = useCallback((id: string) => setQuoteRequest((current) => current?.id === id ? undefined : current), []);
	const { loadWorkspaceFiles, openTurnSettings, openShell, openLink, openArtifact, nameOfSession, wantSessionName } = useChatOpeners({ config, conversation, filePaths, filePathsTruncated, filePathsRequest, session, setFilePaths, setFilePathsTruncated, keyboardVisible, router, openingShell, setOpeningShell, setMenuOpen, currentHostId, sessions });
	const { resume, startupView, startupShown, timelineStartup } = useChatStartup({ resuming, setResuming, setResumeError, config, terminated, session, refreshBoard, conversation, refreshConversation });
	const { requestInterfaceSwitch } = useInterfaceSwitchRequest({ setMenuOpen, interfaceSwitch, turnActive, turnWaiting });
	useConversationActionsMenu({ conversation, menuOpen, setMenuOpen, session, actionsListeners, actionsEntryRef, sessionName, interfaceSwitch, interfaceTransitionActive, filesNavigation, router, setJumpToSequence, openShell, currentHostId, openPage, openTurnSettings, requestInterfaceSwitch, renameWorker, setWorkerPinned, kill, keyboardVisible, openingShell, title });
	const { interfaceTransitionBanner } = useInterfaceTransitionBanner({ interfaceSwitch });

	if (failedStart && !conversation.snapshot) return <Centered icon="alert-triangle" title="Session failed to start" message={failedStart} action={resuming ? "Retrying…" : "Retry"} onAction={() => void resume()} />;
	// A session that is still starting shows its checklist straight away rather than a bare spinner.
	if (conversation.loading && !conversation.snapshot && startupView && !startupView.failed) {
		return <View style={styles.startupLoading}><SessionStartupStatus startup={startupView} /></View>;
	}
	if (conversation.loading && !conversation.snapshot) return <Centered icon="message-square" title="Loading conversation…" spinning />;
	if (conversation.unavailable) return <Unavailable message={conversation.unavailable.message} onShell={() => void openShell()} openingShell={openingShell} />;
	// The board's poll is the app's view of the link: when it is down, say so in
	// the board's words instead of echoing whatever this request failed with.
	if (!conversation.snapshot && unreachable) return <Centered icon="wifi-off" title="This computer is offline" message="This conversation loads once the app reconnects." action="Retry" onAction={() => void conversation.refresh()} />;
	if (!conversation.snapshot) return <Centered icon="alert-triangle" title="Couldn't load the conversation" message={conversation.error || "The computer didn't return this conversation. Try again."} action="Retry" onAction={() => void conversation.refresh()} />;

	const snapshot = conversation.snapshot;
	const active = snapshot.turns.some((turn) => turn.state === "running" || turn.state === "queued");
	const brokenServers = brokenMcpServers(snapshot);
	const rolledBack = snapshot.turns.filter((turn) => turn.rolledBack).length;
	const quota = quotaWarning(snapshot.rateLimits);
	const compactSupported = can(snapshot, "compaction") && !conversationActionUnsupported("compact", conversation.actionCodes.compact);
	const mcpReloadSupported = can(snapshot, "mcp_reload") && !conversationActionUnsupported("mcp", conversation.actionCodes.mcp);
	const steerUnsupported = conversationActionUnsupported("steer", conversation.actionCodes.steer);

	return (
		// keyboard-controller's avoider, not React Native's. Both do the same
		// arithmetic; this one interpolates the padding with the keyboard's own
		// animation, so the composer travels with the keys. RN's re-renders once per
		// keyboard event on the JS thread, which is the single-frame jump up when
		// the keyboard opens.
		<KeyboardAvoidingView
			style={[styles.screen, chatKeyboard.rootStyle]}
			behavior={chatKeyboard.behavior}
			keyboardVerticalOffset={chatKeyboard.keyboardVerticalOffset}
		>
			{interfaceTransitionActive ? (
				<InlineBanner
					tone="warning"
					icon="repeat"
					title={interfaceTransitionBanner.text}
					action={interfaceTransitionBanner.action}
					secondary={interfaceTransitionBanner.secondary}
					onPress={interfaceTransitionBanner.onPress}
					onSecondary={interfaceTransitionBanner.onSecondary}
				/>
			) : interfaceTransitionNotice ? (
				<InlineBanner
					tone={interfaceTransitionRecovered ? "warning" : "danger"}
					icon={interfaceTransitionRecovered ? "check-circle" : "alert-triangle"}
					title={`${interfaceTransitionNoticeText}${
						interfaceSwitch.acknowledgeNoticeError
							? ` Couldn't dismiss: ${interfaceSwitch.acknowledgeNoticeError}`
							: ""
					}`}
					action={interfaceSwitch.acknowledgingNotice ? "Dismissing…" : "Dismiss"}
					onPress={
						interfaceSwitch.acknowledgingNotice
							? undefined
							: () =>
									void interfaceSwitch
										.acknowledgeNotice(interfaceTransitionNotice.id)
										.catch(() => {})
					}
				/>
			) : null}
			<ConversationBanners
				snapshot={snapshot}
				// The checklist reports setup, and a failed start with its Retry, in place.
				startFailure={startupShown ? undefined : failedStart}
				starting={startupShown}
				brokenServers={brokenServers}
				resuming={resuming}
				resumeError={resumeError}
				terminated={terminated}
				mcpReloading={conversation.pendingActions.includes("mcp")}
				mcpError={conversation.actionErrors.mcp}
				mcpReloadSupported={mcpReloadSupported}
				turnInFlight={active}
				onResume={() => void resume()}
				onReload={() => void conversation.reloadMcp().catch(() => {})}
				onOpenShell={() => void openShell()}
				dismissed={dismissedBanners}
				onDismiss={dismissBanner}
			/>
			{conversation.error ? <DismissibleBanner copy={errorBanner("load", conversation.error)} dismissed={dismissedBanners} onDismiss={dismissBanner} tone="danger" icon="wifi-off" action="Retry" onPress={() => void conversation.refresh()} /> : null}
			{quota ? <DismissibleBanner copy={quotaBanner(quota)} dismissed={dismissedBanners} onDismiss={dismissBanner} tone={quota.severity === "critical" ? "danger" : "warning"} icon="alert-triangle" action="Details" onPress={() => setMenuOpen(true)} /> : null}
			{conversation.actionError && conversation.actionError !== conversation.error ? <DismissibleBanner copy={errorBanner("action", conversation.actionError)} dismissed={dismissedBanners} onDismiss={dismissBanner} tone="danger" icon="alert-circle" /> : null}
			{rolledBack ? <DismissibleBanner copy={rolledBackBanner(rolledBack)} dismissed={dismissedBanners} onDismiss={dismissBanner} tone="muted" icon="rotate-ccw" /> : null}
			{conversation.pendingSends.map((pendingSend) => pendingSend.state === "failed" ? <InlineBanner key={pendingSend.id} tone="danger" icon="send" title="Message delivery uncertain" body={pendingSend.error || "Delivery failed"} action="Retry" secondary="Discard" onPress={() => void conversation.retrySend(pendingSend.id).catch(() => {})} onSecondary={() => void conversation.discardSend(pendingSend.id).catch(() => {})} /> : null)}
			<ChatLinkProvider onLinkOpen={openLink} onArtifactOpen={openArtifact} artifactSessionId={session.id} sessionName={nameOfSession} onSessionNameNeeded={wantSessionName} onAddToChat={conversation.snapshot?.supportsExcerpts ? addResponseToChat : undefined}>
				<FilesNavigationContext.Provider value={filesNavigation}>
				<ChatTimeline
					snapshot={snapshot}
					loadingOlder={conversation.loadingOlder}
					onLoadOlder={conversation.loadOlder}
					approvalPending={conversation.pendingActions.includes("approval")}
					inputPending={conversation.pendingActions.includes("input")}
					onDecide={conversation.resolveApproval}
					onResolveInput={conversation.resolveInput}
					onRollback={conversation.rollback}
					onEditMessage={conversation.editMessage}
					onEditingChange={setEditingMessage}
					jumpToSequence={jumpToSequence}
					onJumpHandled={clearJumpToSequence}
					answeredBelow={answeredBelow}
					startup={timelineStartup}
				/>
				</FilesNavigationContext.Provider>
			</ChatLinkProvider>
			{isOrchestratorChat(session) && session.projectId ? <AskStrip cfg={config} projectId={session.projectId} lane={askLaneOf(session)} /> : null}
			{isOrchestratorChat(session) && session.projectId ? <PlanCards cfg={config} projectId={session.projectId} /> : null}
			{editingMessage ? <View style={{ height: chatKeyboard.restingInset }} /> : null}
			<View style={editingMessage ? styles.composerHidden : undefined}>
				<ChatComposer
					key={composerKey}
					quoteRequest={quoteRequest?.composerKey === composerKey ? quoteRequest : undefined}
					onQuoteHandled={quoteHandled}
					reviewPR={reviewPromptPR}
					reviewPRSummary={reviewPromptSummary}
					reviewPRCollapsed={reviewPromptCollapsed}
					onCollapseReviewPR={collapseReviewPrompt}
					onOpenReview={reviewPromptPR ? () => openPage(reviewRouteForPR(session.id, reviewPromptPR, currentHostId)) : undefined}
					sessionId={session.id}
					config={config}
					snapshot={snapshot}
					changes={changeSummary?.files ? { ...changeSummary, onOpen: filesNavigation.openChanges } : undefined}
					quotaActive={Boolean(quota)}
					request={request}
					requestDismissed={requestDismissed}
					onRequestDecide={conversation.resolveApproval}
					onRequestResolveInput={conversation.resolveInput}
					onShowRequest={setJumpToSequence}
					onDismissRequest={() => setDismissedRequest(request?.sequence)}
					onRestoreRequest={restoreRequest}
					skills={conversation.skills}
					filePaths={filePaths}
					filePathsTruncated={filePathsTruncated}
					onLoadSkills={conversation.loadSkills}
					onLoadFiles={loadWorkspaceFiles}
					configOptions={conversation.configOptions}
					models={conversation.models}
					steerUnavailable={steerUnsupported}
					disabled={interfaceTransitionActive || Boolean(failedStart)}
					pending={mobileInterfaceTransitionIsBusy(interfaceSwitch.transition) || conversation.pendingSends.some((item) => item.state === "sending")}
					interrupting={conversation.pendingActions.includes("interrupt")}
					onSend={conversation.send}
					onAcknowledgeSend={conversation.acknowledgeSend}
					completedRetry={conversation.completedRetry}
					onSteer={conversation.steer}
					onPromoteQueuedTurn={conversation.promoteQueuedTurn}
					onCancelQueuedTurn={conversation.cancelQueuedTurn}
					onInterrupt={() => void conversation.interrupt().catch(() => {})}
					onOpenSettings={() => void openTurnSettings()}
					onSettings={conversation.chooseSettings}
					onConfigOption={conversation.setConfigOption}
					restingInset={chatKeyboard.restingInset}
					dockRiseOverride={chatKeyboard.dockRiseOverride}
					openingTurnId={startupView?.openingTurnId}
					orchestrator={isOrchestratorChat(session)}
					startingAgentName={startupShown && !startupView?.failed ? startupView?.agentName : undefined}
				/>
			</View>
		</KeyboardAvoidingView>
	);
}

function sessionTitle(session: MobileChatSession): string { return "displayName" in session ? session.displayName || session.issueTitle || session.issueLabel || session.id : session.projectName || session.id; }
export function interfacePhaseLabel(phase?: string): string {
	switch (phase) {
		case "draining": return "finishing current work";
		case "source_stopping": return "stopping Chat controller";
		case "source_stopped": return "Chat controller stopped";
		case "target_starting": return "resuming terminal controller";
		case "activating": return "opening Terminal UI";
		default: return "preparing native handoff";
	}
}
export function signInCommand(harness: string): string | undefined { return harness === "codex" ? "codex login" : harness === "claude-code" || harness === "claude" ? "claude auth login" : undefined; }
