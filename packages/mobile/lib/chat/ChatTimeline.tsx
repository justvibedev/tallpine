import { accountSwitchedDetail } from "./accountSwitch";
import { Feather } from "../icons";
import * as Clipboard from "expo-clipboard";
import { memo, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { ActivityIndicator, FlatList, Keyboard, Platform, Pressable, Text, View } from "react-native";
import { usePreventRemove } from "expo-router";
import { haptics } from "../haptics";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { FoldBody, FoldLayout } from "./TurnFoldMotion";
import { ChatMarkdown } from "./ChatMarkdown";
import { RenderFrame } from "./RenderFrame";
import { artifactRenderRef, notifyRenderViewport, readArtifactRef, readRenderRef } from "./renderFrameModel";
import { jumpToLatestColors } from "./chatChrome";
import { errorActivityDuplicatesTurn } from "./providerError";
import { editableMessageText, stagedAttachmentParts } from "./messageAttachments";
import { InlineMessageEditor, type MessageEditDraft } from "./InlineMessageEditor";
import { useMessageEdit } from "./useMessageEdit";
import { MessageContextMenu } from "./MessageContextMenu";
import type { ConversationItem, ConversationMessage, ConversationSnapshot } from "./types";
import { canRollbackTurn, conversationTimelineRenderPlan, editableHumanTurns, sameEditableTurns, partitionTurnItems, readableConversationItems, startupPlacement, streamingCursorMessageId, type ConversationGroup } from "./timelineModel";
import { space } from "../tokens";
import { SentResponseQuotes } from "./SentResponseQuotes";
import { ConversationHarness, LatestWorkerUpdates, NoticeLine, OriginMessageView, WorkerSessionsProvider, WorkerUpdateRows } from "./AutomationMessage";
import { isTaskTitleUpdate, renamedTitle } from "@tallpine/shared/chat/automation/title-update";
import { humanVisibleText, latestWorkerUpdateKeys, piggybackedUpdates, relayedVia } from "./automationMessages";
import { SessionStartupStatus } from "./SessionStartup";
import type { SessionStartupView } from "../session/sessionStartup";
import { activityRuns, deliveryCopy } from "./timeline/format";
import { EmptyConversation, ImageViewerHost, OriginMessage, StagedAttachments, SystemSignal } from "./timeline/attachments";
import { ActivityRow, ActivityRun, CompactionMarker, ErrorActivity } from "./timeline/activityRows";
import { TurnStatusRow, TurnSummary } from "./timeline/turnParts";
import { ApprovalCard, UserInputCard } from "./timeline/requestCards";
import { makeStyles } from "./timeline/styles";

/** A starting session's checklist and where it sits; see ChatSessionScreen. */
export type TimelineStartup = { view: SessionStartupView; visible: boolean; onRetry?(): void; retrying?: boolean };

export const ChatTimeline = memo(function ChatTimeline({
	snapshot,
	loadingOlder,
	onLoadOlder,
	approvalPending,
	inputPending,
	onDecide,
	onResolveInput,
	onRollback,
	onEditMessage,
	onEditingChange,
	jumpToSequence,
	onJumpHandled,
	answeredBelow,
	startup,
}: {
	snapshot: ConversationSnapshot;
	loadingOlder: boolean;
	onLoadOlder(): void;
	approvalPending: boolean;
	inputPending: boolean;
	onDecide(requestId: string, decisionId: string): Promise<void>;
	onResolveInput(requestId: string, action: "accept" | "decline" | "cancel", content?: Record<string, unknown>): Promise<void>;
	onRollback?(turnId: string): Promise<number>;
	/** Branches before an earlier human prompt and resends it edited. */
	onEditMessage?(turnId: string, text: string, clientMessageId: string): Promise<void>;
	/** Whether a prompt is open in its inline editor, so the screen can make room. */
	onEditingChange?(editing: boolean): void;
	jumpToSequence?: number;
	onJumpHandled?(): void;
	/**
	 * The request the composer is currently answering. Its card here collapses to
	 * a record of what was asked, so the same decision never has two live sets of
	 * controls that could disagree.
	 */
	answeredBelow?: number;
	/** Setup progress for a session that is still starting. */
	startup?: TimelineStartup;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const listRef = useRef<FlatList<ConversationGroup>>(null);
	const followsTail = useRef(true);
	const [showJump, setShowJump] = useState(false);
	// Held above the virtualized list so a "Worked for" fold keeps its state when its row scrolls away and back.
	const [openWorked, setOpenWorked] = useState<ReadonlySet<string>>(() => new Set());
	const toggleWorked = useCallback((key: string) => {
		setOpenWorked((current) => {
			const next = new Set(current);
			if (!next.delete(key)) next.add(key);
			return next;
		});
	}, []);
	// Usage is snapshot state, not conversation. Reasoning stays available in the
	// durable record but hidden on mobile: prose and work are the primary surface.
	const openingTurnId = startup?.view.openingTurnId;
	const items = useMemo(() => readableConversationItems(snapshot, openingTurnId), [openingTurnId, snapshot]);
	// Each worker's newest report, read once per item list and kept as the same set
	// until some worker reports again, so settled rows stay idle while a reply streams.
	const latestKeys = useMemo(() => latestWorkerUpdateKeys(items), [items]);
	const latestSignature = useMemo(() => [...latestKeys].sort().join("\n"), [latestKeys]);
	// Keyed on the signature, not the set: a new set with the same keys keeps the old one.
	const latestUpdates = useMemo(() => latestKeys, [latestSignature]);
	const plan = useMemo(() => conversationTimelineRenderPlan(snapshot, items, Platform.OS === "android" ? "android" : "ios"), [items, snapshot.turns]);
	const groups = plan.groups;
	const inverted = plan.inverted;
	// Recomputed per snapshot but kept by identity while its contents are the
	// same, so `extraData` changes (and every row re-renders) only when they do.
	const nextEdits = useMemo(() => onEditMessage ? editableHumanTurns(snapshot) : undefined, [onEditMessage, snapshot]);
	const editsRef = useRef(nextEdits);
	if (!sameEditableTurns(editsRef.current, nextEdits)) editsRef.current = nextEdits;
	const edits = editsRef.current;
	const stopFollowingTail = useCallback(() => { followsTail.current = false; }, []);
	const { editDraft, editing, detached, startEdit, cancelEdit, submitEdit } = useMessageEdit({ items: snapshot.items, onEditMessage, onEditingChange, onStart: stopFollowingTail });
	// Back closes the edit first, as Escape does on the desktop, rather than
	// leaving the conversation with the draft. A JS back listener never hears
	// predictive back on Android, so this guards the screen's removal instead.
	usePreventRemove(editing, cancelEdit);
	const rowState = useMemo(() => ({ openWorked, edits, editDraft }), [editDraft, edits, openWorked]);
	const jumpViewPosition = inverted ? 0.82 : 0.18;
	const scrollToLatest = (animated: boolean) => {
		if (inverted) listRef.current?.scrollToOffset({ offset: 0, animated });
		else listRef.current?.scrollToEnd({ animated });
	};

	useEffect(() => {
		if (jumpToSequence === undefined) return;
		// Match the group that CONTAINS the sequence, not one whose anchor equals
		// it: a group's anchor is its first item, so an activity partway through a
		// turn never matched and the jump silently did nothing.
		const index = groups.findIndex((group) => group.anchor === jumpToSequence || group.items.some((item) => item.sequence === jumpToSequence));
		if (index >= 0) {
			followsTail.current = index === (inverted ? 0 : groups.length - 1);
			setShowJump(!followsTail.current);
			requestAnimationFrame(() => listRef.current?.scrollToIndex({ index, animated: true, viewPosition: jumpViewPosition }));
		}
		onJumpHandled?.();
	}, [groups, inverted, jumpToSequence, jumpViewPosition, onJumpHandled]);

	// The keyboard shrinks the list, so bring the prompt being edited back into view.
	const editingTurnId = editDraft?.turnId;
	const revealEdit = useCallback(() => {
		if (!editingTurnId) return;
		const index = groups.findIndex((group) => group.turnId === editingTurnId);
		if (index < 0) return;
		// A row is the whole turn, so pin its top (the prompt) to the top of the
		// viewport; on an inverted list that is the row's far end.
		requestAnimationFrame(() => listRef.current?.scrollToIndex({ index, animated: true, viewPosition: inverted ? 1 : 0, viewOffset: inverted ? -space.md : space.md }));
	}, [editingTurnId, groups, inverted]);
	useEffect(() => {
		if (!editingTurnId) return;
		revealEdit();
		const subscription = Keyboard.addListener("keyboardDidShow", revealEdit);
		return () => subscription.remove();
		// Only when an edit opens or the keyboard settles: following every refresh
		// would pull the list back while the person scrolls to read.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [editingTurnId]);

	if (plan.kind === "empty" && startup?.visible) {
		// No brief in the timeline (a promptless start): the checklist stands alone.
		return (
			<View style={styles.timelineWrap}>
				<View style={[styles.list, styles.content]}>
					<SessionStartupStatus startup={startup.view} onRetry={startup.onRetry} retrying={startup.retrying} />
				</View>
			</View>
		);
	}
	if (plan.kind === "empty") {
		return (
			<View style={styles.timelineWrap}>
				<View style={styles.emptySurface}>
					<EmptyConversation controller={snapshot.controller.state} />
				</View>
			</View>
		);
	}
	const placement = startupPlacement(groups, openingTurnId, Boolean(startup?.visible));
	// A checklist no loaded turn can host sits at the live edge instead.
	const standaloneStartup = placement === "standalone" && startup ? (
		<SessionStartupStatus startup={startup.view} onRetry={startup.onRetry} retrying={startup.retrying} />
	) : null;
	const olderControl = snapshot.hasMoreBefore ? (
		<Pressable
			accessibilityRole="button"
			disabled={loadingOlder}
			onPress={() => { haptics.tap(); void onLoadOlder(); }}
			style={styles.older}
		>
			{loadingOlder ? <ActivityIndicator size="small" /> : <Feather name="clock" size={12} />}
			<Text style={styles.olderText}>{loadingOlder ? "Loading history…" : "Load earlier messages"}</Text>
		</Pressable>
	) : null;

	return (
		<WorkerSessionsProvider>
			<ConversationHarness.Provider value={snapshot.harness}>
				<LatestWorkerUpdates.Provider value={latestUpdates}>
					<ImageViewerHost>
					<View style={styles.timelineWrap}>
						<FlatList<ConversationGroup>
							ref={listRef}
							data={groups}
							extraData={rowState}
							inverted={plan.inverted}
							keyExtractor={(group) => group.key}
							style={styles.list}
							contentContainerStyle={styles.content}
							keyboardShouldPersistTaps="handled"
							initialNumToRender={4}
							maxToRenderPerBatch={4}
							updateCellsBatchingPeriod={32}
							windowSize={5}
							// Content off screen can resize (a page measured ahead, earlier history
							// loading in); what is on screen stays put on both platforms.
							maintainVisibleContentPosition={{ minIndexForVisible: 0 }}
							onScroll={(event) => {
								const { contentOffset, contentSize, layoutMeasurement } = event.nativeEvent;
								followsTail.current = inverted
									? Math.abs(contentOffset.y) < 120
									: contentSize.height - layoutMeasurement.height - contentOffset.y < 120;
								setShowJump(!followsTail.current);
								notifyRenderViewport();
							}}
							scrollEventThrottle={100}
							onContentSizeChange={() => {
								notifyRenderViewport();
								if (followsTail.current) requestAnimationFrame(() => scrollToLatest(false));
							}}
							onScrollToIndexFailed={({ index, averageItemLength }) => {
								listRef.current?.scrollToOffset({ offset: Math.max(0, index * averageItemLength), animated: true });
								setTimeout(() => listRef.current?.scrollToIndex({ index, animated: true, viewPosition: jumpViewPosition }), 120);
							}}
							ListHeaderComponent={inverted ? standaloneStartup ?? undefined : olderControl ?? undefined}
							ListFooterComponent={inverted ? olderControl ?? undefined : standaloneStartup ?? undefined}
							renderItem={({ item: group }) => <ConversationTurnGroup
								group={group}
								snapshot={snapshot}
								approvalPending={approvalPending}
								inputPending={inputPending}
								onDecide={onDecide}
								onResolveInput={onResolveInput}
								onRollback={onRollback}
								editableTurns={edits?.editable}
								onEditMessage={onEditMessage ? startEdit : undefined}
								editDraft={editDraft}
								onCancelEdit={cancelEdit}
								onSubmitEdit={submitEdit}
								onEditFocus={revealEdit}
								answeredBelow={answeredBelow}
								workedOpen={openWorked.has(group.key)}
								onToggleWorked={toggleWorked}
								startup={placement === "turn" && group.turnId === openingTurnId ? startup : undefined}
							/>}
						/>
						{/* An unresolved edit whose prompt left the conversation keeps its Retry and
						    Abandon controls here, apart from the list, as the desktop does. */}
						{detached && editDraft ? <View style={styles.detachedEditor}>
							<InlineMessageEditor key={editDraft.turnId} draft={editDraft} blocked onCancel={cancelEdit} onEdit={submitEdit} />
						</View> : null}
						{showJump ? <Pressable accessibilityRole="button" accessibilityLabel="Jump to latest message" onPress={() => { haptics.tap(); followsTail.current = true; setShowJump(false); scrollToLatest(true); }} style={styles.jump}><Feather name="arrow-down" size={15} color={jumpToLatestColors(t).foregroundColor} /><Text style={styles.jumpText}>Latest</Text></Pressable> : null}
					</View>
					</ImageViewerHost>
				</LatestWorkerUpdates.Provider>
			</ConversationHarness.Provider>
		</WorkerSessionsProvider>
	);
});

function ConversationTurnGroup({ group, snapshot, approvalPending, inputPending, onDecide, onResolveInput, onRollback, editableTurns, onEditMessage, editDraft, onCancelEdit, onSubmitEdit, onEditFocus, answeredBelow, workedOpen, onToggleWorked, startup }: {
	group: ConversationGroup;
	snapshot: ConversationSnapshot;
	approvalPending: boolean;
	inputPending: boolean;
	onDecide(requestId: string, decisionId: string): Promise<void>;
	onResolveInput(requestId: string, action: "accept" | "decline" | "cancel", content?: Record<string, unknown>): Promise<void>;
	onRollback?(turnId: string): Promise<number>;
	editableTurns?: ReadonlySet<string>;
	onEditMessage?(message: ConversationMessage): void;
	editDraft?: MessageEditDraft;
	onCancelEdit(): void;
	onSubmitEdit(payload: string, body: string): Promise<void>;
	onEditFocus(): void;
	answeredBelow?: number;
	workedOpen: boolean;
	onToggleWorked(key: string): void;
	startup?: TimelineStartup;
}) {
	// A provider failure arrives twice: as an error activity, and again as the
	// turn's errorMessage below it. The turn keeps it — that line carries the
	// outcome and the rollback — so the activity restating it is dropped.
	const turnError = group.turn?.state === "failed" ? group.turn.errorMessage : undefined;
	const items = turnError
		? group.items.filter((item) => !(item.kind === "activity" && errorActivityDuplicatesTurn(item, turnError)))
		: group.items;
	const cursorMessageId = streamingCursorMessageId(items);
	// Only the prompt being edited gets an element, so every other row keeps its memo.
	const editorFor = (item: ConversationItem) => editDraft && item.kind === "message" && item.role === "user" && item.origin === "human" && item.turnId === editDraft.turnId
		? <InlineMessageEditor key={editDraft.turnId} draft={editDraft} blocked={!editableTurns?.has(editDraft.turnId)} onCancel={onCancelEdit} onEdit={onSubmitEdit} onFocus={onEditFocus} />
		: undefined;
	const renderRows = (list: ConversationItem[]) => activityRuns(list).map((row) => row.kind === "activities"
		? <ActivityRun key={row.key} activities={row.items} />
		: <TimelineItem key={row.key} item={row.items[0]} sessionId={snapshot.sessionId} conversationId={snapshot.conversationId} completed={group.turn?.state === "completed" && !group.turn.rolledBack} approvalPending={approvalPending} inputPending={inputPending} onDecide={onDecide} onResolveInput={onResolveInput} namedTitle={isTaskTitleUpdate(row.items[0]) ? renamedTitle(group.items) : undefined} onEdit={row.items[0].turnId && editableTurns?.has(row.items[0].turnId) ? onEditMessage : undefined} editor={editorFor(row.items[0])} answeredBelow={answeredBelow} showCursor={row.items[0].id === cursorMessageId} />);
	if (startup) {
		// The checklist sits exactly where the Working row will, under the brief,
		// so the handoff moves nothing.
		const firstWork = items.findIndex((item) => !(item.kind === "message" && item.role === "user"));
		const leading = firstWork < 0 ? items.length : firstWork;
		// The checklist explains the wait, so the brief drops its "Queued" note.
		const brief = items.slice(0, leading).map((item) => item.kind === "message" && item.delivery === "queued" ? { ...item, delivery: undefined } : item);
		return <View>
			{renderRows(brief)}
			<SessionStartupStatus startup={startup.view} onRetry={startup.onRetry} retrying={startup.retrying} />
			{renderRows(items.slice(leading))}
		</View>;
	}
	const partition = partitionTurnItems(items, group.turn);
	// The prompt's editor grows as it opens and as lines are typed. Android's
	// inverted list misplaces rows whose layout transition runs under that growth,
	// so the turn being edited lays out without the fold motion.
	const editing = Boolean(editDraft && group.turnId === editDraft.turnId);
	const WorkBody = editing ? View : FoldBody;
	const BelowFold = editing ? View : FoldLayout;
	const summary = group.turn ? <TurnSummary turn={group.turn} onRollback={canRollbackTurn(snapshot, group.turn) ? onRollback : undefined} /> : null;
	if (!partition || !group.turn) return <View>{renderRows(items)}{summary}</View>;
	// Running: the prompt, a live Working row, then the work as it streams in.
	// Settled: the same row becomes "Worked for", folding the work away and
	// leaving the notices and the final reply in view.
	return <View>
		{renderRows(partition.prompt)}
		<TurnStatusRow turn={group.turn} foldable={partition.settled && partition.work.length > 0} open={workedOpen} onToggle={() => onToggleWorked(group.key)} />
		{partition.settled ? (workedOpen ? <WorkBody style={{ gap: space.xs, paddingBottom: space.xs }}>{renderRows(partition.work)}</WorkBody> : null) : renderRows(partition.work)}
		<BelowFold>{renderRows(partition.notices)}{renderRows(partition.final)}{summary}</BelowFold>
	</View>;
}

export const TimelineItem = memo(function TimelineItem({
	item,
	sessionId,
	conversationId,
	completed,
	approvalPending,
	inputPending,
	onDecide,
	onResolveInput,
	onEdit,
	editor,
	answeredBelow,
	showCursor = false,
	namedTitle,
}: {
	item: ConversationItem;
	sessionId: string;
	conversationId: string;
	completed: boolean;
	approvalPending: boolean;
	inputPending: boolean;
	onDecide(requestId: string, decisionId: string): Promise<void>;
	onResolveInput(requestId: string, action: "accept" | "decline" | "cancel", content?: Record<string, unknown>): Promise<void>;
	/** Present only on a human prompt the daemon can branch before and resend edited. */
	onEdit?(message: ConversationMessage): void;
	/** Shown in place of the bubble while this prompt is being edited. */
	editor?: ReactNode;
	answeredBelow?: number;
	showCursor?: boolean;
	/** The title a task-naming prompt produced, drawn instead of the prompt. */
	namedTitle?: string;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	if (item.kind === "message") {
		if (item.role === "user" && item.origin === "human") {
			if (editor) return <View style={styles.userRow}>{editor}</View>;
			const delivery = deliveryCopy(item.delivery);
			// Reports the daemon folded in while the orchestrator was busy are drawn as
			// the worker rows they are, above what the user typed.
			const reports = piggybackedUpdates(item);
			const via = relayedVia(item);
			const { body, attachments } = stagedAttachmentParts(via ? via.body : humanVisibleText(item.text));
			// Long-press opens Edit and Copy, so the text is not selectable: a
			// selection would take the same gesture.
			return (
				<>
				{reports.length > 0 ? <WorkerUpdateRows updates={reports} /> : null}
				<View style={styles.userRow}>
					<MessageContextMenu style={styles.userMenu} canEdit={Boolean(onEdit)} onAction={(action) => {
						if (action === "edit") onEdit?.(item);
						else { void Clipboard.setStringAsync(editableMessageText(humanVisibleText(item.text)).body.trim()); haptics.success(); }
					}}>
						<View style={[styles.userBubble, styles.menuBubble, item.content?.some((content) => content.type === "excerpt" && content.text) && styles.quotedBubble]}>
							<SentResponseQuotes content={item.content} />
							{via ? <Text style={styles.delivery}>{`via ${via.name}`}</Text> : null}
							{body ? <Text style={styles.userText}>{body}</Text> : null}
							{attachments.length > 0 ? <StagedAttachments sessionId={sessionId} paths={attachments} spaced={Boolean(body)} /> : null}
							{delivery ? <Text style={styles.delivery}>{delivery}</Text> : null}
						</View>
					</MessageContextMenu>
				</View>
				</>
			);
		}
		if (item.role === "user" && namedTitle) return <NoticeLine label={`New task named ${namedTitle}`} />;
		if (item.role === "user") {
			return <OriginMessageView message={item} fallback={<OriginMessage message={item} />} />;
		}
		return (
			<View style={styles.assistantRow}>
				{item.senderLabel ? <Text style={styles.sender}>{item.senderLabel}</Text> : null}
				<ChatMarkdown text={showCursor ? `${item.text || ""} ▍` : item.text} streaming={item.streaming} continuousSelection selectionSource={completed && !item.streaming ? { conversationId, messageId: item.id, revision: item.revision } : undefined} />
				{!item.streaming && item.text ? <View style={styles.responseActions}>
					<Pressable accessibilityRole="button" accessibilityLabel="Copy response" hitSlop={10} onPress={() => { void Clipboard.setStringAsync(item.text); haptics.success(); }} style={styles.copy}><Feather name="copy" size={12} color={t.textFaint} /></Pressable>
				</View> : null}
			</View>
		);
	}
	if (item.activityKind === "approval") {
		return <ApprovalCard activity={item} busy={approvalPending} onDecide={onDecide} handledBelow={item.sequence === answeredBelow} />;
	}
	if (item.activityKind === "user_input") {
		return <UserInputCard activity={item} busy={inputPending} onResolve={onResolveInput} handledBelow={item.sequence === answeredBelow} />;
	}
	if (item.activityKind === "system" && item.detail?.event === "compaction") {
		return <CompactionMarker activity={item} />;
	}
	if (item.activityKind === "system" && item.detail?.event === "steer") {
		const { body, attachments } = stagedAttachmentParts(item.detail.text || item.summary);
		const steered = <View style={styles.userRow}><View style={[styles.userBubble, styles.steerBubble]}><Text style={styles.steerLabel}>STEERED</Text>{body ? <Text selectable style={styles.userText}>{body}</Text> : null}{attachments.length > 0 ? <StagedAttachments sessionId={sessionId} paths={attachments} spaced={Boolean(body)} /> : null}</View></View>;
		// Another agent's message that arrived mid-turn names its sender, as one between turns does.
		return item.detail.senderSessionId ? <OriginMessageView message={item} fallback={steered} /> : steered;
	}
	// A page the agent published with html_render / tallpine render.
	const render = item.activityKind === "system" ? readRenderRef(item.detail) : undefined;
	if (render) return <RenderFrame render={render} />;
	// An HTML page the agent reported with tallpine report --artifact, in the same frame.
	const artifact = item.activityKind === "system" ? readArtifactRef(item.detail) : undefined;
	const artifactPage = artifact ? artifactRenderRef(sessionId, artifact) : undefined;
	if (artifact && artifactPage) return <RenderFrame render={artifactPage} artifact={{ sessionId, path: artifact.path }} />;
	if (item.detail?.event === "model.rerouted") return <SystemSignal icon="shuffle" title={`Answered by ${item.detail.toModel || "another model"}`} detail={item.detail.fromModel ? `Instead of ${item.detail.fromModel}${item.detail.reason ? ` · ${item.detail.reason}` : ""}` : item.detail.reason} />;
	if (item.detail?.event === "auth.reauth_required") return <SystemSignal icon="key" danger title="The provider asked you to sign in again" detail={item.detail.reason} />;
	if (item.detail?.event === "account.switched") return <SystemSignal icon="repeat" title="Switched account" detail={accountSwitchedDetail(item.detail.accountId)} />;
	if (item.activityKind === "error") return <ErrorActivity activity={item} />;
	return <ActivityRow activity={item} />;
});
