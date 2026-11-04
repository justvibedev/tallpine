import { useChatDraftTranslation } from "../../../lib/chat-draft-messages";
import { useCallback, useEffect, useId, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { type ComposerEditorHandle, type ComposerTrigger } from "../ComposerEditor";
import { useFileAttachments, type FileAttachment } from "../../../hooks/useFileAttachments";
import { chatDraftScopeKey, chatQueuedAttachmentScopeKey, getChatComposerMutation, readChatSessionDraft, subscribeChatDraftRuntime, writeChatAttachments, type ChatComposerDelivery, type ChatDraftScope } from "../../../lib/chat-drafts";
import { subscribeChatComposerReferences } from "../../../lib/chat-context-bus";
import { ChatComposerProps } from "./ComposerProps";
import { StoredComposerAttachment, restoredDeliveryNotice } from "./composer-model";

/** Draft scope, editor refs and persisted draft state for the ChatComposer. */
export function useComposerDraftState(props: ChatComposerProps) {
	const {
		onStageAttachments,
		draftSeed,
		editingQueuedTurnId,
		queuedDraftScope,
		onQueuedAttachmentsChange,
		queuedEditRecovery,
		queuedDock,
		draftSessionId,
		draftSessionIncarnation,
	} = props;
	const translateDraft = useChatDraftTranslation();
	const draftScope = useMemo<ChatDraftScope | undefined>(
		() =>
			draftSessionId
				? {
						sessionId: draftSessionId,
						incarnation: draftSessionIncarnation ?? draftSessionId,
					}
				: undefined,
		[draftSessionId, draftSessionIncarnation],
	);
	const draftScopeKey = draftScope ? chatDraftScopeKey(draftScope) : undefined;
	const attachmentScopeKey = queuedDraftScope
		? chatQueuedAttachmentScopeKey(queuedDraftScope, draftSeed?.id ?? "undefined")
		: draftScopeKey;
	const boundarySessionId = draftSessionId ?? queuedDraftScope?.sessionId;
	const [retainedAttachments, setRetainedAttachments] = useState<StoredComposerAttachment[]>(draftSeed?.attachments ?? []);
	const visibleRetainedAttachments = editingQueuedTurnId ? retainedAttachments : [];
	const [hasText, setHasText] = useState(false);
	const hasTextRef = useRef(false);
	const [trigger, setTrigger] = useState<ComposerTrigger>();
	/**
	 * The trigger position the user dismissed with Escape. Held so the menu stays
	 * shut for the completion they rejected, while a new `/` or `@` still opens one.
	 */
	const [dismissedKey, setDismissedKey] = useState<string | null>(null);
	const dismissedKeyRef = useRef<string | null>(null);
	const [highlighted, setHighlighted] = useState(0);
	const highlightedRef = useRef(0);
	const [isComposing, setIsComposing] = useState(false);
	const [dragging, setDragging] = useState(false);
	const [sendError, setSendError] = useState<string | null>(null);
	const [steerOutcomeNotice, setSteerOutcomeNotice] = useState<string | null>(null);
	const [deliveryRecoveryNotice, setDeliveryRecoveryNotice] = useState<string | null>(null);
	const [deliveryUncertain, setDeliveryUncertain] = useState(
		() =>
			draftScope !== undefined &&
			readChatSessionDraft(draftScope).composer.delivery?.state === "dispatching",
	);
	const [textDraftPersistenceError, setTextDraftPersistenceError] = useState<string | null>(null);
	const [attachmentDraftPersistenceError, setAttachmentDraftPersistenceError] = useState<
		string | null
	>(null);
	const [submitting, setSubmitting] = useState(false);
	const [durableDelivery, setDurableDelivery] = useState<ChatComposerDelivery | undefined>(() =>
		draftScope
			? readChatSessionDraft(draftScope).composer.delivery
			: undefined,
	);
	const [steerNextRequest, setSteerNextRequest] = useState(0);
	// Cmd/Ctrl held anywhere in the window turns a send-button click into a steer.
	const [modifierHeld, setModifierHeld] = useState(false);
	// The DOM event is the source of truth while React catches up with the draft
	// transition. This keeps Enter-after-fast-typing from observing stale state.
	const textRef = useRef("");
	/**
	 * What Enter does while the agent is working.
	 *
	 * Queueing is the safe default and matches `tallpine send`: the daemon records the
	 * message durably and dispatches it when the current turn finishes. Steering is
	 * timing-sensitive and changes the running turn, so it stays an explicit choice.
	 */

	const editor = useRef<ComposerEditorHandle>(null);
	const filePicker = useRef<HTMLInputElement>(null);
	const addMenuTrigger = useRef<HTMLButtonElement>(null);
	const [addMenuBox, setAddMenuBox] = useState<{ lift: number; shift: number; width: number }>();
	const submitInFlight = useRef<Promise<void> | null>(null);
	// Disabling the active editor can move focus to the document body. Remember
	// keyboard-origin submissions so focus can return once the editor is enabled.
	const restoreFocusAfterSubmission = useRef(false);
	const menuId = useId();
	const hadQueuedDockRef = useRef(Boolean(queuedDock));
	const previousTrigger = useRef<ComposerTrigger | undefined>(undefined);
	const triggerRef = useRef<ComposerTrigger | undefined>(undefined);
	const automaticDeliveryRecoveryAttempted = useRef<string | undefined>(undefined);
	/**
	 * Up/Down recall of sent messages. The history is captured newest first when
	 * browsing starts, so a message arriving meanwhile cannot shift the position, and
	 * `draft` is what the editor held before, for Down to bring back. A recalled
	 * message is never saved as the draft until it is edited: leaving mid-browse keeps
	 * the user's own text.
	 */
	const historyBrowse = useRef<{ entries: string[]; index: number; draft: string; shown: string } | null>(null);
	// Code selected in a file or diff view ("Ask in chat") arrives here as a
	// reference chip. Only the session's ordinary prompt accepts it; a queued-turn
	// edit has no draftSessionId and stays out of the way.
	useEffect(() => {
		if (!draftSessionId) return;
		return subscribeChatComposerReferences(draftSessionId, (reference) => {
			editor.current?.insertReference(reference.path, reference.display, reference.wire);
			// The Chat surface may only now be coming forward from behind a file tab.
			window.requestAnimationFrame(() => window.requestAnimationFrame(() => editor.current?.focus()));
		});
	}, [draftSessionId]);
	const restoredSeedKey = useRef<string | undefined>(undefined);
	const restoredSessionId = useRef<string | undefined>(undefined);
	const persistedDraft = useMemo(
		() => (draftScope ? readChatSessionDraft(draftScope) : undefined),
		[draftScope],
	);
	const subscribeComposerMutation = useCallback(
		(listener: () => void) => subscribeChatDraftRuntime(draftScope ?? "", listener),
		[draftScope],
	);
	const getComposerMutation = useCallback(
		() => getChatComposerMutation(draftScope ?? ""),
		[draftScope],
	);
	const composerMutation = useSyncExternalStore(
		subscribeComposerMutation,
		getComposerMutation,
		getComposerMutation,
	);
	const optimisticSend = Boolean(durableDelivery?.draft && composerMutation.pending);
	const contextReferences = draftScope
		? readChatSessionDraft(draftScope).composer.excerpts ?? []
		: [];
	const [appliedAcceptanceSequence, setAppliedAcceptanceSequence] = useState(0);
	const composerRevision = useRef(persistedDraft?.composer.revision ?? 0);
	const unsavedText = useRef<{ text: string; revision: number } | undefined>(undefined);
	const synchronouslyClearedDeliveryRevision = useRef<number | undefined>(undefined);
	const restoredAttachments = useMemo<FileAttachment[]>(
		() =>
			(persistedDraft?.composer.delivery?.draft?.attachments ?? persistedDraft?.composer.attachments ?? draftSeed?.stagedAttachments)?.map((attachment) => ({
				id: attachment.id,
				name: attachment.name,
				mimeType: attachment.mimeType,
				bytes: attachment.bytes,
				stagedPath: attachment.path,
			})) ?? [],
		[persistedDraft, draftSeed?.stagedAttachments],
	);
	const persistAttachments = useCallback(
		(attachments: FileAttachment[]) => {
			const descriptors = attachments.flatMap((attachment) => attachment.stagedPath
				? [{ id: attachment.id, path: attachment.stagedPath, name: attachment.name, mimeType: attachment.mimeType, bytes: attachment.bytes }]
				: []);
			if (!draftScope) {
				onQueuedAttachmentsChange?.(descriptors);
				return;
			}
			const sentAttachments = readChatSessionDraft(draftScope).composer.delivery?.draft?.attachments;
			const result = writeChatAttachments(draftScope, descriptors.filter((attachment) =>
				!sentAttachments?.some((sent) => sent.id === attachment.id)));
			if (result.ok) composerRevision.current = result.draft.composer.revision;
			setAttachmentDraftPersistenceError(
				result.ok
					? null
					: "chat.draft.saveFailed",
			);
		},
		[draftScope, onQueuedAttachmentsChange],
	);
	const prepareAttachments = useCallback(
		async (attachments: FileAttachment[]): Promise<FileAttachment[]> => {
			if (!onStageAttachments) throw new Error("Attachment staging is unavailable");
			const paths = await onStageAttachments(
				attachments.flatMap(({ mimeType, data }) =>
					data ? [{ mimeType, data }] : [],
				),
			);
			if (paths.length !== attachments.length) {
				throw new Error("Attachment staging returned an incomplete result");
			}
			return attachments.map((attachment, index) => ({
				...attachment,
				stagedPath: paths[index],
			}));
		},
		[onStageAttachments],
	);
	useEffect(() => {
		if (restoredSessionId.current === draftScopeKey) return;
		restoredSessionId.current = draftScopeKey;
		historyBrowse.current = null;
		synchronouslyClearedDeliveryRevision.current = undefined;
		const currentDraft = draftScope ? readChatSessionDraft(draftScope) : undefined;
		composerRevision.current = currentDraft?.composer.revision ?? 0;
		unsavedText.current = undefined;
		setDurableDelivery(currentDraft?.composer.delivery);
		setAppliedAcceptanceSequence(0);
		setSendError(null);
		setSteerOutcomeNotice(null);
		setTextDraftPersistenceError(null);
		setDeliveryRecoveryNotice(restoredDeliveryNotice(currentDraft?.composer.delivery));
		setDeliveryUncertain(currentDraft?.composer.delivery?.state === "dispatching");
		setAttachmentDraftPersistenceError(null);
		automaticDeliveryRecoveryAttempted.current = undefined;
	}, [draftScope, draftScopeKey]);
	const fileAttachments = useFileAttachments({
		initialAttachments: restoredAttachments,
		initialKey: attachmentScopeKey,
		prepareAttachments: onStageAttachments ? prepareAttachments : undefined,
		onAttachmentsChange: persistAttachments,
	});
	const canAttach = Boolean(onStageAttachments) && !queuedEditRecovery;

	return {
		addMenuBox,
		addMenuTrigger,
		appliedAcceptanceSequence,
		attachmentDraftPersistenceError,
		automaticDeliveryRecoveryAttempted,
		boundarySessionId,
		canAttach,
		composerMutation,
		composerRevision,
		contextReferences,
		deliveryRecoveryNotice,
		deliveryUncertain,
		dismissedKey,
		dismissedKeyRef,
		draftScope,
		draftScopeKey,
		dragging,
		durableDelivery,
		editor,
		fileAttachments,
		filePicker,
		hadQueuedDockRef,
		hasText,
		hasTextRef,
		highlighted,
		highlightedRef,
		historyBrowse,
		isComposing,
		menuId,
		modifierHeld,
		optimisticSend,
		persistedDraft,
		previousTrigger,
		restoreFocusAfterSubmission,
		restoredSeedKey,
		retainedAttachments,
		sendError,
		setAddMenuBox,
		setAppliedAcceptanceSequence,
		setDeliveryRecoveryNotice,
		setDeliveryUncertain,
		setDismissedKey,
		setDragging,
		setDurableDelivery,
		setHasText,
		setHighlighted,
		setIsComposing,
		setModifierHeld,
		setRetainedAttachments,
		setSendError,
		setSteerNextRequest,
		setSteerOutcomeNotice,
		setSubmitting,
		setTextDraftPersistenceError,
		setTrigger,
		steerNextRequest,
		steerOutcomeNotice,
		submitInFlight,
		submitting,
		synchronouslyClearedDeliveryRevision,
		textDraftPersistenceError,
		textRef,
		translateDraft,
		trigger,
		triggerRef,
		unsavedText,
		visibleRetainedAttachments,
	};
}
