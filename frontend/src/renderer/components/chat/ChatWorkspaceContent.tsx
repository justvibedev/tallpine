import type { WorkspaceSession } from "../../types/workspace";
import { type CSSProperties } from "react";
import { TriangleAlert } from "lucide-react";
import { ChatHeader } from "./ChatHeader";
import { ControllerBanner } from "./ControllerBanner";
import { CHAT_FONT_SIZE_DEFAULT } from "./chat-workspace-constants";
import { ConfirmDialog } from "../ConfirmDialog";
import { BackgroundTasksBanner } from "./BackgroundTasksBanner";
import { ReauthBanner, ThreadStateBanner, VersionSwitchBanner } from "./ChatStatusBanners";
import { useChatContentData } from "./workspace-content/useChatContentData";
import { useChatContentTabs } from "./workspace-content/useChatContentTabs";
import { useChatContentStatus } from "./workspace-content/useChatContentStatus";
import type { ChatContentState } from "./workspace-content/chat-content-state";
import type { ChatWorkspaceContentProps } from "./workspace-content/chat-content-props";
import { AuxiliaryTabPanels } from "./workspace-content/AuxiliaryTabPanels";
import { ConversationBody } from "./workspace-content/ConversationBody";

export function ChatWorkspaceContent(props: ChatWorkspaceContentProps) {
	const {
		snapshot,
		sessionTitle,
		sessionRole = "worker",
		headerActions,
		sessionTabAction,
		sessionTabActionWide = false,
		tabStripAction,
		workspaceTabActions,
		workspaceActiveTabKey,
		controllerTransitioning,
		agentResuming = false,
		agentInputDisabled = false,
		newWorkDisabled = false,
		onOpenReviewerTerminal,
		onOpenReviewerChat,
		hideHeader = false,
		inlineHeader = false,
		session,
		onSessionRenamed,
		onSelectChat,
		shellTarget,
		onSelectShellTerminal,
		onCloseShellTerminal,
		onRenameShellTerminal,
		onResumeAgent,
		resumingAgent,
		resumeError,
		onOpenShell,
		openingShell,
		shellError,
		onRollback,
		rollbackPending,
		rollbackError,
		draftPersistenceAvailable,
	} = props;
	const data = useChatContentData(props);
	const tabs = useChatContentTabs(props, data);
	const status = useChatContentStatus(props, data);
	const v: ChatContentState = { ...data, ...tabs, ...status };
	const {
		activeRemoteHostId,
		confirming,
		handleChatKeyDown,
		handleChatSurfaceClick,
		handleChatTabsKeyDown,
		isFullscreen,
		orchestratorStarting,
		orderedAuxiliaryTabs,
		reauthErrorInChat,
		reorderAuxiliaryTabs,
		reviewerActive,
		setConfirming,
		shellActive,
		startupReconnecting,
		suppressStopped,
		surfaceRef,
		t,
	} = v;

	return (
		<section
			ref={surfaceRef}
			onKeyDown={handleChatKeyDown}
			onClick={handleChatSurfaceClick}
			aria-label={t("chat.surfaceAria")}
			className="cursor-chat-surface flex h-full min-h-0 min-w-0 w-full flex-col overflow-hidden [font-size:var(--chat-font-size)]"
			data-session-mode={snapshot.mode}
			data-session-role={sessionRole}
			style={
				{
					"--chat-font-size": `${CHAT_FONT_SIZE_DEFAULT}px`,
				} as CSSProperties
			}
		>
			{hideHeader ? null : <ChatHeader
				snapshot={snapshot}
				sessionTitle={sessionTitle}
				sessionRole={sessionRole}
				onOpenReviewerTerminal={onOpenReviewerTerminal}
				onOpenReviewerChat={onOpenReviewerChat}
				reviewerActive={reviewerActive}
				onSelectChat={onSelectChat}
				shellActiveHandleId={shellActive ? shellTarget?.handleId : undefined}
				onSelectShellTerminal={onSelectShellTerminal}
				onCloseShellTerminal={onCloseShellTerminal}
				onRenameShellTerminal={onRenameShellTerminal}
				onTabsKeyDown={handleChatTabsKeyDown}
				headerActions={headerActions}
				session={session}
				onSessionRenamed={onSessionRenamed}
				sessionTabAction={sessionTabAction}
				sessionTabActionWide={sessionTabActionWide}
				tabStripAction={tabStripAction}
				workspaceTabActions={workspaceTabActions}
				workspaceActiveTabKey={workspaceActiveTabKey}
				orderedAuxiliaryTabs={orderedAuxiliaryTabs}
				onReorderAuxiliaryTabs={reorderAuxiliaryTabs}
				inline={isFullscreen || inlineHeader}
			/>}
			<div className="relative flex min-h-0 flex-1 flex-col">
				<AuxiliaryTabPanels props={props} v={v} />
				<div
					aria-hidden={reviewerActive || shellActive}
					aria-label={t("chat.conversationAria")}
					className="flex min-h-0 flex-1 flex-col"
					data-testid="chat-conversation-panel"
					hidden={reviewerActive || shellActive}
					inert={
						reviewerActive || shellActive || agentInputDisabled || controllerTransitioning
							? true
							: undefined
					}
					role="tabpanel"
				>
					{/* Keep sign-in guidance available without repeating the error from chat. */}
					{snapshot.account ? (
						<ReauthBanner key={`${snapshot.sessionId}:${snapshot.conversationId}`} account={snapshot.account} harness={snapshot.harness} sessionId={snapshot.sessionId} reasonInTimeline={reauthErrorInChat} />
					) : null}
					{!draftPersistenceAvailable ? (
						<div role="status" className="flex shrink-0 items-center gap-2 border-b border-border bg-surface px-4 py-2 text-xs text-muted-foreground">
							<TriangleAlert aria-hidden="true" className="size-3.5 shrink-0 text-warning" />
							{t("chat.draft.storageUnavailable")}
						</div>
					) : null}
					<ControllerBanner
						controller={snapshot.controller}
						provisionState={orchestratorStarting ? "provisioning" : session?.provisionState}
						transitioning={controllerTransitioning || agentResuming}
						automaticWakePending={suppressStopped}
						reconnecting={startupReconnecting}
						onResume={newWorkDisabled ? undefined : onResumeAgent}
						resuming={resumingAgent}
						resumeError={resumeError}
						onOpenShell={onOpenShell}
						openingShell={openingShell}
						shellError={shellError}
						finishedLabel={finishedSessionLabel(session)}
					/>
					{snapshot.threadState ? <ThreadStateBanner threadState={snapshot.threadState} /> : null}
					<BackgroundTasksBanner tasks={session?.backgroundTasks} sessionId={snapshot.sessionId} hostId={activeRemoteHostId} />
					<VersionSwitchBanner
						staleBinary={session?.staleBinary}
						hostId={activeRemoteHostId}
						// Only a resume starts a stopped agent, on the new release.
						agentStopped={snapshot.controller.state === "stopped" && !(controllerTransitioning || agentResuming || suppressStopped || resumingAgent || startupReconnecting)}
					/>
					<ConversationBody props={props} v={v} />
				</div>
			</div>

			{/* The copy has to be honest about the cost: this is not "hide these
			    messages", it is "the agent forgets them". Nothing in the worktree is
			    reverted either, and a user who assumed otherwise would be badly
			    surprised, so it is said out loud. */}
			<ConfirmDialog
				open={Boolean(confirming) && !reviewerActive && !shellActive}
				onOpenChange={(open) => {
					if (!open) setConfirming(undefined);
				}}
				title={t("chat.rollback.title")}
				description={
					<>
						<p className="text-xs font-medium text-foreground">
							{t("chat.rollback.summary")}
						</p>
						<p className="mt-1 text-xs text-muted-foreground">
							{t("chat.rollback.detail")}
						</p>
					</>
				}
				confirmLabel="Roll back"
				destructive
				busy={rollbackPending}
				error={rollbackError ?? null}
				onConfirm={() => {
					const turnId = confirming;
					if (!turnId) return;
					setConfirming(undefined);
					void Promise.resolve(onRollback?.(turnId)).catch(() => {});
				}}
			/>
		</section>
	);
}

/** Calm label for a session ended on purpose; undefined while it is live. */
export function finishedSessionLabel(session?: WorkspaceSession): string | undefined {
	if (!session || session.isTerminated !== true) return undefined;
	const merged = session.prs?.find((pr) => pr.state === "merged");
	return merged ? `Finished: PR #${merged.number} merged` : "Session finished";
}
