import AsyncStorage from "@react-native-async-storage/async-storage";
import * as DocumentPicker from "expo-document-picker";
import * as ImagePicker from "expo-image-picker";
import { useRouter } from "expo-router";
import { useCallback, useRef } from "react";
import { Keyboard } from "react-native";
import { haptics } from "../haptics";
import { type ChatExcerpt, type ChatImage, type ChatResource, type ChatSkill } from "./types";
import { composerSuggestionKey, findComposerSuggestion, insertComposerSkill, replaceComposerSuggestion, type ComposerSuggestion } from "./composerSuggestions";
import { chatSheetRoute } from "./chatSheetRegistry";
import { COMPOSER_FIELD_HEIGHT } from "./composerStyles";
import { composerDeliveryRoute, type ComposerDeliveryIntent } from "./composerDeliveryModel";
import { userFacingError } from "../connectionError";
import { encodeResponseDraft, responseReplyText, responseExcerpts, type ResponseQuote } from "./responseQuotes";
import { afterDraftWrites } from "./pendingSend";
import { Attachment, MAX_ATTACHMENTS, MAX_EMBEDDED_FILE_BYTES } from "./composerAttachments";
import { addComposerImages, placeAddedImages, type ComposerImageAsset, type PasteOverflow } from "./composerImages";
import { RequestGate } from "./requestGate";

export function useComposerActions({
	submitting,
	pending,
	disabled,
	draftKey,
	draftLoaded,
	text,
	quotes,
	attachments,
	setLocalError,
	setSubmitting,
	draftTimer,
	steerEligible,
	onSteer,
	onSend,
	latestText,
	latestQuotes,
	onAcknowledgeSend,
	setText,
	setFieldHeight,
	setQuotes,
	setAttachments,
	pickerGate,
	onLoadSkills,
	skills,
	onLoadFiles,
	filePaths,
	filePathsTruncated,
	latestCursor,
	router,
	setCursor,
}: {
	submitting: boolean;
	pending: boolean | undefined;
	disabled: boolean | undefined;
	draftKey: string | null;
	draftLoaded: boolean;
	text: string;
	quotes: ResponseQuote[];
	attachments: Attachment[];
	setLocalError: React.Dispatch<React.SetStateAction<string | undefined>>;
	setSubmitting: React.Dispatch<React.SetStateAction<boolean>>;
	draftTimer: React.RefObject<NodeJS.Timeout | null>;
	steerEligible: boolean;
	onSteer: (text: string) => Promise<string>;
	onSend: (text: string, attachments?: ChatImage[] | undefined, resources?: ChatResource[] | undefined, excerpts?: ChatExcerpt[] | undefined) => Promise<string>;
	latestText: React.RefObject<string>;
	latestQuotes: React.RefObject<ResponseQuote[]>;
	onAcknowledgeSend: (id: string) => Promise<void>;
	setText: React.Dispatch<React.SetStateAction<string>>;
	setFieldHeight: React.Dispatch<React.SetStateAction<number>>;
	setQuotes: React.Dispatch<React.SetStateAction<ResponseQuote[]>>;
	setAttachments: React.Dispatch<React.SetStateAction<Attachment[]>>;
	pickerGate: RequestGate;
	onLoadSkills: () => Promise<ChatSkill[]>;
	skills: ChatSkill[];
	onLoadFiles: () => Promise<{ paths: string[]; truncated: boolean; }>;
	filePaths: string[];
	filePathsTruncated: boolean | undefined;
	latestCursor: React.RefObject<number>;
	router: ReturnType<typeof useRouter>;
	setCursor: React.Dispatch<React.SetStateAction<number>>;
}) {

	const submit = useCallback(async (intent: ComposerDeliveryIntent = "send") => {
		if (submitting || pending || disabled || (draftKey && !draftLoaded)) return;
		const trimmed = responseReplyText(text, quotes);
		if (!trimmed && attachments.length === 0) return;
		// Dismissed on the tap, not after the send lands. Waiting for the request
		// tied the keyboard's exit to the network, so it dropped whenever the
		// answer arrived — which read as the keyboard being taken away mid-sentence.
		Keyboard.dismiss();
		setLocalError(undefined);
		setSubmitting(true);
		if (draftTimer.current) clearTimeout(draftTimer.current);
		try {
			// Submission cancels the debounce; persist before a preflight can reject.
			if (draftKey) {
				await afterDraftWrites(draftKey, () => AsyncStorage.setItem(draftKey, encodeResponseDraft(text, quotes)));
			}
			const excerpts = responseExcerpts(quotes);
			const images = attachments.filter((item): item is Extract<Attachment, { kind: "image" }> => item.kind === "image").map((item) => item.image);
			const resources = attachments.filter((item): item is Extract<Attachment, { kind: "resource" }> => item.kind === "resource").map((item) => item.resource);
			const route = composerDeliveryRoute(intent, steerEligible);
			const id = route === "steer"
				? await onSteer(trimmed)
				: await onSend(trimmed, images.length ? images : undefined, resources.length ? resources : undefined, excerpts.length ? excerpts : undefined);
			if (draftKey) {
				const current = latestText.current;
				const currentQuotes = latestQuotes.current;
				const unchanged = current === text && currentQuotes === quotes;
				await afterDraftWrites(draftKey, () => unchanged ? AsyncStorage.removeItem(draftKey) : AsyncStorage.setItem(draftKey, encodeResponseDraft(current, currentQuotes)));
			}
			await onAcknowledgeSend(id);
			if (latestText.current === text && latestQuotes.current === quotes) {
				latestText.current = "";
				setText("");
				setFieldHeight(COMPOSER_FIELD_HEIGHT);
				latestQuotes.current = [];
				setQuotes([]);
				setAttachments([]);
			}
			haptics.success();
		} catch (cause) {
			setLocalError(userFacingError(cause));
			haptics.error();
		} finally { setSubmitting(false); }
	}, [text, quotes, attachments, steerEligible, onSteer, onSend, onAcknowledgeSend, draftKey, draftLoaded, submitting, pending, disabled]);

	const latestAttachments = useRef(attachments);
	latestAttachments.current = attachments;
	const addImages = (assets: readonly ComposerImageAsset[], overflow?: PasteOverflow) => {
		// Reads the latest list, so a pick or paste that outlives this render still lands.
		const before = latestAttachments.current;
		const added = addComposerImages(before, assets, overflow);
		latestAttachments.current = added.items;
		setAttachments(added.items);
		if (added.added > 0) {
			const placed = placeAddedImages(latestText.current, latestCursor.current, before, added.added);
			latestText.current = placed.text;
			latestCursor.current = placed.caret;
			setText(placed.text);
			setCursor(placed.caret);
		}
		setLocalError(added.error);
		return added.added > 0;
	};
	const pasteImages = (assets: readonly ComposerImageAsset[], overflow?: PasteOverflow) => {
		if (addImages(assets, overflow)) haptics.tap();
	};
	const addImage = async () => {
		setLocalError(undefined);
		try {
			const result = await ImagePicker.launchImageLibraryAsync({ mediaTypes: ["images"], base64: true, quality: 0.82, allowsMultipleSelection: true, selectionLimit: 4 });
			if (result.canceled) return;
			addImages(result.assets);
		} catch (cause) {
			setLocalError(userFacingError(cause, "Couldn't open your photo library."));
		}
	};
	const addFile = async () => {
		setLocalError(undefined);
		const result = await DocumentPicker.getDocumentAsync({ multiple: true, copyToCacheDirectory: true, type: ["text/*", "application/json", "application/xml", "application/yaml"] });
		if (result.canceled) return;
		try {
			const added: Attachment[] = [];
			for (const asset of result.assets) {
				if (attachments.length + added.length >= MAX_ATTACHMENTS) throw new Error(`You can attach up to ${MAX_ATTACHMENTS} items.`);
				if ((asset.size ?? 0) > MAX_EMBEDDED_FILE_BYTES) throw new Error(`${asset.name} is larger than 500 KB. Reference a worktree file with @ instead.`);
				const body = await fetch(asset.uri).then((response) => response.text());
				const bytes = new TextEncoder().encode(body).byteLength;
				if (bytes > MAX_EMBEDDED_FILE_BYTES) throw new Error(`${asset.name} is too large to embed. Reference a worktree file with @ instead.`);
				added.push({ id: `${asset.uri}-${Date.now()}`, kind: "resource", name: asset.name, bytes, resource: { uri: `mobile-attachment://${encodeURIComponent(asset.name)}`, name: asset.name, mimeType: asset.mimeType || "text/plain", text: body } });
			}
			setAttachments((old) => [...old, ...added]);
		} catch (cause) { setLocalError(userFacingError(cause)); }
	};

	const openPicker = useCallback(async (kind: "skills" | "files", activeTrigger?: ComposerSuggestion) => {
		const request = pickerGate.begin();
		const loadedSkills = kind === "skills" ? await onLoadSkills() : skills;
		const loadedFiles = kind === "files" ? await onLoadFiles() : { paths: filePaths, truncated: Boolean(filePathsTruncated) };
		if (!pickerGate.isCurrent(request)) return;
		if (activeTrigger) {
			const currentTrigger = findComposerSuggestion(latestText.current, latestCursor.current);
			if (!currentTrigger || composerSuggestionKey(currentTrigger) !== composerSuggestionKey(activeTrigger)) return;
		}
		const pickerCatalog = kind === "skills"
			? { kind, skills: loadedSkills } as const
			: { kind, paths: loadedFiles.paths } as const;
		router.push(chatSheetRoute({ kind: "composer-picker", catalog: pickerCatalog, initialQuery: activeTrigger?.query, truncated: kind === "files" ? loadedFiles.truncated : undefined, onSelect: (value) => {
			setText((old) => {
				const next = activeTrigger ? replaceComposerSuggestion(old, activeTrigger, value) : kind === "skills" ? insertComposerSkill(old, value) : `${old}${old && !/\s$/.test(old) ? " " : ""}${/\s/.test(value) ? `"${value}"` : value} `;
				setCursor(next.length);
				return next;
			});
		} }));
	}, [filePaths, filePathsTruncated, onLoadFiles, onLoadSkills, pickerGate, router, skills]);

	return { submit, addImage, pasteImages, addFile, openPicker };
}
