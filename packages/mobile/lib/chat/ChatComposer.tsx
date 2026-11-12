import { Feather } from "../icons";
import AsyncStorage from "@react-native-async-storage/async-storage";
import { machineIdentity, type ServerConfig } from "../config";
import { useRouter } from "expo-router";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ActivityIndicator, Image, Pressable, ScrollView, StyleSheet, Text, TextInput, View, type TextInputInstance } from "react-native";
import Animated from "react-native-reanimated";
import { useReanimatedKeyboardAnimation } from "react-native-keyboard-controller";
import { haptics } from "../haptics";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { textSelectionColors } from "../textSelection";
import { fontScaleCap, iconSize, press } from "../tokens";
import { MicKey } from "../voice/MicKey";
import { useVoiceInput } from "../voice/useVoiceInput";
import { activeTurn, type ChatConfigOption, type ChatExcerpt, type ChatImage, type ChatModel, type ChatResource, type ChatSkill, type ConversationSnapshot, type TurnSettings } from "./types";
import { composerSuggestionKey, findComposerSuggestion, shouldAutoOpenComposerPicker } from "./composerSuggestions";
import { ChatAttachmentMenu } from "./ChatAttachmentMenu";
import { ComposerGlass } from "./composer-glass";
import { COMPOSER_FIELD_HEIGHT, COMPOSER_FIELD_MAX_HEIGHT, COMPOSER_RADIUS } from "./composerStyles";
import { ChatTurnSettingsControl } from "./ChatTurnSettingsControl";
import { compactCount } from "../files/model";
import { composerDeliveryPresentation, composerPrimaryAction } from "./composerDeliveryModel";
import { contextMeterModel } from "./contextMeter";
import { RequestCard } from "./RequestCard";
import type { RequestDockModel } from "./requestDockModel";
import { createRequestGate } from "./requestGate";
import { queuedConversationMessages } from "./timelineModel";
import { userFacingError } from "../connectionError";
import type { DashboardPR, SessionPRSummary } from "../api";
import { PRReviewPrompt } from "./PRReviewPrompt";
import { decodeResponseDraft, encodeResponseDraft, responseReplyText, responseExcerpts, excerptDraftText, isChatExcerpt, type ResponseQuote, type ResponseQuoteRequest } from "./responseQuotes";
import { afterDraftWrites } from "./pendingSend";
import { Attachment } from "./composerAttachments";
import { removeComposerAttachment } from "./composerImages";
import { changesAccessibilityLabel, makeStyles } from "./chatComposerStyles";
import { useComposerActions } from "./useComposerActions";
import { useImagePaste } from "./useImagePaste";
import { useReviewCardMotion } from "./useReviewCardMotion";

export function ChatComposer({
	quoteRequest,
	onQuoteHandled,
	reviewPR,
	reviewPRSummary,
	reviewPRCollapsed,
	onCollapseReviewPR,
	onOpenReview,
	sessionId,
	config,
	changes,
	snapshot,
	skills,
	filePaths,
	filePathsTruncated,
	onLoadSkills,
	onLoadFiles,
	configOptions,
	models,
	steerUnavailable,
	pending,
	interrupting,
	disabled,
	onSend,
	onAcknowledgeSend,
	completedRetry,
	onSteer,
	onPromoteQueuedTurn,
	onCancelQueuedTurn,
	onInterrupt,
	onOpenSettings,
	onSettings,
	onConfigOption,
	restingInset,
	dockRiseOverride,
	quotaActive,
	request,
	requestDismissed,
	onRequestDecide,
	onRequestResolveInput,
	onShowRequest,
	onDismissRequest,
	onRestoreRequest,
	openingTurnId,
	startingAgentName,
	orchestrator,
}: {
	quoteRequest?: ResponseQuoteRequest;
	onQuoteHandled?(id: string): void;
	reviewPR?: DashboardPR;
	reviewPRSummary?: SessionPRSummary;
	reviewPRCollapsed?: boolean;
	onCollapseReviewPR?(): void;
	onOpenReview?(): void;
	sessionId: string;
	config: ServerConfig | null;
	/** What this session changed, from git. Present only when it changed something. */
	changes?: { files: number; additions: number; deletions: number; onOpen(): void };
	snapshot: ConversationSnapshot;
	skills: ChatSkill[];
	filePaths: string[];
	filePathsTruncated?: boolean;
	onLoadSkills(): Promise<ChatSkill[]>;
	onLoadFiles(): Promise<{ paths: string[]; truncated: boolean }>;
	configOptions?: ChatConfigOption[];
	models: ChatModel[];
	steerUnavailable?: boolean;
	pending?: boolean;
	interrupting?: boolean;
	disabled?: boolean;
	onSend(text: string, attachments?: ChatImage[], resources?: ChatResource[], excerpts?: ChatExcerpt[]): Promise<string>;
	onAcknowledgeSend(id: string): Promise<void>;
	completedRetry?: { id: string; draftText: string };
	onSteer(text: string): Promise<string>;
	onPromoteQueuedTurn(turnId: string): Promise<void>;
	onCancelQueuedTurn(turnId: string): Promise<void>;
	onInterrupt(): void;
	onOpenSettings(): void;
	onSettings(settings: TurnSettings): Promise<void>;
	onConfigOption(id: string, value: { value: string } | { enabled: boolean }): Promise<ChatConfigOption[]>;
	/**
	 * What the dock owes with the keyboard down — the home-indicator inset. The
	 * dock keeps it at all times and closes the difference to the keyboard by
	 * itself; see `dockRise` below.
	 */
	restingInset: number;
	/**
	 * Pins the dock's rise (0 resting, 1 against the keyboard) when the keyboard's
	 * own progress missed a transition and is stuck at the old end. Leave it
	 * unset to ride the progress. See `chatDockRiseOverride`.
	 */
	dockRiseOverride?: number;
	/** The account-quota banner is up, so the context meter stands down. */
	quotaActive?: boolean;
	/** A pending request, which takes the composer's place until it is answered. */
	request?: RequestDockModel | null;
	/** The user pushed the request aside to type instead. */
	requestDismissed?: boolean;
	onRequestDecide(requestId: string, decisionId: string): Promise<void>;
	onRequestResolveInput(requestId: string, action: "accept" | "decline" | "cancel", content?: Record<string, unknown>): Promise<void>;
	onShowRequest(sequence: number): void;
	onDismissRequest(): void;
	onRestoreRequest(): void;
	/** A starting session's brief, which the timeline shows as sent. */
	openingTurnId?: string;
	/** Set while the session starts, for the placeholder. */
	startingAgentName?: string;
	/** An orchestrator chat: the placeholder names it, not a worker. */
	orchestrator?: boolean;
}) {
	const t = useTheme();
	const router = useRouter();
	const styles = useThemedStyles(makeStyles);
	// The keyboard's own progress, 0 closed to 1 open. This is the same value the
	// keyboard is animating with, so the dock moves in lockstep with it.
	const keyboard = useReanimatedKeyboardAnimation();
	const { reviewPromptAvailable, reviewCardHeaderHeight, reviewCardHeaderMeasured, reviewCardHeaderStyle, collapseReviewCard, reviewCardPan, reviewCardDragStyle, reviewCardGlassStyle, reviewComposerSurfaceStyle, reviewComposerGlassStyle, dockRise } = useReviewCardMotion({ reviewPR, onOpenReview, reviewPRCollapsed, onCollapseReviewPR, t, restingInset, dockRiseOverride, keyboard });
	const [text, setText] = useState("");
	const [quotes, setQuotes] = useState<ResponseQuote[]>([]);
	const inputRef = useRef<TextInputInstance>(null);
	const consumedQuote = useRef<string | undefined>(undefined);
	const [draftLoaded, setDraftLoaded] = useState(false);
	const [cursor, setCursor] = useState(0);
	const [fieldHeight, setFieldHeight] = useState(COMPOSER_FIELD_HEIGHT);
	const [attachments, setAttachments] = useState<Attachment[]>([]);
	const [localError, setLocalError] = useState<string>();
	const [submitting, setSubmitting] = useState(false);
	const [promotingQueuedTurnId, setPromotingQueuedTurnId] = useState<string>();
	const [cancellingQueuedTurnId, setCancellingQueuedTurnId] = useState<string>();
	const [hiddenQueuedTurnIds, setHiddenQueuedTurnIds] = useState<Set<string>>(() => new Set());
	const active = Boolean(activeTurn(snapshot));
	const queuedMessages = useMemo(() => queuedConversationMessages(snapshot, openingTurnId), [openingTurnId, snapshot]);
	const visibleQueuedMessages = useMemo(() => queuedMessages.filter((entry) => !hiddenQueuedTurnIds.has(entry.turnId)), [hiddenQueuedTurnIds, queuedMessages]);
	// Absent below 70%: a permanent token gauge is chrome nobody reads.
	const contextMeter = contextMeterModel(snapshot.usage, Boolean(quotaActive));
	// A blocking question is the next thing to do, so it takes the input's place
	// rather than pointing at a card somewhere up the timeline.
	const requestCard = request && !requestDismissed ? (
		<RequestCard
			model={request}
			onDecide={onRequestDecide}
			onResolveInput={onRequestResolveInput}
			onShow={onShowRequest}
			onDismiss={onDismissRequest}
		/>
	) : null;
	const canSteer = snapshot.capabilities?.includes("steer") && !steerUnavailable && active;
	const canEmbedFiles = snapshot.capabilities?.includes("embedded_context");
	const hasDraft = Boolean(text.trim() || quotes.length);
	const primaryAction = composerPrimaryAction({ active, hasDraft, hasAttachments: attachments.length > 0 });
	const steerEligible = Boolean(canSteer && hasDraft && attachments.length === 0 && quotes.length === 0);
	const deliveryPresentation = composerDeliveryPresentation({ orchestrator, active, canSteer: Boolean(canSteer && quotes.length === 0), hasDraft, hasAttachments: attachments.length > 0, hasQueued: visibleQueuedMessages.length > 0 });
	const stopped = snapshot.controller.state === "stopped";
	const draftKey = config ? `ao.chat.draft.${machineIdentity(config)}.${sessionId}` : null;
	const draftTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
	const openingSuggestion = useRef<string | undefined>(undefined);
	const pickerGate = useRef(createRequestGate()).current;
	const latestQuotes = useRef(quotes);
	latestQuotes.current = quotes;
	const latestText = useRef(text);
	const latestCursor = useRef(cursor);
	latestText.current = text;
	latestCursor.current = cursor;
	useEffect(() => {
		if (!quoteRequest || consumedQuote.current === quoteRequest.id) return;
		consumedQuote.current = quoteRequest.id;
		const next = [...latestQuotes.current, { id: quoteRequest.id, text: quoteRequest.text, source: quoteRequest.source }];
		latestQuotes.current = next;
		setQuotes(next);
		onQuoteHandled?.(quoteRequest.id);
		haptics.select();
		requestAnimationFrame(() => inputRef.current?.focus());
	}, [quoteRequest, onQuoteHandled]);
	useEffect(() => () => pickerGate.invalidate(), [pickerGate]);
	useEffect(() => {
		const queuedIds = new Set(queuedMessages.map((entry) => entry.turnId));
		setHiddenQueuedTurnIds((current) => {
			const next = new Set([...current].filter((id) => queuedIds.has(id)));
			return next.size === current.size ? current : next;
		});
	}, [queuedMessages]);

	useEffect(() => {
		if (!draftKey) return;
		let mounted = true;
		void AsyncStorage.getItem(draftKey)
			.then((value) => {
				if (!mounted) return;
				const draft = decodeResponseDraft(value);
				setText((current) => current || draft.text);
				setQuotes((current) => [...draft.quotes.filter((quote) => !current.some((item) => item.id === quote.id)), ...current]);
			})
			.catch(() => {})
			.finally(() => { if (mounted) setDraftLoaded(true); });
		return () => { mounted = false; };
	}, [draftKey]);
	useEffect(() => {
		if (!draftKey || !draftLoaded) return;
		draftTimer.current = setTimeout(() => {
			const value = encodeResponseDraft(text, quotes);
			void afterDraftWrites(draftKey, () => value ? AsyncStorage.setItem(draftKey, value) : AsyncStorage.removeItem(draftKey)).catch(() => {});
		}, 250);
		return () => { if (draftTimer.current) clearTimeout(draftTimer.current); };
	}, [draftKey, draftLoaded, text, quotes]);
	useEffect(() => {
		if (!completedRetry) return;
		if (draftTimer.current) clearTimeout(draftTimer.current);
		const current = latestText.current;
		const currentQuotes = latestQuotes.current;
		const matches = currentQuotes.length <= 8 && currentQuotes.every((quote) => isChatExcerpt({ ...quote.source, text: quote.text })) && excerptDraftText(responseReplyText(current, currentQuotes), responseExcerpts(currentQuotes)) === completedRetry.draftText;
		if (matches) {
			latestText.current = "";
			setText("");
			latestQuotes.current = [];
			setQuotes([]);
			setAttachments([]);
		}
		void (async () => {
			if (draftKey) {
				await afterDraftWrites(draftKey, () => matches ? AsyncStorage.removeItem(draftKey) : AsyncStorage.setItem(draftKey, encodeResponseDraft(current, currentQuotes)));
			}
			await onAcknowledgeSend(completedRetry.id);
		})().catch((cause) => setLocalError(userFacingError(cause)));
	}, [completedRetry, draftKey, onAcknowledgeSend]);

	const voice = useVoiceInput({ onTranscript: useCallback((spoken: string) => setText((old) => old ? `${old} ${spoken}` : spoken), []) });
	const { submit, addImage, pasteImages, addFile, openPicker } = useComposerActions({ submitting, pending, disabled, draftKey, draftLoaded, text, quotes, attachments, setLocalError, setSubmitting, draftTimer, steerEligible, onSteer, onSend, latestText, latestQuotes, onAcknowledgeSend, setText, setFieldHeight, setQuotes, setAttachments, pickerGate, onLoadSkills, skills, onLoadFiles, filePaths, filePathsTruncated, latestCursor, router, setCursor });
	const pasteLayout = useImagePaste(stopped || submitting ? null : pasteImages);
	useEffect(() => {
		const suggestion = findComposerSuggestion(text, cursor);
		if (!suggestion) {
			pickerGate.invalidate();
			openingSuggestion.current = undefined;
			return;
		}
		if (!shouldAutoOpenComposerPicker(suggestion)) {
			pickerGate.invalidate();
			openingSuggestion.current = undefined;
			return;
		}
		const key = composerSuggestionKey(suggestion);
		if (openingSuggestion.current === key) return;
		openingSuggestion.current = key;
		void openPicker(suggestion.kind, suggestion);
	}, [cursor, openPicker, pickerGate, text]);
	const activeReviewPR = reviewPromptAvailable ? reviewPR : undefined;
	const showReviewPrompt = Boolean(activeReviewPR);
	const composerContents = (
		<>
				{activeReviewPR ? <Animated.View style={[styles.reviewCardHeader, reviewCardHeaderStyle]} onLayout={(event) => { if (!reviewCardHeaderMeasured.value && event.nativeEvent.layout.height > 0) { reviewCardHeaderHeight.value = event.nativeEvent.layout.height; reviewCardHeaderMeasured.value = true; } }}>
					<PRReviewPrompt pr={activeReviewPR} summary={reviewPRSummary} onPress={onOpenReview ?? (() => {})} onCollapse={collapseReviewCard} />
					<View style={styles.reviewDivider} />
				</Animated.View> : null}
			{voice.state === "starting" || voice.state === "recording" ? <View style={styles.voice}><Feather name="mic" size={12} color={t.red} /><Text style={styles.voiceText}>{voice.partial || (voice.state === "starting" ? "Keep holding…" : "Listening…")}</Text></View> : null}
			{attachments.length ? <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={styles.attachments}>{attachments.map((item) => <View key={item.id} style={styles.attachment}>{item.kind === "image" ? <Image accessibilityIgnoresInvertColors source={{ uri: `data:${item.image.mimeType};base64,${item.image.data}` }} style={styles.attachmentImage} /> : <Feather name="file-text" size={12} color={t.accent} />}<Text numberOfLines={1} style={styles.attachmentName}>{item.name}</Text><Pressable hitSlop={7} accessibilityLabel={`Remove ${item.name}`} onPress={() => { haptics.tap(); const removed = removeComposerAttachment(latestText.current, attachments, item.id); latestText.current = removed.text; setText(removed.text); setAttachments(removed.items); }}><Feather name="x" size={12} color={t.textTertiary} /></Pressable></View>)}</ScrollView> : null}
			{/* Composer-local only. Conversation and action failures are banners above
			    the timeline; echoing them here showed one failure twice. */}
			{localError || voice.error ? <Text accessibilityRole="alert" style={styles.error}>{localError || voice.error}</Text> : null}
			<View style={styles.metaRow}>
				<View style={styles.settingsSlot}>
				<ChatTurnSettingsControl snapshot={snapshot} models={models} options={configOptions ?? []} disabled={disabled || stopped || pending || submitting} onSettings={onSettings} onOption={onConfigOption} onOpenFallback={onOpenSettings} />
				</View>
				{/* The changes pill takes the queue note's place: the placeholder already
				    says a message waits for the turn. Attachments keep their note, and
				    the steer action stays either way. */}
				{changes && !(deliveryPresentation.showQueueNote && attachments.length) ? <View style={styles.deliveryNote}>
					<Pressable
						accessibilityRole="button"
						accessibilityLabel={changesAccessibilityLabel(changes)}
						accessibilityHint="Opens the changed files"
						onPress={() => { haptics.tap(); changes.onOpen(); }}
						style={({ pressed }) => [styles.changesPill, pressed && { opacity: press.opacity }]}
					>
						<Feather name="file-diff" size={12} color={t.textSecondary} />
						<Text numberOfLines={1} maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.changesPillText}>{changes.files}</Text>
						{/* The model picker keeps its room: line counts drop out when the context meter needs the row. */}
						{!contextMeter && changes.additions ? <Text numberOfLines={1} maxFontSizeMultiplier={fontScaleCap.chrome} style={[styles.changesPillCount, { color: t.green }]}>+{compactCount(changes.additions)}</Text> : null}
						{!contextMeter && changes.deletions ? <Text numberOfLines={1} maxFontSizeMultiplier={fontScaleCap.chrome} style={[styles.changesPillCount, { color: t.red }]}>−{compactCount(changes.deletions)}</Text> : null}
					</Pressable>
					{deliveryPresentation.showSteerAction ? <Pressable accessibilityRole="button" accessibilityLabel="Steer this turn now" disabled={disabled || submitting || pending} onPress={() => { haptics.tap(); void submit("steer"); }} style={({ pressed }) => [styles.steerAction, pressed && { opacity: 0.7 }]}><Feather name="corner-up-right" size={15} color={t.accent} /></Pressable> : null}
				</View> : deliveryPresentation.showQueueNote ? <View style={styles.deliveryNote}>
					<Feather name="clock" size={12} color={t.textTertiary} />
					<Text numberOfLines={1} style={styles.deliveryNoteText}>{attachments.length ? "Attachments next" : "Sent after this"}</Text>
					{deliveryPresentation.showSteerAction ? <Pressable accessibilityRole="button" accessibilityLabel="Steer this turn now" disabled={disabled || submitting || pending} onPress={() => { haptics.tap(); void submit("steer"); }} style={({ pressed }) => [styles.steerAction, pressed && { opacity: 0.7 }]}><Feather name="corner-up-right" size={15} color={t.accent} /></Pressable> : null}
				</View> : null}
				{contextMeter ? <View accessibilityRole="progressbar" accessibilityLabel={`Context window ${contextMeter.percent}% used`} style={styles.contextMeter}>
					<View style={styles.contextTrack}>
						<View style={[styles.contextFill, { width: `${contextMeter.fillPercent}%`, backgroundColor: contextMeter.severity === "critical" ? t.red : t.amber }]} />
					</View>
					<Text numberOfLines={1} maxFontSizeMultiplier={fontScaleCap.chrome} style={[styles.contextText, { color: contextMeter.severity === "critical" ? t.red : t.amber }]}>{contextMeter.label}</Text>
				</View> : null}
			</View>
			{visibleQueuedMessages.length ? <View accessibilityRole="list" style={styles.queueDock}>
				{visibleQueuedMessages.map((entry, index) => {
					const promoting = promotingQueuedTurnId === entry.turnId;
					const cancelling = cancellingQueuedTurnId === entry.turnId;
					const queueActionPending = Boolean(promotingQueuedTurnId || cancellingQueuedTurnId);
					return <View key={entry.turnId} style={[styles.queueRow, index > 0 && styles.queueRowDivider]}>
						<Feather name="corner-down-right" size={15} color={t.textTertiary} />
						<Text numberOfLines={1} style={styles.queueText}>{entry.message.text}</Text>
						{canSteer ? <Pressable
							accessibilityRole="button"
							accessibilityLabel={`Steer queued message: ${entry.message.text}`}
							accessibilityState={{ busy: promoting, disabled: queueActionPending }}
							disabled={queueActionPending}
							onPress={() => {
								haptics.tap();
								setPromotingQueuedTurnId(entry.turnId);
								setHiddenQueuedTurnIds((current) => new Set(current).add(entry.turnId));
								void onPromoteQueuedTurn(entry.turnId)
									.then(() => haptics.success())
									.catch(() => {
										setHiddenQueuedTurnIds((current) => {
											const next = new Set(current);
											next.delete(entry.turnId);
											return next;
										});
										haptics.error();
									})
									.finally(() => setPromotingQueuedTurnId(undefined));
							}}
							style={({ pressed }) => [styles.queueSteer, pressed && { opacity: 0.55 }]}
						>
							{promoting ? <ActivityIndicator size="small" color={t.accent} /> : <Feather name="corner-up-right" size={15} color={t.accent} />}
						</Pressable> : null}
						<Pressable
							accessibilityRole="button"
							accessibilityLabel={`Delete queued message: ${entry.message.text}`}
							accessibilityState={{ busy: cancelling, disabled: queueActionPending }}
							disabled={queueActionPending}
							onPress={() => {
								haptics.tap();
								setCancellingQueuedTurnId(entry.turnId);
								void onCancelQueuedTurn(entry.turnId)
									.then(() => haptics.success())
									.catch(() => haptics.error())
									.finally(() => setCancellingQueuedTurnId(undefined));
							}}
							style={({ pressed }) => [styles.queueDelete, pressed && { opacity: 0.55 }]}
						>
							{cancelling ? <ActivityIndicator size="small" color={t.textTertiary} /> : <Feather name="x" size={15} color={t.textTertiary} />}
						</Pressable>
					</View>;
				})}
			</View> : null}
			{request && !requestCard ? <Pressable
				accessibilityRole="button"
				accessibilityLabel={`${request.title}. Answer it`}
				onPress={() => { haptics.tap(); onRestoreRequest(); }}
				style={({ pressed }) => [styles.restore, pressed && { opacity: 0.6 }]}
			>
				<Feather name={request.kind === "approval" ? "shield" : "message-circle"} size={12} color={t.amber} />
				<Text numberOfLines={1} maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.restoreText}>{request.title}</Text>
				<Text maxFontSizeMultiplier={fontScaleCap.chrome} style={styles.restoreAction}>Answer</Text>
			</Pressable> : null}
			{requestCard ?? <Animated.View
				style={[styles.composer, activeReviewPR && styles.reviewComposer, activeReviewPR && reviewComposerSurfaceStyle, stopped && { opacity: 0.55 }]}
			>
				{activeReviewPR ? <Animated.View pointerEvents="none" style={[StyleSheet.absoluteFill, reviewComposerGlassStyle]}><ComposerGlass radius={COMPOSER_RADIUS} /></Animated.View> : <ComposerGlass radius={COMPOSER_RADIUS} />}
				{quotes.length ? <ScrollView style={styles.quotes} keyboardShouldPersistTaps="handled">
					{quotes.map((quote) => <View key={quote.id} style={styles.quote}>
						<Text numberOfLines={2} style={styles.quoteText} accessibilityLabel={`Quoted response: ${quote.text}`}>{quote.text}</Text>
						<Pressable accessibilityRole="button" accessibilityLabel={`Remove quote: ${quote.text}`} onPress={() => {
							haptics.tap();
							const next = latestQuotes.current.filter((item) => item.id !== quote.id);
							latestQuotes.current = next;
							setQuotes(next);
						}} style={styles.removeQuote}><Feather name="x" size={iconSize.lg} color={t.textSecondary} /></Pressable>
					</View>)}
				</ScrollView> : null}
				<View style={styles.composerRow}>
					<ChatAttachmentMenu disabled={stopped || submitting} canAttachFile={Boolean(canEmbedFiles)} onChoosePhoto={() => void addImage()} onChooseFile={() => void addFile()} onChooseSkill={() => void openPicker("skills")} />
					<TextInput
						ref={inputRef}
						onLayout={pasteLayout}
						accessibilityLabel="Message the agent"
						editable={!stopped && !submitting}
						value={text}
						onChangeText={(value) => { latestText.current = value; setText(value); }}
						onSelectionChange={(event) => setCursor(event.nativeEvent.selection.start)}
						onContentSizeChange={(event) => {
							if (!latestText.current) {
								setFieldHeight(COMPOSER_FIELD_HEIGHT);
								return;
							}
							// Native contentSize already includes the TextInput's vertical
							// padding. Treat it as the field's full height; converting it to
							// lines counted the padding as an extra line on the first render.
							const contentHeight = Math.ceil(event.nativeEvent.contentSize.height);
							setFieldHeight(Math.max(COMPOSER_FIELD_HEIGHT, Math.min(COMPOSER_FIELD_MAX_HEIGHT, contentHeight)));
						}}
						placeholder={stopped ? "Agent is stopped" : startingAgentName ? `${startingAgentName} is starting…` : deliveryPresentation.placeholder}
						placeholderTextColor={t.textFaint}
						{...textSelectionColors(t)}
						style={[styles.input, { height: text ? fieldHeight : COMPOSER_FIELD_HEIGHT }]}
						multiline
						maxLength={40_000}
					/>
					<MicKey variant="plain" size={44} glyphSize={iconSize.lg} disabled={stopped || submitting} state={voice.state} mode={voice.mode} onPressIn={voice.pressIn} onPressOut={voice.pressOut} />
					{primaryAction === "stop" ? <Pressable accessibilityRole="button" accessibilityLabel="Stop turn" accessibilityState={{ busy: interrupting, disabled: disabled || interrupting }} disabled={disabled || interrupting} onPress={() => { haptics.tap(); void onInterrupt(); }} style={[styles.stop, (disabled || interrupting) && { opacity: 0.55 }]}>{interrupting ? <ActivityIndicator size="small" color={t.textPrimary} /> : <Feather name="square" size={12} color={t.textPrimary} />}</Pressable> : <Pressable accessibilityRole="button" accessibilityLabel={active ? "Queue message" : "Send message"} accessibilityState={{ disabled: disabled || stopped || pending || submitting || Boolean(draftKey && !draftLoaded) }} disabled={disabled || stopped || pending || submitting || Boolean(draftKey && !draftLoaded) || (!hasDraft && attachments.length === 0)} onPress={() => { haptics.tap(); void submit("send"); }} style={({ pressed }) => [styles.send, pressed && { opacity: 0.8 }, (disabled || stopped || pending || submitting || Boolean(draftKey && !draftLoaded) || (!hasDraft && attachments.length === 0)) && { opacity: 0.35 }]}>{pending || submitting ? <ActivityIndicator size="small" color={t.bgBase} /> : <Feather name="arrow-up" size={17} color={t.bgBase} />}</Pressable>}
				</View>
			</Animated.View>}
		</>
	);
	return (
		// The dock holds its resting inset at all times and rides the keyboard's own
		// progress to close the difference, so its distance to the keyboard is
		// `KEYBOARD_DOCK_GAP` at every frame of the animation rather than only once
		// the keyboard has finished moving.
		<Animated.View style={[styles.dock, { paddingBottom: restingInset }, dockRise]}>
			{showReviewPrompt ? <Animated.View style={[styles.reviewArea, styles.reviewAreaWithPrompt]}>
				<Animated.View style={[styles.reviewContainer, reviewCardDragStyle]} {...(reviewPRCollapsed ? {} : reviewCardPan.panHandlers)}>
					{activeReviewPR ? <Animated.View pointerEvents="none" style={[StyleSheet.absoluteFill, reviewCardGlassStyle]}><ComposerGlass radius={24} /></Animated.View> : null}
					{composerContents}
				</Animated.View>
			</Animated.View> : composerContents}
		</Animated.View>
	);
}
