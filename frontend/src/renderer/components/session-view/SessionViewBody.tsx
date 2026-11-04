import { SessionFileExplorer, SessionFileWorkspace } from "./session-view-lazy";
import { Suspense, type CSSProperties } from "react";
import { CenterPane } from "../CenterPane";
import { SessionChatSurface } from "../chat/SessionChatSurface";
import { ReviewerChatSurface } from "../chat/ReviewerChatSurface";
import { SessionInspectorRail, INSPECTOR_SPRING_MS, INSPECTOR_SPRING_EASING } from "../SessionInspectorRail";
import { SessionInspector } from "../SessionInspector";
import { SessionTopbarHost } from "../SessionTopbarPortal";
import { apiErrorMessage } from "../../lib/api-client";
import { cn } from "../../lib/utils";
import { inspectorMaxWidthCss } from "../../lib/inspector-width";
import { LazyChunkBoundary } from "../LazyChunkBoundary";
import type { SessionViewProps, SessionViewState } from "./session-view-types";

export function SessionViewBody({ props, v }: { props: SessionViewProps; v: SessionViewState }) {
	const {
		sessionId,
		hostId,
	} = props;
	const {
		accountSwitchingLabel,
		activeWorkspaceTabKey,
		addShellTerminal,
		artifactRequestsBySession,
		autoResume,
		browserAnnotationQueue,
		browserEntryWidthFloorRef,
		browserOnly,
		browserPoppedOut,
		browserView,
		centerFileRequestsBySession,
		centerFileTabs,
		closeShellTerminalByHandle,
		consumedCenterEditingRequestsRef,
		consumedCenterLineRequestsRef,
		daemonStatus,
		fileAnnotation,
		fileTabs,
		filesKind,
		filesPoppedOut,
		filesSplit,
		handleArtifactRequestConsumed,
		handleInspectorCloseAnimationComplete,
		handleOpenArtifact,
		handleOpenFile,
		handleOpenFiles,
		handleOpenReviewFile,
		handleToggleBrowserPopOut,
		handleToggleFilesPopOut,
		handleToggleInspector,
		hasInspector,
		inspectorPanelVisible,
		inspectorSettledClosed,
		inspectorView,
		interfaceUi,
		isInspectorOpen,
		markCenterFileEditingConsumed,
		markCenterFileLineConsumed,
		newShellTerminalAction,
		openCenterFile,
		openShellTerminal,
		quietResume,
		refreshWorkspaces,
		remoteBase,
		renameShellTerminalByHandle,
		resolvedAuxiliaryTabOrder,
		reviewerChat,
		reviewerChatId,
		reviewerTerminal,
		routedTerminalTarget,
		selectReviewerChat,
		selectReviewerTerminal,
		selectSessionTerminal,
		selectShellTerminal,
		session,
		sessionHeaderActions,
		sessionSplitRef,
		sessionTabActionWide,
		sessionTabActions,
		setAuxiliaryTabOrder,
		setCenterFileDirty,
		setFilesSplit,
		setInspectorOpenForSession,
		setSideChatContainer,
		shellTerminals,
		showChatSurface,
		sideChatContainer,
		sideChatsAvailable,
		sizing,
		switcherHost,
		theme,
		transitionInspectorView,
		uiSessionId,
	} = v;
	return (
			<div
				className="session-split relative flex min-h-0 flex-1 overflow-hidden"
				data-testid="panel-group"
				data-workspace-mode={sizing.mode}
				id="session-workspace"
				ref={sessionSplitRef}
				style={
					{
						"--session-inspector-max-width": inspectorMaxWidthCss(
							sizing.maxPercent,
							sizing.chatMinWidth,
						),
						"--session-inspector-motion-duration": `${INSPECTOR_SPRING_MS}ms`,
						"--session-inspector-motion-easing": INSPECTOR_SPRING_EASING,
					} as CSSProperties
				}
			>
				<div
					className="relative flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden"
					data-panel=""
					id="terminal"
				>
					<div className="relative flex h-full min-h-0 flex-col">
						<SessionTopbarHost
							className="relative z-chrome flex h-inspector-tabs w-full shrink-0 overflow-hidden"
							data-testid="session-topbar-host"
						/>
						<div className="relative min-h-0 flex-1">
							{/* The committed mode owns the agent surface. Auxiliary shell and
							    reviewer targets remain terminal surfaces in either mode. */}
							<div
								className={cn("h-full min-h-0", fileTabs.activePath && "invisible pointer-events-none")}
								inert={fileTabs.activePath ? true : undefined}
							>
							{showChatSurface && session ? (
								<>
								<SessionChatSurface
									sideChatContainer={sideChatContainer}
									key={uiSessionId}
									assetBaseUrl={remoteBase}
									hostId={hostId}
									session={session}
									reviewerTerminal={reviewerTerminal}
									reviewerChat={reviewerChat}
									reviewerChatSelected={Boolean(reviewerChatId)}
									onOpenReviewerTerminal={selectReviewerTerminal}
									onOpenReviewerChat={(target) => selectReviewerChat(target.reviewId)}
									onSessionRenamed={refreshWorkspaces}
									reviewerTarget={
										routedTerminalTarget.kind === "reviewer" ? routedTerminalTarget : undefined
									}
									onSelectChat={selectSessionTerminal}
									shellTerminals={shellTerminals}
									shellTarget={
										routedTerminalTarget.kind === "shell" ? routedTerminalTarget : undefined
									}
									onSelectShellTerminal={selectShellTerminal}
									onCloseShellTerminal={closeShellTerminalByHandle}
									onRenameShellTerminal={renameShellTerminalByHandle}
									daemonReady={hostId ? Boolean(remoteBase) : daemonStatus.state === "ready"}
									theme={theme}
									headerActions={sessionHeaderActions}
									sessionTabAction={sessionTabActions}
									sessionTabActionWide={sessionTabActionWide}
									tabStripAction={newShellTerminalAction}
									workspaceTabs={centerFileTabs}
									workspaceActiveTabKey={activeWorkspaceTabKey}
									workspaceFileActive={Boolean(fileTabs.activePath)}
									auxiliaryTabOrder={resolvedAuxiliaryTabOrder}
									onAuxiliaryTabOrderChange={setAuxiliaryTabOrder}
									controllerResumeError={!hostId && autoResume.variables === sessionId && autoResume.isError
										? apiErrorMessage(autoResume.error) : undefined}
									controllerTransitioning={interfaceUi.controllerTransitioning}
									accountMoving={Boolean(interfaceUi.accountMoveTarget)}
									arriving={interfaceUi.optimisticTarget === "chat"}
									agentResuming={quietResume}
									newWorkDisabled={interfaceUi.newWorkDisabled}
									onConversationWorkChange={interfaceUi.onConversationWorkChange}
									interfaceSwitch={{ control: interfaceUi.switchControl, errorNotice: interfaceUi.switchErrorNotice }}
									onOpenShell={addShellTerminal}
									openingShell={openShellTerminal.isPending}
									shellError={
										openShellTerminal.error ? apiErrorMessage(openShellTerminal.error) : undefined
									}
									onOpenFiles={browserOnly ? undefined : handleOpenFiles}
									onOpenFile={handleOpenFile}
									onOpenLinkInBrowser={browserView.openLink}
								/>
								{reviewerChatId ? (
									<div className="absolute inset-0">
										<ReviewerChatSurface hideHeader hostId={hostId} workerSessionId={sessionId} reviewId={reviewerChatId} />
									</div>
								) : null}
								</>
							) : (
								<CenterPane
									hostId={hostId}
									agentInputDisabled={interfaceUi.agentInputDisabled}
									terminalBooting={interfaceUi.optimisticTarget === "tui"}
									terminalSwitchingLabel={accountSwitchingLabel}
									daemonReady={hostId ? Boolean(remoteBase) : daemonStatus.state === "ready"}
									onCloseShellTerminal={closeShellTerminalByHandle}
									onRenameShellTerminal={renameShellTerminalByHandle}
									onSelectSessionTerminal={selectSessionTerminal}
									onSelectReviewerTerminal={selectReviewerTerminal}
									onSelectReviewerChat={(target) => selectReviewerChat(target.reviewId)}
									onSelectShellTerminal={selectShellTerminal}
									reviewerTerminal={reviewerTerminal}
									reviewerChat={reviewerChat}
									reviewerChatSelected={Boolean(reviewerChatId)}
									reviewerChatContent={reviewerChatId ? <ReviewerChatSurface hideHeader hostId={hostId} workerSessionId={sessionId} reviewId={reviewerChatId} /> : undefined}
									session={session}
									shellTerminals={shellTerminals}
									terminalTarget={routedTerminalTarget}
									theme={theme}
									topbarActions={sessionHeaderActions}
									sessionTabAction={sessionTabActions}
									sessionTabActionWide={sessionTabActionWide}
									tabStripAction={newShellTerminalAction}
									workspaceTabs={centerFileTabs}
									workspaceActiveTabKey={activeWorkspaceTabKey}
									auxiliaryTabOrder={resolvedAuxiliaryTabOrder}
									onAuxiliaryTabOrderChange={setAuxiliaryTabOrder}
								/>
							)}
							</div>
							{fileTabs.openPaths.length > 0 ? (
								<div className={cn("absolute inset-0",!fileTabs.activePath && "invisible pointer-events-none")} inert={!fileTabs.activePath ? true:undefined}>
										<>
											{fileTabs.openPaths.map((fileKey) => {
												const request = centerFileRequestsBySession[uiSessionId]?.[fileKey];
												const token = request ? `${uiSessionId}:${fileKey}:${request.key}` : "";
												return (
													<div
														key={fileKey}
														className={cn("absolute inset-0", fileTabs.activePath !== fileKey && "invisible pointer-events-none")}
														inert={fileTabs.activePath !== fileKey ? true : undefined}
													>
														<LazyChunkBoundary>
														<Suspense fallback={null}>
														<SessionFileWorkspace
															active={fileTabs.activePath === fileKey && !filesPoppedOut}
															fileKey={fileKey}
															hostId={hostId}
															source={request?.source}
															previousPath={request?.previousPath}
															annotation={fileAnnotation}
															commitSha={request?.commitSha}
															initialEditing={Boolean(request?.editing && !consumedCenterEditingRequestsRef.current.has(token))}
															initialLine={request?.line != null && !consumedCenterLineRequestsRef.current.has(token) ? request.line : undefined}
															initialMode={request?.mode ?? "file"}
															initialRequestKey={request?.key ?? 0}
															onDirtyChange={setCenterFileDirty}
															onInitialEditingConsumed={markCenterFileEditingConsumed}
															onInitialLineConsumed={markCenterFileLineConsumed}
															path={request?.path ?? fileKey}
															sessionId={sessionId}
															split={filesSplit}
															scope={request?.scope}
														/>
														</Suspense>
														</LazyChunkBoundary>
													</div>
												);
											})}
										</>
								</div>
							) : null}
							{interfaceUi.notice}
						</div>
					</div>
				</div>
				{hasInspector ? (
					<SessionInspectorRail
						sessionKey={uiSessionId}
						isOpen={isInspectorOpen}
						onCloseAnimationComplete={handleInspectorCloseAnimationComplete}
						onExpand={() => setInspectorOpenForSession(uiSessionId, true)}
						restoreMinWidth={
							sizing.mode === "browser" ? (browserEntryWidthFloorRef.current ?? undefined) : undefined
						}
						sizing={sizing}
						settledClosed={!isInspectorOpen && inspectorSettledClosed}
						splitRef={sessionSplitRef}
					>
						<SessionInspector
							sideChatView={sideChatsAvailable ? <div ref={setSideChatContainer} className="h-full min-h-0 flex-1" /> : undefined}
							hostId={hostId}
							onTogglePanel={handleToggleInspector}
							panelOpen={isInspectorOpen}
							switcherHost={switcherHost}
							browserOnly={browserOnly}
							browserAnnotationQueue={inspectorView === "browser" ? browserAnnotationQueue : undefined}
							browserPoppedOut={browserPoppedOut}
							filesView={
								inspectorView === "files" && session ? (
									<LazyChunkBoundary>
									<Suspense fallback={null}>
										<SessionFileExplorer
											hostId={hostId}
											onOpenFile={openCenterFile}
											onSplitChange={setFilesSplit}
											onToggleMaximized={handleToggleFilesPopOut}
											checkedOutBranch={session.branch}
											projectKind={filesKind}
											sessionId={session.id}
											split={filesSplit}
										/>
									</Suspense>
									</LazyChunkBoundary>
								) : null
							}
							isInspectorVisible={inspectorPanelVisible}
							artifactRequest={artifactRequestsBySession[uiSessionId] ?? null}
							onArtifactRequestConsumed={handleArtifactRequestConsumed}
							onOpenArtifact={browserOnly ? undefined : handleOpenArtifact}
							onOpenFiles={browserOnly ? undefined : handleOpenFiles}
							onOpenReviewFile={handleOpenReviewFile}
							onOpenReviewerTerminal={selectReviewerTerminal}
							onOpenReviewerChat={selectReviewerChat}
							onWorkerMessageSent={showChatSurface || reviewerChatId ? selectSessionTerminal : undefined}
							onToggleBrowserPopOut={handleToggleBrowserPopOut}
							onViewChange={(next) => transitionInspectorView(next, true)}
							view={inspectorView}
							browserView={hostId || inspectorView === "browser" ? browserView : undefined}
							session={session}
						/>
					</SessionInspectorRail>
				) : null}
			</div>
	);
}
