import { useSessionChatSurface } from "./useSessionChatSurface";
import type { SessionChatSurfaceProps } from "./SessionChatSurfaceProps";
import { createPortal } from "react-dom";
import { agentLabel } from "../../lib/agent-options";
import { memo } from "react";
import { conversationQueryKey, ProviderSwitchFailed } from "../../hooks/conversation";
import { can } from "../../types/conversation";
import { ComposerErrorNotice, switchEffort } from "./AgentSwitchPicker";
import { apiErrorMessage } from "../../lib/api-client";
import { AGENT_SWITCH_FAILED } from "./composer/composer-model";
import { CONTINUE_STOPPED_TURN_PROMPT, ChatWorkspace } from "./ChatWorkspace";
import { withUltracode } from "./turn-settings/composer-menu";


/**
 * The central surface for a chat-mode session.
 *
 * Mounted by SessionView when the session's persisted mode is `chat`. It owns the
 * conversation query and command wiring so ChatWorkspace stays a pure view of a
 * snapshot — which is what lets the same component render fixtures in the dev
 * preview and live data here.
 */
export const SessionChatSurface = memo(function SessionChatSurface(props: SessionChatSurfaceProps) {
	const {
		sideChatContainer,
		session,
		hostId,
		assetBaseUrl,
		reviewerTerminal,
		onOpenReviewerTerminal,
		reviewerChat,
		onOpenReviewerChat,
		reviewerChatSelected,
		onSessionRenamed,
		reviewerTarget,
		onSelectChat,
		shellTerminals,
		shellTarget,
		onSelectShellTerminal,
		onCloseShellTerminal,
		onRenameShellTerminal,
		daemonReady,
		theme,
		onOpenShell,
		openingShell,
		shellError,
		onOpenFiles,
		onOpenFile,
		headerActions,
		sessionTabAction,
		sessionTabActionWide = false,
		tabStripAction,
		workspaceTabs,
		workspaceTabActions,
		workspaceActiveTabKey,
		auxiliaryTabOrder,
		onAuxiliaryTabOrderChange,
		controllerTransitioning,
		accountMoving,
		agentResuming,
		controllerResumeError,
		newWorkDisabled,
		interfaceSwitch,
	} = props;
	const controller = useSessionChatSurface(props);
	if ("stateView" in controller) return controller.stateView;
	const {
		agentSwitch,
		annotationNavigationRequest,
		attachToSide,
		clearSwitchError,
		commands,
		configOptions,
		continueIntent,
		controllerBusy,
		controllerCatalogsEnabled,
		currentHarness,
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
	} = controller;
	return (
		<div className="relative h-full min-h-0">
			{refreshError ? <p role="alert" className="px-4 py-2 text-xs text-destructive">{refreshError}</p> : null}
			{sideChatContainer ? createPortal(sideChats.panel, sideChatContainer) : null}
			<ChatWorkspace
				onAddToSideChat={sideEnabled && (sideChats.activeId || sideCreationEnabled) ? attachToSide : undefined}
				annotationNavigationRequest={annotationNavigationRequest}
				key={uiSessionId}
				uiSessionId={uiSessionId}
				assetBaseUrl={assetBaseUrl}
				remoteHostId={hostId}
				snapshot={renderSnapshot}
				promptSuggestion={promptSuggestion}
				newWorkDisabled={newWorkDisabled}
				onLinkOpen={openLinkInBrowser}
				onSessionLinkOpen={openSessionLink}
				sessionTitle={session.title}
				sessionRole={session.kind}
				session={session}
				onSessionRenamed={onSessionRenamed}
				reviewerTerminal={reviewerTerminal}
				onOpenReviewerTerminal={onOpenReviewerTerminal}
				reviewerChat={reviewerChat}
				onOpenReviewerChat={onOpenReviewerChat}
				reviewerChatSelected={reviewerChatSelected}
				reviewerTarget={reviewerTarget}
				onSelectChat={onSelectChat}
				shellTerminals={shellTerminals}
				shellTarget={shellTarget}
				onSelectShellTerminal={onSelectShellTerminal}
				onCloseShellTerminal={onCloseShellTerminal}
				onRenameShellTerminal={onRenameShellTerminal}
				daemonReady={daemonReady}
				theme={theme}
				headerActions={headerActions}
				sessionTabAction={sessionTabAction}
				sessionTabActionWide={sessionTabActionWide}
				tabStripAction={tabStripAction}
				workspaceTabs={workspaceTabs}
				workspaceTabActions={workspaceTabActions}
				workspaceActiveTabKey={workspaceActiveTabKey}
				auxiliaryTabOrder={auxiliaryTabOrder}
				onAuxiliaryTabOrderChange={onAuxiliaryTabOrderChange}
				controllerTransitioning={controllerTransitioning}
				accountMoving={accountMoving}
				loadingQuietly={optimisticChat && !optimisticArrival && session.provisionState !== "provisioning"}
				agentResuming={agentResuming}
				hasOlder={hasOlder}
				loadingOlder={isLoadingOlder}
				onLoadOlder={loadOlder}
				busy={commands.busy}
				excerptsEnabled
				onSend={async (text, attachments, clientMessageId, excerpts) => {
					// Only claim /btw and /side where side chats can open. Elsewhere the
					// text goes to the agent as typed (it may be the harness's own skill),
					// so a failed side launch can never strand the main draft.
					const btw = sideCreationEnabled ? /^\/(?:btw|side)(?:\s+|$)/i.exec(text) : null;
					if (btw) {
						openSideInspector();
						const side = await sideChats.create(undefined, true, clientMessageId);
						const question = text.slice(btw[0].length).trim();
						if (question || excerpts?.length || attachments?.length) {
							// Retain the main draft if handing the request to the side fails.
							await sideChats.send(side.id, question, attachments, excerpts, true, clientMessageId);
						}
						return;
					}
					const viaClaude = (pendingSwitch?.harness ?? currentHarness) === "claude-code";
					const message = {
						text: viaClaude && (pendingSwitch ? pendingSwitch.ultracode : ultracode.on) ? withUltracode(text) : text,
						attachments,
						clientMessageId,
						excerpts: excerpts?.map((excerpt) => ({
							conversationId: excerpt.conversationId,
							messageId: excerpt.messageId,
							revision: excerpt.revision,
							text: excerpt.text,
						})),
					};
					// A new message replaces an earlier switch's failure.
					clearSwitchError();
					if (!pendingSwitch) return commands.send(message);
					// The bar shows the mode the agent will start in only once its modes are known
					// (Pi runs only with permissions bypassed); nothing goes before then.
					if (!pendingPermissionModes) {
						const reason = { message: t("agentSwitch.checkingPermissionModes", { agent: agentLabel(pendingSwitch.harness) }) };
						setSendSwitchError(reason);
						throw Object.assign(new Error(reason.message), { code: AGENT_SWITCH_FAILED });
					}
					const { harness: targetHarness, model, fastMode, autoCompactWindow } = pendingSwitch;
					const effort = switchEffort(pendingSwitch);
					const switchRequest = { targetHarness, model, effort, fastMode, autoCompactWindow, permissionMode: pendingSwitch.approvalMode };
					// The transcript draws the "Switched" line from this while the daemon works.
					const switchDisplay = currentHarness
						? { sourceHarness: currentHarness, sourceModel: snapshot?.settings?.model, targetModel: model, targetEffort: effort }
						: undefined;
					// Nothing was sent if the switch fails, so the draft stays editable. A refused
					// switch drops the pick; with no clear answer it stays until the chat shows its agent.
					const switchFailed = (error: unknown) => {
						if (error instanceof ProviderSwitchFailed) setChosenSwitch(undefined);
						void queryClient.invalidateQueries({ queryKey: conversationQueryKey(session.id, hostId) });
						return Object.assign(new Error(apiErrorMessage(error)), { code: AGENT_SWITCH_FAILED });
					};
					const switched = () => {
						setChosenSwitch(undefined);
						// Ultracode picked with the switch stays on for the messages after it.
						ultracode.onChange(Boolean(pendingSwitch.ultracode));
					};
					// The daemon switches and sends in one request, attachments included, so no other
					// switch can take the message; it shows at once.
					try {
						const sent = await commands.send({ ...message, providerSwitch: switchRequest, switchDisplay });
						switched();
						return sent;
					} catch (error) {
						if (error instanceof ProviderSwitchFailed) {
							setSendSwitchError(error.reason);
							throw switchFailed(error);
						}
						// The switch went through and only the send failed, or no answer came. The chat is
						// read back; once it runs the picked agent the pick drops, so a retry is an ordinary send.
						void queryClient.invalidateQueries({ queryKey: conversationQueryKey(session.id, hostId) });
						throw error;
					}
				}}
				commandError={commands.error}
				agentSwitch={agentSwitch}
				composerNotice={interfaceSwitch?.errorNotice ?? (switchError ? (
					<ComposerErrorNotice
						title={t("agentSwitch.couldNotSwitch")}
						message={apiErrorMessage(switchError)}
						onDismiss={clearSwitchError}
					/>
				) : undefined)}
				onDecide={commands.resolve}
				onResolveInput={commands.resolveInput}
				onInterrupt={commands.interrupt}
				onContinueTurn={() => {
					const turnId = `${renderSnapshot.conversationId}:${renderSnapshot.activeBranchId ?? ""}:${stoppedTurnId}`;
					if (continueIntent.current?.turnId !== turnId) {
						continueIntent.current = { turnId, clientMessageId: crypto.randomUUID(), sequence: renderSnapshot.latestSequence };
					}
					return commands.send({
						text: CONTINUE_STOPPED_TURN_PROMPT,
						continuation: true,
						clientMessageId: continueIntent.current.clientMessageId,
					});
				}}
				onResumeAgent={() => {
					void commands.resumeAgent().catch(() => {});
				}}
				resumingAgent={commands.resumingAgent || (renderSnapshot.controller.state === "hibernated" && controllerBusy)}
				resumeError={commands.resumeError ?? controllerResumeError}
				onOpenShell={onOpenShell}
				openingShell={openingShell}
				shellError={shellError}
				// Until the agent's own settings arrive the bar shows the saved ones, disabled. With
				// none saved yet it waits, so defaults never pass for the chat's own.
				settingsReady={!optimisticChat && (targetChatControllerReady || agentResuming || waking) &&
					(!settingsLoading || Boolean(renderSnapshot.settings?.model || renderSnapshot.settings?.approvalMode))}
				settingsLoading={settingsLoading}
				lastModel={lastModel}
				models={models}
				onChooseSettings={hasProviderMode ? undefined : commands.chooseSettings}
				onRememberPermissions={can(renderSnapshot, "config_options") && !configOptions.loaded
					? undefined : projectPermissions.remember}
				rememberPermissionsPending={projectPermissions.pending}
				rememberPermissionsError={projectPermissions.error}
				rememberedPermissionMode={projectPermissions.savedMode}
				configOptions={offeredConfigOptions}
				onChooseConfigOption={configOptions.setOption}
				configOptionPending={configOptions.pending || commands.choosingSettings}
				configOptionError={controllerCatalogsEnabled ? configOptions.error : undefined}
				onCompact={commands.compact}
				compacting={commands.compacting}
				compactUnavailable={commands.compactUnavailable}
				onRollback={commands.rollback}
				rollbackPending={commands.rollbackPending}
				rollbackError={commands.rollbackError}
				onOpenFiles={onOpenFiles}
				onOpenFile={onOpenFile}
				retryControl={commands.retryControl}
				onEditMessage={commands.editMessage}
				editMessagePending={commands.editMessagePending}
				editMessageError={commands.editMessageError}
				onActivateBranch={commands.activateBranch}
				activateBranchPending={commands.activateBranchPending}
				activateBranchError={commands.activateBranchError}
				skills={sideCreationEnabled ? [...(["btw", "side"].map((name) => ({ name, displayName: name, description: "Open a new side chat", source: "Tallpine" }))), ...skills.filter((skill) => skill.name !== "btw" && skill.name !== "side")] : skills}
				filePaths={paths}
				filePathsTruncated={truncated}
				localEchos={localEchos}
				onStageAttachments={stageAttachments}
				nativeImages={can(renderSnapshot, "images")}
				// Gated on what the daemon advertises, so the control is never drawn for a
				// harness that cannot steer. The refusal check stays as a backstop: it
				// covers the window before the controller reports, and it is the last word
				// afterwards, since the capability is a property of the driver.
				onSteer={can(renderSnapshot, "steer") && !commands.steerUnsupported ? commands.steer : undefined}
				sendPending={commands.sendPending}
				steerPending={commands.steerPending}
				steerRefusal={commands.steerRefusal}
				onPromoteQueuedTurn={
					can(renderSnapshot, "steer") && !commands.steerUnsupported
						? commands.promoteQueuedTurn
						: undefined
				}
				onEditQueuedTurn={commands.editQueuedTurn}
				onCancelQueuedTurn={commands.cancelQueuedTurn}
				onReorderQueuedTurns={commands.reorderQueuedTurns}
				promoteQueuedTurnPendingTurnId={commands.promoteQueuedTurnPendingTurnId}
				cancelQueuedTurnPendingTurnId={commands.cancelQueuedTurnPendingTurnId}
				editQueuedTurnPendingTurnId={commands.editQueuedTurnPendingTurnId}
			/>
		</div>
	);
});
