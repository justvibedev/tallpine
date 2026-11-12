import { apiErrorCode, apiErrorMessage, getApiBaseUrl } from "../../../lib/api-client";
import { isSupportedImageAttachment, type FileAttachment, MAX_ATTACHMENTS, type FileAttachmentPayload } from "../../../hooks/useFileAttachments";
import { cancelChatComposerMutation, clearRejectedChatComposerDelivery, getChatComposerMutation, isChatComposerMutationCurrent, prepareChatComposerDelivery, readChatSessionDraft, type ChatDraftMutationToken } from "../../../lib/chat-drafts";
import { attachmentURL, isImageAttachment, proseBesideImages, splitInlineImagePaths, withAttachmentReferences } from "@tallpine/shared/chat/attachments";
import { placeholdersForPaths } from "@tallpine/shared/chat/image-placeholders";
import { useComposerDraftState } from "./useComposerDraftState";
import { useComposerSuggestions } from "./useComposerSuggestions";
import { useComposerDraftPersistence } from "./useComposerDraftPersistence";
import { ChatComposerProps } from "./ComposerProps";
import { resizeOriginals, MAX_NATIVE_IMAGE_BYTES, MAX_NATIVE_IMAGES_BYTES, DEFINITIVE_SEND_REJECTIONS, AGENT_SWITCH_FAILED } from "./composer-model";


export type SubmitEnv = {
	props: ChatComposerProps;
	draft: ReturnType<typeof useComposerDraftState>;
	suggestionState: ReturnType<typeof useComposerSuggestions>;
	persistence: ReturnType<typeof useComposerDraftPersistence>;
};

// What prepareSubmission settled, for the local or durable delivery paths.
type Submission = {
	currentText: string;
	message: string;
	shouldSteer: boolean;
	settledAttachments: FileAttachment[];
	recoveringDelivery: ReturnType<typeof useComposerDraftState>["durableDelivery"];
	sendNativeImages: boolean;
	retainedNativeImageCount: number;
	restoreNativePayloads: () => Promise<boolean>;
	readonly nativePayloads: FileAttachmentPayload[];
};

export async function handleCompact(env: SubmitEnv, onCompact: () => unknown, mutationToken: ChatDraftMutationToken | undefined): Promise<void> {
	const { compacting, compactUnavailable, compactBlocked } = env.props;
	const { composerRevision, draftScope, setDismissedKey, setHighlighted, setSendError, setSubmitting } = env.draft;
	const { clearAcceptedDraft } = env.persistence;
		if (draftScope && !mutationToken) return;
		let mutationFinished = false;
		setSubmitting(true);
		try {
			if (compactBlocked) {
				setSendError("Stop the current turn before compacting.");
				return;
			}
			if (compacting) {
				setSendError("Conversation history is already being compacted.");
				return;
			}
			if (compactUnavailable) {
				setSendError(compactUnavailable);
				return;
			}
			await onCompact();
			clearAcceptedDraft(composerRevision.current, mutationToken);
			mutationFinished = true;
			setDismissedKey(null);
			setHighlighted(0);
		} catch {
			setSendError("Conversation history could not be compacted. Try again.");
		} finally {
			if (draftScope && mutationToken && !mutationFinished) {
				cancelChatComposerMutation(draftScope, mutationToken);
			}
			setSubmitting(false);
		}
}

export async function prepareSubmission(env: SubmitEnv, forceSteer: boolean | undefined, start: { currentText: string; body: string; recoveringDelivery: ReturnType<typeof useComposerDraftState>["durableDelivery"]; sendNativeImages: boolean }) : Promise<Submission | undefined> {
	const { busy, disabled, steerPending, queuedDraftScope, savingQueuedEditPending, assetBaseUrl, remoteHost, assetSessionId } = env.props;
	const { composerMutation, contextReferences, draftScope, editor, fileAttachments, setSendError, textRef, visibleRetainedAttachments } = env.draft;
	const { draftMutationPending, savingQueuedEdit } = env.suggestionState;
	let currentText = start.currentText;
	let body = start.body;
	const { recoveringDelivery, sendNativeImages } = start;
	await fileAttachments.toSettledPayload();
	// A replacement hook can still have staging work owned by the old surface.
	if (fileAttachments.hasPendingReads()) return;
	const settledAttachments = fileAttachments.getAttachments();
	const settledPaths = settledAttachments.flatMap((attachment) =>
		attachment.stagedPath ? [attachment.stagedPath] : []);
	const attachedPaths = [
		...visibleRetainedAttachments.flatMap((attachment) => attachment.path ? [attachment.path] : []),
		...settledPaths,
	];
	// Staging that just settled has filled its inline chips, so read the text the
	// user sees now. First drop chips whose image is gone (an undo can restore
	// one); typed paths are the user's text and stay.
	editor.current?.pruneImages(attachedPaths);
	currentText = textRef.current;
	body = proseBesideImages(currentText.trim(), attachedPaths);
	const hasAttachments = settledAttachments.length > 0 || visibleRetainedAttachments.length > 0;
	const canSubmitNow =
		(body.length > 0 || hasAttachments || contextReferences.length > 0 || Boolean(recoveringDelivery)) &&
		(!busy || savingQueuedEdit || recoveringDelivery?.state === "accepted") &&
		!disabled && !steerPending && !savingQueuedEditPending &&
		!composerMutation.pending &&
		(!draftMutationPending || Boolean(recoveringDelivery)) &&
		(Boolean(recoveringDelivery) ||
			getChatComposerMutation(draftScope ?? "").accepted?.result.ok !== false);
	if (!canSubmitNow) {
		if (busy && !disabled && !recoveringDelivery && !savingQueuedEdit) {
			setSendError("Still sending the previous message. Try again in a moment.");
		}
		return;
	}
	if (hasAttachments && settledPaths.length !== settledAttachments.length) {
		setSendError("chat.draft.filesUnavailable");
		return;
	}
	const shouldSteer = Boolean(forceSteer && !savingQueuedEdit && contextReferences.length === 0);
	// The agent reads [IMGn] where each image chip sat, then the block naming each
	// image's staged path and full original; the bubble shows only the text.
	const images = attachedPaths.filter(isImageAttachment);
	const withPlaceholders = placeholdersForPaths(body, images, (text) => splitInlineImagePaths(text, (path) => images.includes(path)));
	const originals = {
		...Object.fromEntries(visibleRetainedAttachments.flatMap((attachment) =>
			attachment.path && attachment.originalPath ? [[attachment.path, attachment.originalPath]] : [])),
		...resizeOriginals(settledAttachments),
	};
	const message = withAttachmentReferences(withPlaceholders, attachedPaths, originals) ||
		(contextReferences.length > 0 ? `Use the attached ${contextReferences.length} chat excerpt(s) as context` : "");
	// Ordinary delivery reserves its exact draft before these staged reads await.
	// Queue editors use their existing owner/revision CAS before mutation.
	const attachmentScope = queuedDraftScope ?? draftScope;
	const retainedNativeImageCount = visibleRetainedAttachments.filter((item) => item.contentType === "image").length;
	let nativeImageBytes = 0;
	const deliveryAttachments = recoveringDelivery?.draft?.attachments.map<FileAttachment>((attachment) =>
		settledAttachments.find((staged) => staged.id === attachment.id) ?? {
			...attachment, stagedPath: attachment.path,
		});
	const nativeImageAttachments = (deliveryAttachments ?? settledAttachments).filter((attachment) => {
		// Retained image sizes are server-owned and unknown here. Keep new images
		// as workspace files so an edit cannot exceed the native 25 MiB budget.
		if (!sendNativeImages || retainedNativeImageCount > 0 || !isSupportedImageAttachment(attachment.mimeType) ||
			attachment.bytes > MAX_NATIVE_IMAGE_BYTES ||
			nativeImageBytes + attachment.bytes > MAX_NATIVE_IMAGES_BYTES) return false;
		nativeImageBytes += attachment.bytes;
		return true;
	});
	let nativePayloads = nativeImageAttachments.flatMap(({ mimeType, data }) =>
		data ? [{ mimeType, data }] : []);
	const restoreNativePayloads = async (): Promise<boolean> => {
		if (!attachmentScope || !sendNativeImages || recoveringDelivery?.kind === "steer" || recoveringDelivery?.state === "accepted") return true;
		const restored: FileAttachmentPayload[] = [];
		try {
			for (const attachment of nativeImageAttachments) {
				if (attachment.data) {
					restored.push({ mimeType: attachment.mimeType, data: attachment.data });
					continue;
				}
				if (!attachment.stagedPath) throw new Error("Missing staged attachment");
				const assetOrigin = remoteHost ? assetBaseUrl : assetBaseUrl ?? getApiBaseUrl();
				if (assetOrigin === undefined) throw new Error("Remote host disconnected");
				const response = await fetch(attachmentURL(assetOrigin, assetSessionId ?? attachmentScope.sessionId, attachment.stagedPath));
				if (!response.ok) throw new Error("Could not read staged attachment");
				const blob = await response.blob();
				const data = await new Promise<string>((resolve, reject) => {
					const reader = new FileReader();
					reader.onload = () => resolve(String(reader.result).split(",")[1] ?? "");
					reader.onerror = () => reject(new Error("Could not read staged attachment"));
					reader.readAsDataURL(blob);
				});
				restored.push({ mimeType: attachment.mimeType, data });
			}
			nativePayloads = restored;
			return true;
		} catch {
			setSendError("chat.draft.readAttachmentFailed");
			return false;
		}
	};
	if ((!draftScope || savingQueuedEdit) && !await restoreNativePayloads()) return;

	if (savingQueuedEdit && nativePayloads.length + retainedNativeImageCount > MAX_ATTACHMENTS) {
		setSendError(`You can attach up to ${MAX_ATTACHMENTS} images.`);
		return;
	}
	return {
		currentText, message, shouldSteer, settledAttachments, recoveringDelivery, sendNativeImages,
		retainedNativeImageCount, restoreNativePayloads,
		get nativePayloads() { return nativePayloads; },
	};
}

export async function sendLocal(env: SubmitEnv, sub: Submission): Promise<void> {
	const { currentText, message, shouldSteer } = sub;
	const { onSend, onSteer } = env.props;
	const { editor, fileAttachments, hasTextRef, setHasText, setSendError, setSteerOutcomeNotice, setSubmitting, textRef, visibleRetainedAttachments } = env.draft;
	const { savingQueuedEdit, staged } = env.suggestionState;
	const { clearEditorView } = env.persistence;
		setSubmitting(true);
		// A plain-text send has a local timeline echo — clear the editor immediately
		// so the user sees one acknowledgement rather than their draft stranded until
		// the daemon round-trip completes. Attachments retain the retry path.
		const clearForLocalEcho = !shouldSteer && !savingQueuedEdit && sub.nativePayloads.length === 0;
		try {
			if (clearForLocalEcho) clearEditorView();
			if (shouldSteer && onSteer) {
				const outcome = sub.nativePayloads.length > 0
					? await onSteer(message, sub.nativePayloads)
					: await onSteer(message);
				if (outcome?.status === "not-accepted") {
					setSteerOutcomeNotice(outcome.reason);
					return;
				}
			} else if (savingQueuedEdit) {
				await onSend(message, sub.nativePayloads.length ? sub.nativePayloads : undefined, undefined,
					visibleRetainedAttachments.flatMap((item) => item.contentIndex === undefined ? [] : [item.contentIndex]));
				// The parent leaves this editor only after proving durable cleanup.
				return;
			} else if (sub.nativePayloads.length > 0) {
				await onSend(message, sub.nativePayloads);
			} else {
				await onSend(message);
			}
			if (!clearForLocalEcho) clearEditorView();
			fileAttachments.clear();
		} catch (error) {
			if (clearForLocalEcho && !textRef.current) {
				textRef.current = currentText;
				hasTextRef.current = currentText.trim().length > 0;
				setHasText(hasTextRef.current);
				editor.current?.setText(currentText);
			}
			setSendError(
				savingQueuedEdit
					? apiErrorMessage(error, "chat.draft.queueSaveFailed")
					: staged
					? "chat.draft.sendAttachmentsFailed"
					: "chat.draft.sendFailed",
			);
		} finally {
			setSubmitting(false);
		}
}

export async function sendDurable(env: SubmitEnv, sub: Submission, draftScope: NonNullable<SubmitEnv["draft"]["draftScope"]>, mutationToken: ChatDraftMutationToken | undefined): Promise<void> {
	const { currentText, message, shouldSteer, settledAttachments, recoveringDelivery, sendNativeImages, restoreNativePayloads } = sub;
	const { onSend, onSteer } = env.props;
	const { composerRevision, contextReferences, editor, hasTextRef, setDeliveryRecoveryNotice, setDeliveryUncertain, setDurableDelivery, setHasText, setSendError, setSteerOutcomeNotice, setSubmitting, setTextDraftPersistenceError, synchronouslyClearedDeliveryRevision, textRef, unsavedText } = env.draft;
	const { acceptAndClearDurableDelivery, clearEditorView } = env.persistence;
	const requestText = recoveringDelivery
		? recoveringDelivery.requestText
		: message;
	const prepared = prepareChatComposerDelivery(draftScope, {
		kind: recoveringDelivery?.kind ?? (shouldSteer ? "steer" : "send"),
		optimistic: !shouldSteer,
		nativeImages: sendNativeImages,
		composerText: currentText,
		attachments: settledAttachments.flatMap((attachment) =>
			attachment.stagedPath
				? [{
						id: attachment.id,
						path: attachment.stagedPath,
						name: attachment.name,
						mimeType: attachment.mimeType,
						bytes: attachment.bytes,
					}]
				: [],
		),
		excerpts: recoveringDelivery?.excerpts ?? contextReferences,
		requestText,
		clientMessageId: recoveringDelivery?.clientMessageId ?? crypto.randomUUID(),
	});
	if (!prepared.ok) {
		setTextDraftPersistenceError(
			"chat.draft.prepareFailed",
		);
		return;
	}
	const delivery = prepared.mutation;
	if (!prepared.recovered && delivery.draft) clearEditorView();
	synchronouslyClearedDeliveryRevision.current = undefined;
	setDeliveryUncertain(false);
	if (!prepared.recovered) {
		composerRevision.current = prepared.draft.composer.revision;
		unsavedText.current = undefined;
	}
	setDurableDelivery(delivery);
	setTextDraftPersistenceError(null);
	setDeliveryRecoveryNotice(
		delivery.state === "accepted"
			? "chat.draft.acceptedMessage"
			: null,
	);
	if (delivery.state === "accepted") {
		acceptAndClearDurableDelivery(delivery);
		return;
	}

	if (!mutationToken) return;
	let mutationFinished = false;
	setSubmitting(true);
	try {
		if (!await restoreNativePayloads()) return;
		if (!isChatComposerMutationCurrent(draftScope, mutationToken)) return;
		if (delivery.kind === "steer") {
			if (!onSteer) throw new Error("Steering is unavailable");
			const outcome = await onSteer(delivery.requestText, prepared.recovered || sub.nativePayloads.length === 0 ? undefined : sub.nativePayloads, delivery.clientMessageId, prepared.recovered);
			if (outcome?.status === "not-accepted") {
				const cleared = clearRejectedChatComposerDelivery(
					draftScope,
					delivery.clientMessageId,
					delivery.revision,
				);
				setDurableDelivery(cleared.draft.composer.delivery);
				composerRevision.current = cleared.draft.composer.revision;
				if (cleared.ok) {
					setTextDraftPersistenceError(null);
					setDeliveryRecoveryNotice(null);
					setSteerOutcomeNotice(outcome.reason);
				} else {
					setTextDraftPersistenceError(
						"chat.draft.clearRefusedSteerFailed",
					);
				}
				return;
			}
		} else {
			const deliveryAttachments =
				sendNativeImages && sub.nativePayloads.length > 0 ? sub.nativePayloads : undefined;
			if (delivery.excerpts?.length) {
				await onSend(
					delivery.requestText,
					deliveryAttachments,
					delivery.clientMessageId,
					undefined,
					delivery.excerpts,
				);
			} else {
				await onSend(delivery.requestText, deliveryAttachments, delivery.clientMessageId);
			}
		}
		acceptAndClearDurableDelivery(delivery, mutationToken);
		mutationFinished = true;
	} catch (error) {
		if (delivery.kind === "send" && !textRef.current) {
			textRef.current = delivery.draft?.text ?? currentText;
			hasTextRef.current = textRef.current.trim().length > 0;
			setHasText(hasTextRef.current);
			editor.current?.setText(textRef.current);
		}
		// Refusal of a retry says nothing about a previous attempt whose response
		// was lost. Only an initial, definitively unaccepted send can be edited.
		if (
			delivery.kind === "send" && !prepared.recovered &&
			DEFINITIVE_SEND_REJECTIONS.has(apiErrorCode(error) ?? "")
		) {
			const cleared = unsavedText.current
				? { ok: false, draft: readChatSessionDraft(draftScope) }
				: clearRejectedChatComposerDelivery(draftScope, delivery.clientMessageId, delivery.revision);
			setDurableDelivery(cleared.draft.composer.delivery);
			if (cleared.ok && !cleared.draft.composer.delivery) {
				composerRevision.current = cleared.draft.composer.revision;
				textRef.current = cleared.draft.composer.text;
				hasTextRef.current = textRef.current.trim().length > 0;
				setHasText(hasTextRef.current);
				editor.current?.setText(textRef.current);
			}
			setDeliveryUncertain(false);
			setDeliveryRecoveryNotice(null);
			setTextDraftPersistenceError(cleared.ok ? null : "chat.draft.saveFailed");
			// A failed agent switch is reported in its own strip above the box.
			setSendError(apiErrorCode(error) === AGENT_SWITCH_FAILED ? null : apiErrorMessage(error));
			return;
		}
		setDeliveryUncertain(true);
		setDeliveryRecoveryNotice(
			delivery.kind === "steer"
				? "chat.draft.steerUncertain"
				: "chat.draft.sendUncertain",
		);
	} finally {
		if (!mutationFinished) cancelChatComposerMutation(draftScope, mutationToken);
		setSubmitting(false);
	}
}
