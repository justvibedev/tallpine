import { Feather } from "../icons";
import * as DocumentPicker from "expo-document-picker";
import * as ImagePicker from "expo-image-picker";
import { useCallback, useEffect, useRef, useState } from "react";
import { ActivityIndicator, Image, Pressable, ScrollView, Text, TextInput, View } from "react-native";
import Animated, { useAnimatedStyle } from "react-native-reanimated";
import { useReanimatedKeyboardAnimation } from "react-native-keyboard-controller";
import { haptics } from "../haptics";
import { KEYBOARD_DOCK_GAP } from "../session/keyboardInset";
import { useTheme, useThemedStyles } from "../ThemeProvider";
import { textSelectionColors } from "../textSelection";
import { iconSize } from "../tokens";
import { MicKey } from "../voice/MicKey";
import { useVoiceInput } from "../voice/useVoiceInput";
import { ChatAttachmentMenu } from "./ChatAttachmentMenu";
import { ComposerGlass } from "./composer-glass";
import { COMPOSER_FIELD_HEIGHT, COMPOSER_FIELD_MAX_HEIGHT, COMPOSER_RADIUS, composerStyleRules } from "./composerStyles";
import { addComposerImages, type ComposerImageAsset, type PasteOverflow } from "./composerImages";
import { useImagePaste } from "./useImagePaste";
import type { ChatImage, ChatResource } from "./types";

type Attachment =
	| { id: string; kind: "image"; name: string; bytes: number; image: ChatImage }
	| { id: string; kind: "resource"; name: string; resource: ChatResource };

const MAX_ATTACHMENTS = 8;
const MAX_FILE_BYTES = 500_000;

/** Reviewer-only composer: send/attachments/interrupt, deliberately no session actions. */
export function ReviewerComposer({ busy, stopped, attachmentsEnabled = true, onSend, onAcknowledgeSend, completedRetry, onInterrupt, restingInset, dockRiseOverride }: {
	busy: boolean;
	stopped: boolean;
	attachmentsEnabled?: boolean;
	onSend(text: string, images?: ChatImage[], resources?: ChatResource[]): Promise<string>;
	onAcknowledgeSend(id: string): Promise<void>;
	completedRetry?: { id: string; draftText: string };
	onInterrupt(): Promise<void>;
	/** The dock's inset with the keyboard down; see ChatComposer. */
	restingInset: number;
	/** See ChatComposer's prop of the same name. */
	dockRiseOverride?: number;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const [text, setText] = useState("");
	const [attachments, setAttachments] = useState<Attachment[]>([]);
	const [submitting, setSubmitting] = useState(false);
	const [error, setError] = useState("");
	const [fieldHeight, setFieldHeight] = useState(COMPOSER_FIELD_HEIGHT);
	const keyboard = useReanimatedKeyboardAnimation();
	const voice = useVoiceInput({ onTranscript: useCallback((spoken: string) => setText((old) => old ? `${old} ${spoken}` : spoken), []) });
	useEffect(() => {
		if (!completedRetry) return;
		if (text.trim() === completedRetry.draftText) { setText(""); setAttachments([]); }
		void onAcknowledgeSend(completedRetry.id).catch((cause) => setError(cause instanceof Error ? cause.message : "Could not clear the sent reply."));
	}, [completedRetry, onAcknowledgeSend, text]);

	const latestAttachments = useRef(attachments);
	latestAttachments.current = attachments;
	const addImages = (assets: readonly ComposerImageAsset[], overflow?: PasteOverflow) => {
		const added = addComposerImages(latestAttachments.current, assets, overflow);
		latestAttachments.current = added.items;
		setAttachments(added.items);
		setError(added.error ?? "");
		if (added.added > 0) haptics.tap();
	};
	const pasteLayout = useImagePaste(stopped || submitting || !attachmentsEnabled ? null : addImages);
	const addPhoto = async () => {
		setError("");
		try {
			const result = await ImagePicker.launchImageLibraryAsync({ mediaTypes: ["images"], base64: true, quality: 0.82, allowsMultipleSelection: true, selectionLimit: 4 });
			if (result.canceled) return;
			addImages(result.assets);
		} catch (cause) { setError(cause instanceof Error ? cause.message : "Could not attach this photo."); }
	};

	const addFile = async () => {
		setError("");
		try {
			const result = await DocumentPicker.getDocumentAsync({ multiple: true, copyToCacheDirectory: true, type: ["text/*", "application/json", "application/xml", "application/yaml"] });
			if (result.canceled) return;
			const added: Attachment[] = [];
			for (const asset of result.assets) {
				if (attachments.length + added.length >= MAX_ATTACHMENTS) throw new Error(`You can attach up to ${MAX_ATTACHMENTS} items.`);
				if ((asset.size || 0) > MAX_FILE_BYTES) throw new Error(`${asset.name} is larger than 500 KB.`);
				const body = await fetch(asset.uri).then((response) => response.text());
				if (new TextEncoder().encode(body).byteLength > MAX_FILE_BYTES) throw new Error(`${asset.name} is larger than 500 KB.`);
				added.push({ id: `${asset.uri}-${Date.now()}`, kind: "resource", name: asset.name, resource: { uri: `mobile-attachment://${encodeURIComponent(asset.name)}`, name: asset.name, mimeType: asset.mimeType || "text/plain", text: body } });
			}
			setAttachments((current) => [...current, ...added]);
		} catch (cause) { setError(cause instanceof Error ? cause.message : "Could not attach this file."); }
	};

	const submit = async () => {
		if (submitting || stopped || (!text.trim() && !attachments.length)) return;
		setSubmitting(true); setError("");
		try {
			const id = await onSend(text.trim(), attachments.flatMap((item) => item.kind === "image" ? [item.image] : []), attachments.flatMap((item) => item.kind === "resource" ? [item.resource] : []));
			setText(""); setAttachments([]); setFieldHeight(COMPOSER_FIELD_HEIGHT);
			await onAcknowledgeSend(id);
			haptics.success();
		} catch (cause) { setError(cause instanceof Error ? cause.message : "Could not send this reply."); }
		finally { setSubmitting(false); }
	};

	// Same dock and pill as the session composer (ChatComposer), minus the turn
	// settings and queue rows a reviewer has no use for. The dock holds its resting
	// inset and rides the keyboard's progress to close the difference, exactly as
	// the session composer does, so the two sit in the same place.
	const dockRise = useAnimatedStyle(() => ({
		transform: [{ translateY: (restingInset - KEYBOARD_DOCK_GAP) * (dockRiseOverride ?? keyboard.progress.value) }],
	}));
	const sendDisabled = stopped || submitting || (!text.trim() && !attachments.length);
	return <Animated.View style={[styles.dock, { paddingBottom: restingInset }, dockRise]}>
		{voice.state === "starting" || voice.state === "recording" ? <View style={styles.voice}><Feather name="mic" size={12} color={t.red} /><Text style={styles.voiceText}>{voice.partial || (voice.state === "starting" ? "Keep holding…" : "Listening…")}</Text></View> : null}
		{attachments.length ? <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={styles.attachments}>{attachments.map((item) => <View key={item.id} style={styles.attachment}>{item.kind === "image" ? <Image accessibilityIgnoresInvertColors source={{ uri: `data:${item.image.mimeType};base64,${item.image.data}` }} style={styles.attachmentImage} /> : <Feather name="file-text" size={12} color={t.accent} />}<Text numberOfLines={1} style={styles.attachmentName}>{item.name}</Text><Pressable hitSlop={7} accessibilityLabel={`Remove ${item.name}`} onPress={() => { haptics.tap(); setAttachments((current) => current.filter((candidate) => candidate.id !== item.id)); }}><Feather name="x" size={12} color={t.textTertiary} /></Pressable></View>)}</ScrollView> : null}
		{error || voice.error ? <Text accessibilityRole="alert" style={styles.error}>{error || voice.error}</Text> : null}
		<View style={[styles.composer, stopped && { opacity: 0.55 }]}>
			<ComposerGlass radius={COMPOSER_RADIUS} />
			<View style={styles.composerRow}>
				<ChatAttachmentMenu disabled={stopped || submitting || !attachmentsEnabled} canAttachFile onChoosePhoto={() => void addPhoto()} onChooseFile={() => void addFile()} />
				<TextInput
					onLayout={pasteLayout}
					accessibilityLabel="Reply to reviewer"
					value={text}
					onChangeText={setText}
					onContentSizeChange={(event) => {
						// Native contentSize already includes the field's vertical padding,
						// as in ChatComposer.
						const contentHeight = Math.ceil(event.nativeEvent.contentSize.height);
						setFieldHeight(Math.max(COMPOSER_FIELD_HEIGHT, Math.min(COMPOSER_FIELD_MAX_HEIGHT, contentHeight)));
					}}
					placeholder={stopped ? "Reviewer is stopped" : "Reply to reviewer…"}
					placeholderTextColor={t.textFaint}
					{...textSelectionColors(t)}
					multiline
					maxLength={40_000}
					editable={!stopped && !submitting}
					style={[styles.input, { height: text ? fieldHeight : COMPOSER_FIELD_HEIGHT }]}
				/>
				<MicKey variant="plain" size={44} glyphSize={iconSize.lg} disabled={stopped || submitting} state={voice.state} mode={voice.mode} onPressIn={voice.pressIn} onPressOut={voice.pressOut} />
				{busy ? <Pressable accessibilityRole="button" accessibilityLabel="Stop reviewer" onPress={() => { haptics.tap(); void onInterrupt().catch((cause) => setError(cause instanceof Error ? cause.message : "Could not stop the reviewer.")); }} style={({ pressed }) => [styles.stop, pressed && { opacity: 0.8 }]}><Feather name="square" size={12} color={t.textPrimary} /></Pressable> : <Pressable accessibilityRole="button" accessibilityLabel="Send reply" accessibilityState={{ disabled: sendDisabled, busy: submitting }} disabled={sendDisabled} onPress={() => { haptics.tap(); void submit(); }} style={({ pressed }) => [styles.send, pressed && { opacity: 0.8 }, sendDisabled && { opacity: 0.35 }]}>{submitting ? <ActivityIndicator size="small" color={t.bgBase} /> : <Feather name="arrow-up" size={17} color={t.bgBase} />}</Pressable>}
			</View>
		</View>
	</Animated.View>;
}

// The session composer's dock, pill, field and controls; it draws nothing of its own.
const makeStyles = composerStyleRules;
