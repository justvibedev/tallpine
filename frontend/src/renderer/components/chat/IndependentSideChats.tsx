import { Loader2, MessageSquare, Pencil, Plus, X } from "lucide-react";
import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { useSideChats } from "../../hooks/side-chats/useSideChats";
import { MAX_SESSION_DISPLAY_NAME_LEN } from "../../hooks/useSessionRename";
import { useTabScrollEdges } from "../../hooks/useTabScrollEdges";
import { apiErrorMessage } from "../../lib/api-client";
import {
	cancelSideTurn,
	recoverSideChat,
} from "../../lib/side-chats-api";
import { handleTabListKeyDown } from "../../lib/terminal-tabs";
import { cn } from "../../lib/utils";
import type { ChatModel, ChatSkill } from "../../types/conversation";
import { ConfirmDialog } from "../ConfirmDialog";
import { Button } from "../ui/button";
import { ContextMenu, ContextMenuContent, ContextMenuItem, ContextMenuTrigger } from "../ui/context-menu";
import { type ChatAnnotationSummaryItem } from "./ChatAnnotationSummary";
import { ApprovalCard } from "./ChatTimelineItems";
import { Timeline } from "./ChatWorkspace";
import { QueuedMessageDock } from "./QueuedMessageDock";
import { emptySideChatDraft } from "./sideChatDraft";
import { SideComposer } from "./SideComposer";
import { sideConversationActivity } from "./sideConversation";
import { Spinner } from "../ui/spinner";

export function useIndependentSideChats(
	sessionId: string,
	models: ChatModel[],
	skills: ChatSkill[],
	stageAttachments: (items: { mimeType: string; data: string }[]) => Promise<string[]>,
	nativeImages: boolean,
	enabled = true,
	onNavigate?: (annotation: ChatAnnotationSummaryItem) => void,
	_onHide?: () => void,
	canCreate = true,
) {
	const { t } = useTranslation();
	const {
		activeId,
		addReference,
		attachFiles,
		beginRename,
		close,
		closeError,
		closing,
		compact,
		confirmClose,
		conversation,
		create,
		currentActiveId,
		drafts,
		editingText,
		editingTurnId,
		error,
		focusKey,
		inputAnswers,
		interrupt,
		loadOlder,
		loadingOlder,
		navigateAnnotation,
		pending,
		renameCancelled,
		renameId,
		renameText,
		replaceDraft,
		resolveApproval,
		resolveInput,
		retry,
		saveQueuedEdit,
		saveRename,
		send,
		sending,
		sendingRef,
		setActiveId,
		setAttachments,
		setCloseError,
		setClosing,
		setConfirmClose,
		setDrafts,
		setEditingText,
		setEditingTurnId,
		setError,
		setFocusKey,
		setInputAnswers,
		setRenameId,
		setRenameText,
		setSnapshotRefreshKey,
		setVisible,
		sides,
		snapshot,
		timelineDraftScope,
		timelineNavigationRef,
		updateDraft,
		updateSettings,
		visible,
	} = useSideChats({ sessionId, stageAttachments, nativeImages, enabled, onNavigate, canCreate });
	const {
		scrollRef: tabStripRef,
		showLeftFade,
		showRightFade,
		scrollToEnd,
	} = useTabScrollEdges([sides.length, visible]);
	const previousTabCount = useRef(sides.length);
	useEffect(() => {
		if (sides.length > previousTabCount.current) scrollToEnd();
		previousTabCount.current = sides.length;
	}, [sides.length, scrollToEnd]);
	useEffect(() => {
		const strip = tabStripRef.current;
		const selected = strip?.querySelector<HTMLElement>('[aria-selected="true"]');
		if (!strip || !selected) return;
		const bounds = strip.getBoundingClientRect(),
			rect = selected.getBoundingClientRect();
		if (rect.left < bounds.left) strip.scrollLeft += rect.left - bounds.left;
		else if (rect.right > bounds.right) strip.scrollLeft += rect.right - bounds.right;
	}, [currentActiveId, sides.length]);
	const tabBar = enabled ? (
		<header className="browser-panel__tab-row">
			<div className="browser-panel__tab-region">
				<div
					ref={tabStripRef}
					role="tablist"
					aria-label={t("sideChat.tabs")}
					onKeyDown={handleTabListKeyDown}
					className="browser-panel__tab-strip"
				>
					{sides.map((side) => {
						const tab = (
							<div
								key={side.id}
								className={cn("browser-panel__tab", side.id === currentActiveId && "browser-panel__tab--active")}
							>
								{renameId === side.id ? (
									<input
										autoFocus
										aria-label={t("sideChat.renameSideChat")}
										className="min-w-0 w-36 flex-1 rounded-xs border border-accent bg-background px-1 text-control text-foreground outline-none ring-1 ring-accent"
										maxLength={MAX_SESSION_DISPLAY_NAME_LEN}
										onFocus={(event) => event.currentTarget.select()}
										onBlur={() => void saveRename()}
										value={renameText ?? ""}
										onChange={(event) => setRenameText(event.target.value)}
										onKeyDown={(event) => {
											if (event.key === "Enter") {
												event.preventDefault();
												event.currentTarget.blur();
											}
											if (event.key === "Escape") {
												event.preventDefault();
												renameCancelled.current = true;
												setRenameId(undefined);
												setRenameText(undefined);
											}
										}}
									/>
								) : (
									<button
										type="button"
										role="tab"
										aria-selected={side.id === currentActiveId}
										tabIndex={side.id === currentActiveId ? 0 : -1}
										className="browser-panel__tab-select"
										onClick={(event) => {
											if (event.detail > 1) return;
											setActiveId(side.id);
											setFocusKey(0);
										}}
										aria-keyshortcuts="F2"
										onDoubleClick={(event) => {
											event.preventDefault();
											beginRename(side);
										}}
										onKeyDown={(event) => {
											if (event.key === "F2") {
												event.preventDefault();
												beginRename(side);
											}
										}}
										title={side.label}
									>
										<MessageSquare className="browser-panel__tab-icon" aria-hidden="true" />
										<span className="browser-panel__tab-title">{side.label?.trim() || t("sideChat.defaultLabel")}</span>
									</button>
								)}
								<button
									type="button"
									hidden={renameId === side.id}
									className="browser-panel__tab-close"
									aria-label={t("sideChat.closeNamed", { name: side.label })}
									onClick={() => {
										setCloseError(undefined);
										setConfirmClose(side.id);
									}}
								>
									<X className="size-3.5" aria-hidden="true" />
								</button>
							</div>
						);
						if (renameId === side.id) return tab;
						return (
							<ContextMenu key={side.id}>
								<ContextMenuTrigger asChild>{tab}</ContextMenuTrigger>
								<ContextMenuContent className="min-w-44">
									<ContextMenuItem onSelect={() => beginRename(side)}>
										<Pencil aria-hidden="true" />
										{t("sideChat.rename")}
									</ContextMenuItem>
								</ContextMenuContent>
							</ContextMenu>
						);
					})}
				</div>
				{showLeftFade ? (
					<div aria-hidden="true" className="browser-panel__tab-fade browser-panel__tab-fade--left" />
				) : null}
				{showRightFade ? <div aria-hidden="true" className="browser-panel__tab-fade" /> : null}
			</div>
			<Button
				type="button"
				variant="ghost"
				size="icon-sm"
				className="browser-panel__tab-new"
				disabled={!canCreate || pending}
				aria-label={t("sideChat.newSideChat")}
				onClick={() => void create(undefined, true).catch(() => undefined)}
			>
				<Plus className="size-3.5" />
			</Button>
		</header>
	) : null;
	const closeDialog = (
		<ConfirmDialog
			open={Boolean(confirmClose)}
			title={t("sideChat.deleteConfirm")}
			description={t("sideChat.deleteExplanation")}
			confirmLabel={t("sideChat.delete")}
			cancelLabel={t("confirm.cancel")}
			busy={closing}
			error={closeError}
			onOpenChange={(open) => {
				if (!open && !closing) setConfirmClose(undefined);
			}}
			onConfirm={() => {
				if (!confirmClose) return;
				setClosing(true);
				void close(confirmClose)
					.then(() => setConfirmClose((current) => (current === confirmClose ? undefined : current)))
					.catch((cause) => setCloseError(apiErrorMessage(cause)))
					.finally(() => setClosing(false));
			}}
		/>
	);
	// Like the main chat, the transcript shows answered requests and the pending
	// ones wait beside the composer, where the reader acts on them.
	const pendingInteractions = (snapshot?.side.id === activeId ? snapshot?.activities ?? [] : []).filter(
		(activity) =>
			activity.status === "pending" &&
			activity.requestId &&
			(activity.kind === "approval" || ((activity.kind === "user_input" || activity.kind === "input") && activity.input)),
	);
	const renderPendingInteraction = (activity: (typeof pendingInteractions)[number]) => {
		if (!activeId) return null;
		const requestId = activity.requestId!;
		if (activity.kind === "approval")
			return (
				<ApprovalCard
					key={activity.id}
					activity={sideConversationActivity(activity)}
					onDecide={(id, decisionId) => void resolveApproval(activeId, id, decisionId)}
				/>
			);
		const input = activity.input!;
		return (
			<div key={activity.id} className="space-y-2 rounded-lg border border-border px-3 py-2 text-sm">
				<p>{input.message}</p>
				{input.url && /^https?:\/\//i.test(input.url) ? (
					<a className="underline" href={input.url} target="_blank" rel="noreferrer">
						{t("sideChat.openRequestedUrl")}
					</a>
				) : null}
				{Object.keys((input.schema?.properties ?? {}) as Record<string, unknown>).map((key) => (
					<label key={key} className="block">
						{key}
						<input
							className="ml-2 rounded border border-border bg-background p-1"
							value={inputAnswers[requestId]?.[key] ?? ""}
							onChange={(event) =>
								setInputAnswers((current) => ({
									...current,
									[requestId]: { ...current[requestId], [key]: event.target.value },
								}))
							}
						/>
					</label>
				))}
				<div className="flex gap-2">
					<Button type="button" size="sm" onClick={() => void resolveInput(activeId, requestId, "accept")}>
						{t("sideChat.submit")}
					</Button>
					<Button type="button" size="sm" variant="outline" onClick={() => void resolveInput(activeId, requestId, "decline")}>
						{t("sideChat.decline")}
					</Button>
				</div>
			</div>
		);
	};
	const panel =
		enabled && currentActiveId && visible ? (
			<aside
				aria-label={t("sideChat.sideChats")}
				className="cursor-chat-surface relative flex h-full min-h-0 w-full min-w-0 flex-col overflow-hidden bg-background [font-size:14px]"
			>
				{tabBar}
				{closeDialog}
				{snapshot && snapshot.side.id === activeId ? (
					<>
						{snapshot.side.state !== "ready" ? (
							<p className="border-b border-border px-4 py-2 text-xs text-muted-foreground" role="status">
								{snapshot.side.errorMessage ||
									(snapshot.side.state === "recovering" ? t("sideChat.reconnecting") : snapshot.side.state)}
							</p>
						) : null}
						{conversation ? (
							<div className="relative flex min-h-0 flex-1 flex-col">
								{conversation.items.length === 0 ? (
									// The Timeline draws nothing for an empty transcript.
									<div className="mx-auto flex max-w-sm flex-1 flex-col items-center justify-center gap-2 px-4 py-8 text-center text-sm">
										<h3 className="font-medium">{t("sideChat.askASideQuestion")}</h3>
										<p className="text-xs leading-relaxed text-muted-foreground">{t("sideChat.isolationPolicy")}</p>
									</div>
								) : null}
								<Timeline
									key={activeId}
									snapshot={conversation}
									annotationNavigationRef={timelineNavigationRef}
									draftScope={timelineDraftScope!}
									draftPersistenceAvailable={false}
									hasOlder={conversation.hasMoreBefore}
									loadingOlder={loadingOlder}
									onLoadOlder={() => void loadOlder()}
									excerptsEnabled
									onAddToChat={(excerpt) => addReference(activeId, excerpt)}
									onForeignAnnotation={(annotation) => onNavigate?.(annotation)}
									onDecide={(requestId, decisionId) => void resolveApproval(activeId, requestId, decisionId)}
									busy={conversation.controller.state === "busy"}
									retryControl={{ retry: (turnId) => retry(activeId, turnId) }}
								/>
							</div>
						) : null}
						{pendingInteractions.length ? (
							<div className="shrink-0 px-4 pb-2">
								<div className="mx-auto flex w-full max-w-3xl flex-col gap-2">
									{pendingInteractions.map(renderPendingInteraction)}
								</div>
							</div>
						) : null}
						{snapshot.side.state === "failed" && snapshot.side.recreateRequired ? (
							<Button
								type="button"
								variant="outline"
								size="sm"
								onClick={() => void create(undefined, true).catch(() => undefined)}
								disabled={!canCreate || pending}
							>
								{t("sideChat.newSideChat")}
							</Button>
						) : snapshot.side.state === "failed" ? (
							<Button
								type="button"
								variant="outline"
								size="sm"
								onClick={() =>
									void recoverSideChat(sessionId, activeId)
										.then(({ error: recoveryError }) => {
											if (recoveryError) setError(apiErrorMessage(recoveryError));
										})
								}
							>
								{t("sideChat.retryConnection")}
							</Button>
						) : null}
						{drafts[activeId]?.pendingDelivery && !sendingRef.current.has(activeId) ? (
							<div className="px-3 text-xs text-muted-foreground">
								<p>{t("sideChat.deliveryIsUnconfirmedRetryTheOriginalQuestionBeforeSendingAnother")}</p>
								<Button
									type="button"
									size="sm"
									variant="ghost"
									disabled={sending}
									onClick={() => void send(activeId, "")}
								>
									{t("sideChat.retryDelivery")}
								</Button>
							</div>
						) : null}
						<SideComposer
							onNavigate={navigateAnnotation}
							draft={drafts[activeId] ?? emptySideChatDraft()}
							onChange={(text) => updateDraft(activeId, text)}
							onSend={(text) => void send(activeId, text)}
							onCompact={() => void compact(activeId)}
							onInterrupt={() => void interrupt(activeId)}
							onAttach={(files) => void attachFiles(activeId, files)}
							onRemoveFile={(index) => {
								const stagedCount = (drafts[activeId]?.attachments ?? []).length;
								if (index < stagedCount) {
									const path = drafts[activeId]?.attachments[index]?.path;
									setDrafts((current) => ({
										...current,
										[activeId]: {
											...(current[activeId] ?? emptySideChatDraft()),
											attachments: (current[activeId]?.attachments ?? []).filter((_, i) => i !== index),
										},
									}));
									setAttachments((current) => ({
										...current,
										[activeId]: (current[activeId] ?? []).filter((item) => item.path !== path),
									}));
								}
							}}
							files={[]}
							ready={snapshot.side.state === "ready"}
							deliveryPending={Boolean(drafts[activeId]?.pendingDelivery)}
							skills={skills}
							focusKey={focusKey}
							onFocusSide={canCreate ? () => void create(undefined, true).catch(() => undefined) : undefined}
							onRemoveReference={(id) =>
								setDrafts((current) => ({
									...current,
									[activeId]: {
										...(current[activeId] ?? emptySideChatDraft()),
										references: (current[activeId]?.references ?? []).filter((ref) => ref.id !== id),
									},
								}))
							}
							onRemoveAllReferences={() =>
								setDrafts((current) => ({
									...current,
									[activeId]: {
										...(current[activeId] ?? emptySideChatDraft()),
										references: [],
									},
								}))
							}
							running={(snapshot.turns ?? []).some((turn) => turn.state === "running")}
							sending={sending}
							models={models}
							side={snapshot.side}
							onSettings={(model, effort) => void updateSettings(activeId, model, effort)}
							queuedDock={
								(snapshot.turns ?? []).some((turn) => turn.state === "queued") ? (
									<>
										<QueuedMessageDock
											messages={(snapshot.turns ?? [])
												.filter((turn) => turn.state === "queued")
												.map((turn, index) => ({
													turnId: turn.id,
													message: {
														kind: "message",
														id: `queued-${turn.id}`,
														turnId: turn.id,
														sequence: index + 1,
														revision: 1,
														role: "user",
														origin: "human",
														text: turn.text,
														streaming: false,
														createdAt: turn.createdAt,
													},
												}))}
											onCancelQueuedTurn={(turnId) =>
												cancelSideTurn(sessionId, activeId, turnId)
													.then(({ error }) => {
														if (error) setError(apiErrorMessage(error));
													})
											}
											onBeginQueuedEdit={(turnId, text) => {
												setEditingTurnId(turnId);
												setEditingText(text);
											}}
										/>
										{editingTurnId ? (
											<div className="border-x border-border bg-surface px-3 py-2 text-xs">
												<p className="mb-1 text-muted-foreground">{t("sideChat.editingQueuedMessage")}</p>
												<textarea
													aria-label={t("sideChat.editQueuedSideQuestion")}
													className="w-full rounded border border-border bg-background p-2"
													value={editingText}
													onChange={(event) => setEditingText(event.target.value)}
												/>
												<div className="flex gap-2">
													<Button type="button" size="sm" onClick={() => void saveQueuedEdit(activeId)}>
														{t("sideChat.save")}
													</Button>
													<Button type="button" size="sm" variant="ghost" onClick={() => setEditingTurnId(undefined)}>
														{t("sideChat.cancel")}
													</Button>
												</div>
											</div>
										) : null}
									</>
								) : null
							}
						/>
					</>
				) : (
					<div
						className="flex min-h-0 flex-1 flex-col items-center justify-center gap-3 px-6 text-center"
						role="status"
					>
						{error ? (
							<MessageSquare aria-hidden="true" className="size-5 text-muted-foreground" />
						) : (
							<Spinner icon={Loader2}   className="size-5 text-muted-foreground" />
						)}
						<p className="text-sm font-medium">
							{error ? t("sideChat.sideChatIsUnavailable") : t("sideChat.openingSideChat")}
						</p>
						<p className="max-w-xs text-xs leading-relaxed text-muted-foreground">
							{error
								? t(
										"sideChat.yourDraftStaysInThisThreadTryLoadingItAgainOrReturnToTheMainChatWhileTheConnectionRetriesAutomatically",
									)
								: t("sideChat.preparingThisSeparateThreadYouCanKeepWorkingInTheMainChat")}
						</p>
						{error ? (
							<Button
								type="button"
								variant="outline"
								size="sm"
								onClick={() => {
									setError(undefined);
									setSnapshotRefreshKey((key) => key + 1);
								}}
							>
								{t("sideChat.retryLoading")}
							</Button>
						) : null}
					</div>
				)}
				{error ? (
					<div className="shrink-0 border-t border-border bg-surface px-4 py-3 text-xs" role="alert">
						<p className="break-words text-destructive">{error}</p>
					</div>
				) : null}
			</aside>
		) : enabled && currentActiveId ? null : (
			<div className="flex h-full min-h-0 flex-col bg-background">
				{tabBar}
				{closeDialog}
				<div className="flex flex-1 flex-col items-center justify-center gap-3 p-4 text-center text-xs text-muted-foreground">
					<p>
						{enabled ? t("sideChat.noneOpen") : t("sideChat.sideChatsAreAvailableOnlyForLocalDesktopChatSessions")}
					</p>
					{enabled ? <p className="max-w-sm leading-relaxed">{t("sideChat.isolationPolicy")}</p> : null}
					{error ? <p role="alert">{error}</p> : null}
				</div>
			</div>
		);

	return {
		create,
		panel,
		pending,
		error,
		send,
		setQuestionDraft: updateDraft,
		replaceDraft,
		addReference,
		sides,
		activeId: currentActiveId,
		visible,
		show: (sideId?: string) => {
			if (sideId) setActiveId(sideId);
			setVisible(true);
			setFocusKey((key) => key + 1);
		},
		hide: () => setVisible(false),
		close,
	};
}
