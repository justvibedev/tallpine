import { useState } from "react";
import { cn } from "../../../lib/utils";
import { agentLabel } from "../../../lib/agent-options";
import { OrchestratorStartupStatus, RolledBackNotice } from "../ChatStartupNotices";
import { SkillsOfferCard } from "../../SkillsOfferCard";
import { AskStrip } from "../../ask/AskStrip";
import { PlanCards } from "../../plan/PlanCards";
import { WelcomePromptSuggestions } from "../WelcomePromptSuggestions";
import { Timeline } from "../Timeline";
import { ChatHostId, ChatPromptSender } from "../ChatTimelineItems";
import { ChatLinkProvider } from "../ChatMarkdown";
import { ChatImageSourceProvider } from "../chat-image-source";
import { ChatComposer } from "../ChatComposer";
import { isPercentContextHarness } from "../../../lib/context-view";
import { ContextMeter } from "../ContextMeter";
import { ComposerModeItems } from "../turn-settings/composer-menu";
import { McpServerBanner } from "../ChatStatusBanners";
import { UsageLimitNotice } from "../UsageLimitNotice";
import { can } from "../../../types/conversation";
import { ChatDropOverlay } from "../ChatDropOverlay";
import type { ChatContentState } from "./chat-content-state";
import type { ChatWorkspaceContentProps } from "./chat-content-props";

/** Timeline, queued messages and composer of the chat workspace. */
export function ConversationBody({ props, v }: { props: ChatWorkspaceContentProps; v: ChatContentState }) {
	const {
		onAddToSideChat,
		snapshot,
		promptSuggestion,
		sessionRole = "worker",
		assetBaseUrl,
		workspaceActiveTabKey,
		controllerTransitioning,
		loadingQuietly,
		agentResuming = false,
		agentInputDisabled = false,
		newWorkDisabled = false,
		session,
		hasOlder,
		loadingOlder,
		onLoadOlder,
		excerptsEnabled = false,
		onDecide,
		onContinueTurn,
		commandError,
		resumingAgent,
		onLinkOpen,
		onSessionLinkOpen,
		busy,
		models,
		agentSwitch,
		composerNotice,
		onChooseSettings,
		configOptions,
		onChooseConfigOption,
		configOptionPending,
		onCompact,
		compacting,
		compactUnavailable,
		rollbackPending,
		onOpenFiles,
		onOpenFile,
		retryControl,
		editMessagePending,
		editMessageError,
		onActivateBranch,
		activateBranchPending,
		activateBranchError,
		skills,
		filePaths,
		filePathsTruncated,
		localEchos,
		onStageAttachments,
		nativeImages,
		onSteer,
		showSteerButton,
		sendPending,
		steerPending,
		steerRefusal,
		editQueuedTurnPendingTurnId,
		draftScope,
		draftPersistenceAvailable,
	} = props;
	const {
		acceptedClientMessageIds,
		activeRemoteHostId,
		annotationNavigationRef,
		arrivingInChat,
		arrivingPlaceholder,
		arrivingWithHistory,
		artifactSession,
		brokenServers,
		canContinue,
		cancelQueuedEdit,
		changeQueuedDraft,
		changeQueuedRetainedAttachments,
		changeQueuedStagedAttachments,
		compactStartup,
		compactsAt,
		composerApproval,
		composerBoxRef,
		composerDockRef,
		composerDraftSeed,
		composerElicitation,
		composerEmpty,
		composerFocusRef,
		composerQueuedDock,
		composerSettings,
		conversationEmpty,
		discarded,
		draftScopeKey,
		editHumanMessage,
		emptyChatPlaceholder,
		handleComposerSend,
		limitedTurn,
		navigateToAnnotation,
		openPlanTab,
		orchestratorStarting,
		promptSender,
		queueDraftError,
		queueEdit,
		queuedMessages,
		reviewerActive,
		rollbackTarget,
		sentMessages,
		setComposerEmpty,
		setupPlaceholder,
		shellActive,
		stableInterrupt,
		stableSettings,
		startup,
		startupReconnecting,
		steer,
		suppressStopped,
		t,
		turn,
		uiSessionId,
		welcomeLeaving,
	} = v;
	const [openAsks, setOpenAsks] = useState(0);
	// The pinned "Waiting on you" strip is the one surface while an ask is open: no hero or suggestions beside it.
	const welcomeVisible = conversationEmpty && openAsks === 0;
	return (
		<>
			<ChatDropOverlay
				enabled={!agentInputDisabled && !newWorkDisabled}
				onFiles={(files) => composerFocusRef.current?.attachFiles(files) ?? false}
			>
					<div
						className={cn("flex min-h-0 flex-1 flex-col", conversationEmpty && "justify-center", arrivingWithHistory && snapshot.items.length === 0 && "justify-end")}
						data-composer-placement={conversationEmpty ? "center" : "dock"}
					>
						<ChatLinkProvider
							onLinkOpen={onLinkOpen}
							onFileOpen={onOpenFile}
							onSessionLinkOpen={onSessionLinkOpen}
							remoteHost={Boolean(activeRemoteHostId)}
							sessionLinkHostId={activeRemoteHostId}
							workspacePaths={filePaths}
						>
							<ChatImageSourceProvider sessionId={snapshot.sessionId} assetBaseUrl={assetBaseUrl} remoteHost={Boolean(activeRemoteHostId)} artifacts={session?.artifactFiles}>
							<ChatHostId.Provider value={activeRemoteHostId}>
							<ChatPromptSender.Provider value={promptSender}>

								<Timeline
							onAddToSideChat={onAddToSideChat}
									workerUpdates={sessionRole === "orchestrator"}
									annotationNavigationRef={annotationNavigationRef}
									key={draftScopeKey}
									snapshot={snapshot}
									assetBaseUrl={assetBaseUrl}
									remoteHost={Boolean(activeRemoteHostId)}
									draftScope={draftScope}
									draftPersistenceAvailable={draftPersistenceAvailable}
									hasOlder={hasOlder}
									loadingOlder={loadingOlder}
									onLoadOlder={onLoadOlder}
									excerptsEnabled={excerptsEnabled}
									onDecide={onDecide}
									busy={busy}
									onRollback={rollbackTarget}
									onOpenPlan={openPlanTab}
									hasPlanTab={Boolean(session)}
									onOpenFiles={onOpenFiles}
									onOpenFile={onOpenFile}
									retryControl={retryControl}
									composerRecoveryTurnId={retryControl ? limitedTurn?.id : undefined}
									onEditHumanMessage={draftPersistenceAvailable ? editHumanMessage : undefined}
									editPending={editMessagePending}
									editBusy={Boolean(turn)}
									editError={editMessageError}
									onActivateBranch={onActivateBranch}
									activateBranchPending={activateBranchPending}
									activateBranchError={activateBranchError}
									newWorkDisabled={newWorkDisabled}
									rollbackDisabled={Boolean(turn || rollbackPending || newWorkDisabled)}
									localEchos={localEchos}
									startup={sessionRole === "orchestrator" ? undefined : startup}
									arriving={arrivingInChat}
								/>
							</ChatPromptSender.Provider>
							</ChatHostId.Provider>
							</ChatImageSourceProvider>
						</ChatLinkProvider>

						<div ref={composerDockRef} className="cursor-chat-composer-dock shrink-0 px-4 pb-3">
							<div aria-hidden="true" className="chat-composer-fade" />
							<div
								data-empty={conversationEmpty || undefined}
								className="mx-auto flex w-full max-w-3xl flex-col gap-2 transition-[max-width] duration-500 ease-out data-[empty]:max-w-2xl"
							>
								{discarded > 0 ? <RolledBackNotice count={discarded} /> : null}
								{sessionRole === "orchestrator" && !activeRemoteHostId && session?.workspaceId ? <AskStrip projectId={session.workspaceId} lane={session.lane} onOpenCount={setOpenAsks} /> : null}
								{sessionRole === "orchestrator" && !activeRemoteHostId && session?.workspaceId ? <PlanCards projectId={session.workspaceId} sessionId={session.id} lane={session.lane} /> : null}
								{!activeRemoteHostId && session?.workspaceId ? <SkillsOfferCard projectId={session.workspaceId} sessionId={session.id} /> : null}
								{limitedTurn?.usageLimit && retryControl ? (
									<UsageLimitNotice
										key={limitedTurn.id}
										usageLimit={limitedTurn.usageLimit}
										onResume={() => { void Promise.resolve(retryControl.retry(limitedTurn.id)).catch(() => undefined); }}
										onAutoResumeChange={retryControl.setAutoResume
											? (enabled) => { void Promise.resolve(retryControl.setAutoResume?.(limitedTurn.id, enabled)).catch(() => undefined); }
											: undefined}
										pending={retryControl.pending || retryControl.autoResumePending}
										disabled={Boolean(turn || busy || newWorkDisabled || agentInputDisabled || controllerTransitioning || agentResuming)}
										error={(retryControl.turnId === limitedTurn.id ? retryControl.error : undefined) ??
											(retryControl.autoResumeTurnId === limitedTurn.id ? retryControl.autoResumeError : undefined)}
									/>
								) : null}
								{welcomeVisible ? (
									<h1 className="mb-5 text-center text-2xl font-normal tracking-tight text-foreground sm:text-3xl">
										{t(artifactSession ? "chat.welcome.artifactHeading" : "chat.welcome.heading")}
									</h1>
								) : null}
								<WelcomePromptSuggestions
									open={welcomeVisible && !newWorkDisabled && composerEmpty}
									artifact={artifactSession}
									onPick={(text) => composerFocusRef.current?.setDraft(text)}
								/>
								<div ref={composerBoxRef} className="relative">
									{welcomeLeaving ? (
										<h1
											aria-hidden="true"
											className="chat-welcome-leaving pointer-events-none absolute inset-x-0 bottom-full mb-5 text-center text-2xl font-normal tracking-tight text-foreground sm:text-3xl"
										>
											{t(artifactSession ? "chat.welcome.artifactHeading" : "chat.welcome.heading")}
										</h1>
									) : null}
									<McpServerBanner
										key={draftScopeKey}
										sessionId={uiSessionId}
										incarnation={draftScope.incarnation}
										servers={brokenServers}
										placement={conversationEmpty ? "below" : "above"}
										active={!workspaceActiveTabKey && !reviewerActive && !shellActive}
									/>
									{composerNotice}
									<ChatComposer
										attachedTop={Boolean(composerNotice)}
										onSelectAnnotation={navigateToAnnotation}
										focusRef={composerFocusRef}
										key={`${draftScopeKey}:${draftPersistenceAvailable ? "saved" : "memory"}:${queueEdit ? `${queueEdit.turnId}:${queueEdit.ownerId ?? queueEdit.expectedRevision ?? "legacy"}` : "composer"}`}
										queuedDock={composerQueuedDock}
										approval={composerApproval}
										elicitation={composerElicitation}
										onSend={handleComposerSend}
										sentMessages={sentMessages}
										sentMessagesScope={`${snapshot.conversationId}:${snapshot.activeBranchId ?? ""}`}
										draftSeed={composerDraftSeed}
										promptSuggestion={!queueEdit && !composerApproval && !composerElicitation && !turn &&
											!busy && !sendPending && !steerPending && !agentInputDisabled && !newWorkDisabled &&
											!controllerTransitioning && !loadingQuietly && !agentResuming &&
											!workspaceActiveTabKey && !reviewerActive && !shellActive && snapshot.controller.state === "ready"
											? promptSuggestion : undefined}
										editingQueuedTurnId={queueEdit?.turnId}
										queuedEditRecovery={Boolean(queueEdit?.clientMessageId)}
										savingQueuedEditPending={Boolean(
											queueEdit?.turnId &&
												editQueuedTurnPendingTurnId === queueEdit.turnId,
										)}
										onCancelQueuedEdit={cancelQueuedEdit}
										onQueuedDraftChange={queueEdit ? changeQueuedDraft : undefined}
										onDraftEmptyChange={setComposerEmpty}
										queuedDraftScope={queueEdit ? draftScope : undefined}
										onQueuedAttachmentsChange={changeQueuedStagedAttachments}
										onQueuedRetainedAttachmentsChange={changeQueuedRetainedAttachments}
										onInterrupt={turn && !newWorkDisabled ? stableInterrupt : undefined}
										onContinue={canContinue && !queueEdit ? onContinueTurn : undefined}
										commandError={queueDraftError ?? (queueEdit && !queueEdit.clientMessageId && !queuedMessages.some((entry) => entry.turnId === queueEdit.turnId) ? "chat.draft.queueMissing" : commandError)}
										settings={<>
											{agentSwitch ? null : <ContextMeter usage={snapshot.usage} compactsAt={compactsAt} percentOnly={isPercentContextHarness(snapshot.harness)} />}
											{composerSettings}
										</>}
										addMenuItems={agentSwitch ? (
											<ComposerModeItems
												configOptions={configOptions ?? []}
												settings={stableSettings}
												models={models}
												nativePlan={can(snapshot, "plan_mode")}
												onChange={newWorkDisabled ? undefined : onChooseSettings}
												onChangeConfigOption={newWorkDisabled ? undefined : onChooseConfigOption}
												disabled={configOptionPending || controllerTransitioning}
											/>
										) : undefined}
										busy={busy}
										willQueue={Boolean(turn) || (sessionRole !== "orchestrator" && session?.provisionState === "provisioning")}
										queuePlaceholder={
											session?.provisionState === "provisioning"
												? t("chat.startup.queuePlaceholder", { agent: agentLabel(snapshot.harness) })
												: undefined
										}
										starting={orchestratorStarting || arrivingInChat}
										disabled={(orchestratorStarting || loadingQuietly || (snapshot.controller.state === "stopped" && !suppressStopped && (!resumingAgent || session?.provisionState === "failed")) || controllerTransitioning || newWorkDisabled) && !queueEdit?.clientMessageId}
										disabledPlaceholder={
											arrivingInChat
												? arrivingPlaceholder
												: orchestratorStarting
												? setupPlaceholder
												: controllerTransitioning || newWorkDisabled
												? ""
												: agentResuming
													? (sessionRole === "orchestrator" ? "Starting the orchestrator" : "Resuming agent")
													: startupReconnecting
														? "Reconnecting to the agent…"
														: undefined
										}
										// Keep the composer useful outside the centered welcome state too. A
										// task can have non-message activity before its first visible chat
										// message, and the generic placeholder makes a still-empty composer
										// look like a regression.
										emptyPlaceholder={conversationEmpty ? (artifactSession ? t("chat.welcome.artifactPlaceholder") : emptyChatPlaceholder) : undefined}
										skills={skills}
										filePaths={filePaths}
										filePathsTruncated={filePathsTruncated}
										onStageAttachments={newWorkDisabled ? undefined : onStageAttachments}
										nativeImages={queueEdit?.clientMessageId ? queueEdit.nativeImages ?? nativeImages : nativeImages}
										autoFocus={!reviewerActive}
										autoFocusKey={draftScope.sessionId}
										// Steering is only meaningful into a turn that is running. A queued turn
										// has not reached the provider, so there is nothing to steer.
										onSteer={newWorkDisabled ? undefined : steer}
										showSteerButton={showSteerButton}
										canSteer={Boolean(onSteer) && turn?.state === "running"}
										sendPending={sendPending || agentResuming}
										steerPending={steerPending}
										steerRefusal={steerRefusal}
										onCompact={newWorkDisabled || snapshot.controller.state === "hibernated" ? undefined : onCompact}
										compacting={compacting}
										compactUnavailable={compactUnavailable}
										compactBlocked={Boolean(turn)}
										draftSessionId={draftPersistenceAvailable && !queueEdit ? draftScope.sessionId : undefined}
										draftSessionIncarnation={draftPersistenceAvailable ? draftScope.incarnation : undefined}
										assetBaseUrl={assetBaseUrl}
										assetSessionId={snapshot.sessionId}
										remoteHost={Boolean(activeRemoteHostId)}
										acceptedClientMessageIds={acceptedClientMessageIds}
									/>
								</div>
								{compactStartup ? <OrchestratorStartupStatus {...compactStartup} /> : null}
							</div>
						</div>
					</div>
			</ChatDropOverlay>
		</>
	);
}
