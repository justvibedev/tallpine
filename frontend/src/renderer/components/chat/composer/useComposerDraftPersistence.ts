import { useCallback, useEffect, useImperativeHandle, useLayoutEffect, useRef } from "react";
import { type ComposerEditorSnapshot } from "../ComposerEditor";
import { acknowledgeChatComposerMutation, cancelChatComposerMutation, clearAcceptedChatComposer, clearUncertainChatComposerDelivery, finishChatComposerMutation, getChatComposerMutation, loadChatSessionDraft, markChatComposerDeliveryAccepted, writeChatComposerText, type ChatDraftMutationToken, type ChatComposerDelivery, type DraftClearResult } from "../../../lib/chat-drafts";
import { setChatDraftBoundary } from "../../../lib/chat-draft-boundary";
import { attachmentFromDraft } from "./composer-model";
import { useComposerDraftState } from "./useComposerDraftState";
import { useComposerSuggestions } from "./useComposerSuggestions";
import { ChatComposerProps } from "./ComposerProps";

/** Queue dock focus, accepted-delivery clearing and draft persistence. */
export function useComposerDraftPersistence(props: ChatComposerProps, draft: ReturnType<typeof useComposerDraftState>, suggestionState: ReturnType<typeof useComposerSuggestions>) {
	const {
		focusRef,
		approval,
		draftSeed,
		editingQueuedTurnId,
		onQueuedDraftChange,
		queuedDock,
		autoFocusKey,
		autoFocus = true,
		draftSessionId,
		acceptedClientMessageIds,
		sentMessagesScope,
	} = props;
	const {
		appliedAcceptanceSequence,
		automaticDeliveryRecoveryAttempted,
		composerMutation,
		composerRevision,
		dismissedKeyRef,
		draftScope,
		durableDelivery,
		editor,
		fileAttachments,
		hadQueuedDockRef,
		hasTextRef,
		highlightedRef,
		historyBrowse,
		optimisticSend,
		previousTrigger,
		restoredSeedKey,
		setAppliedAcceptanceSequence,
		setDeliveryRecoveryNotice,
		setDeliveryUncertain,
		setDismissedKey,
		setDurableDelivery,
		setHasText,
		setHighlighted,
		setSendError,
		setSteerOutcomeNotice,
		setTextDraftPersistenceError,
		setTrigger,
		synchronouslyClearedDeliveryRevision,
		textRef,
		triggerRef,
		unsavedText,
	} = draft;
	const {
		draftSeedId,
		draftSeedText,
		focusEditor,
		hasDraft,
	} = suggestionState;
	useEffect(() => {
		focusEditor();
	}, [autoFocusKey, focusEditor]);

	const hasQueuedDock = Boolean(queuedDock);
	const restoreFocusAfterQueueAppears =
		hasQueuedDock &&
		!hadQueuedDockRef.current &&
		typeof document !== "undefined" &&
		document.activeElement?.getAttribute("aria-label") === "Message the agent";
	useLayoutEffect(() => {
		hadQueuedDockRef.current = hasQueuedDock;
		if (restoreFocusAfterQueueAppears) editor.current?.focus();
	}, [hasQueuedDock, restoreFocusAfterQueueAppears]);

	useEffect(() => {
		if (!autoFocus) return;

		const onWindowFocus = () => focusEditor();
		const onVisibilityChange = () => {
			if (document.visibilityState === "visible") focusEditor();
		};

		window.addEventListener("focus", onWindowFocus);
		document.addEventListener("visibilitychange", onVisibilityChange);
		return () => {
			window.removeEventListener("focus", onWindowFocus);
			document.removeEventListener("visibilitychange", onVisibilityChange);
		};
	}, [autoFocus, focusEditor]);

	const clearEditorView = useCallback(() => {
		historyBrowse.current = null;
		textRef.current = "";
		hasTextRef.current = false;
		setHasText(false);
		setTrigger(undefined);
		triggerRef.current = undefined;
		previousTrigger.current = undefined;
		dismissedKeyRef.current = null;
		setDismissedKey(null);
		highlightedRef.current = 0;
		setHighlighted(0);
		editor.current?.clear();
	}, []);

	const applyAcceptedDraftResult = useCallback(
		(result: DraftClearResult) => {
			setDurableDelivery(result.draft.composer.delivery);
			if (!result.ok) {
				setDeliveryRecoveryNotice(
					"chat.draft.acceptedMessage",
				);
				setTextDraftPersistenceError(
					"chat.draft.clearMessageFailed",
				);
				return false;
			}
			if (!unsavedText.current) composerRevision.current = result.draft.composer.revision;
			setTextDraftPersistenceError(unsavedText.current ? "chat.draft.saveFailed" : null);
			setDeliveryRecoveryNotice(null);
			// A receipt from the old surface must not clear this surface's unsaved draft.
			if (!unsavedText.current && !result.cleared) {
				textRef.current = result.draft.composer.text;
				hasTextRef.current = textRef.current.trim().length > 0;
				setHasText(hasTextRef.current);
				if (editor.current?.getSnapshot().text !== textRef.current) {
					historyBrowse.current = null;
					editor.current?.setText(textRef.current);
				}
			} else if (!unsavedText.current && editor.current?.getSnapshot().text !== "") {
				clearEditorView();
			}
			if (result.draft.composer.attachments.length === 0) {
				fileAttachments.clear();
			} else {
				for (const attachment of fileAttachments.getAttachments()) {
					if (!result.draft.composer.attachments.some((kept) => kept.id === attachment.id)) {
						fileAttachments.remove(attachment.id);
					}
				}
			}
			return result.cleared && !unsavedText.current;
		},
		[clearEditorView, fileAttachments],
	);

	const clearAcceptedDraft = useCallback(
		(acceptedRevision: number, mutationToken?: ChatDraftMutationToken) => {
			if (!draftScope) {
				clearEditorView();
				fileAttachments.clear();
				return true;
			}
			const saved = unsavedText.current
				? writeChatComposerText(draftScope, unsavedText.current.text, undefined, unsavedText.current.revision)
				: undefined;
			if (saved?.ok && saved.draft.composer.text === unsavedText.current?.text) unsavedText.current = undefined;
			const result: DraftClearResult = saved?.ok === false
				? { ok: false, cleared: false, draft: saved.draft }
				: clearAcceptedChatComposer(draftScope, acceptedRevision, undefined,
					fileAttachments.getAttachments().map((attachment) => attachment.id));
			// A successful durable clear can synchronously trigger a replacement
			// surface before React applies the acceptance receipt. Release the route
			// boundary first so cleared UI never exposes stale unsafe-draft state.
			if (result.ok && result.cleared && draftSessionId) {
				synchronouslyClearedDeliveryRevision.current = acceptedRevision;
				setChatDraftBoundary(draftSessionId, "composer", undefined);
			}
			if (mutationToken) {
				finishChatComposerMutation(
					draftScope,
					mutationToken,
					acceptedRevision,
					result,
				);
				return result.ok && result.cleared;
			}
			return applyAcceptedDraftResult(result);
		},
		[
			applyAcceptedDraftResult,
			clearEditorView,
			draftScope,
			draftSessionId,
			fileAttachments,
		],
	);

	const acceptAndClearDurableDelivery = useCallback(
		(delivery: ChatComposerDelivery, mutationToken?: ChatDraftMutationToken) => {
			if (!draftScope) return clearAcceptedDraft(delivery.revision, mutationToken);
			const accepted = markChatComposerDeliveryAccepted(
				draftScope,
				delivery.clientMessageId,
				delivery.revision,
			);
			if (!accepted.ok) {
				if (mutationToken) cancelChatComposerMutation(draftScope, mutationToken);
				setDurableDelivery(delivery);
				setTextDraftPersistenceError(
					"chat.draft.recordMessageFailed",
				);
				return false;
			}
			return clearAcceptedDraft(delivery.revision, mutationToken);
		},
		[clearAcceptedDraft, draftScope],
	);

	const abandonUncertainSteer = useCallback(() => {
		if (!draftScope || !durableDelivery || durableDelivery.kind !== "steer") return;
		const result = clearUncertainChatComposerDelivery(
			draftScope,
			durableDelivery.clientMessageId,
			durableDelivery.revision,
		);
		setDurableDelivery(result.draft.composer.delivery);
		if (!result.ok) {
			setTextDraftPersistenceError(
				"chat.draft.abandonFailed",
			);
			return;
		}
		setDeliveryUncertain(false);
		setTextDraftPersistenceError(null);
		setDeliveryRecoveryNotice(null);
		setSteerOutcomeNotice(
			"chat.draft.abandonedSteer",
		);
	}, [draftScope, durableDelivery]);

	useEffect(() => {
		const accepted = composerMutation.accepted;
		if (!accepted || accepted.sequence <= appliedAcceptanceSequence) return;
		applyAcceptedDraftResult(accepted.result);
		setAppliedAcceptanceSequence(accepted.sequence);
		if (draftScope) acknowledgeChatComposerMutation(draftScope, accepted.sequence);
	}, [
		appliedAcceptanceSequence,
		applyAcceptedDraftResult,
		composerMutation.accepted,
		draftScope,
	]);

	useEffect(() => {
		// A concurrent replacement can render this memoized seed, remain disconnected,
		// and commit only after another surface accepted and cleared the draft. Read the
		// session record again at the effect/commit boundary so acknowledgement cannot
		// turn the runtime snapshot into an ABA that resurrects accepted text or staged
		// attachment descriptors.
		if (unsavedText.current || (composerMutation.pending && restoredSeedKey.current !== undefined)) return;
		const loadedDraft = draftScope ? loadChatSessionDraft(draftScope) : undefined;
		if (loadedDraft?.ok === false) return;
		const committedDraft = loadedDraft?.draft;
		// Keep this surface's native bytes for retry; only a new surface needs descriptors.
		if (committedDraft && (!committedDraft.composer.delivery?.draft || restoredSeedKey.current === undefined)) {
			fileAttachments.reconcilePersistedAttachments(
				(committedDraft.composer.delivery?.draft?.attachments ?? committedDraft.composer.attachments).map(attachmentFromDraft),
			);
		}
		const committedSeedText =
			draftSeed?.text ??
			(committedDraft
				? committedDraft.composer.text || (!getChatComposerMutation(draftScope ?? "").pending
					? committedDraft.composer.delivery?.draft?.text ?? ""
					: "")
				: draftSeedText);
		if (committedSeedText === undefined) {
			restoredSeedKey.current = undefined;
			return;
		}
		const seedKey = editingQueuedTurnId ? draftSeedId : JSON.stringify([draftSeedId, committedSeedText]);
		if (restoredSeedKey.current === seedKey) return;
		restoredSeedKey.current = seedKey;
		textRef.current = committedSeedText;
		hasTextRef.current = committedSeedText.trim().length > 0;
		setHasText(hasTextRef.current);
		if (editor.current?.getSnapshot().text !== committedSeedText) {
			historyBrowse.current = null;
			editor.current?.setText(committedSeedText);
			dismissedKeyRef.current = null;
			setDismissedKey(null);
			highlightedRef.current = 0;
			setHighlighted(0);
		}
		if (draftSeed) {
			setSendError(null);
			setSteerOutcomeNotice(null);
		}
		// A history action intentionally creates a new draft and must be persisted.
		// A session restore is already durable; writing it again here needlessly
		// changes the accepted-send revision during mount.
		if (draftScope && draftSeed) {
			const result = writeChatComposerText(draftScope, committedSeedText);
			if (result.ok) composerRevision.current = result.draft.composer.revision;
			setTextDraftPersistenceError(
				result.ok
					? null
					: "chat.draft.saveFailed",
			);
		}
	}, [
		composerMutation.pending,
		draftScope,
		draftSeed,
		draftSeedId,
		draftSeedText,
		editingQueuedTurnId,
		fileAttachments.reconcilePersistedAttachments,
	]);

	useEffect(() => {
		if (!durableDelivery || !draftScope) return;
		// The live send owns completion until its receipt has been applied.
		if (composerMutation.pending || composerMutation.accepted) return;
		// A replacement can commit after another surface cleared its rendered seed.
		const current = loadChatSessionDraft(draftScope);
		// A failed read cannot prove that the delivery was cleared.
		const delivery = current.ok ? current.draft.composer.delivery : durableDelivery;
		if (!delivery || delivery.clientMessageId !== durableDelivery.clientMessageId) {
			setDurableDelivery(delivery);
			return;
		}
		// The chat already holds this message (or steer): it arrived though its answer was lost.
		const observed = acceptedClientMessageIds?.has(delivery.clientMessageId);
		if (delivery.state !== "accepted" && !observed) return;
		if (automaticDeliveryRecoveryAttempted.current === delivery.clientMessageId) return;
		automaticDeliveryRecoveryAttempted.current = delivery.clientMessageId;
		acceptAndClearDurableDelivery(delivery);
	}, [
		acceptAndClearDurableDelivery,
		acceptedClientMessageIds,
		composerMutation.pending,
		composerMutation.accepted,
		draftScope,
		durableDelivery,
	]);

	const approvalActive = Boolean(approval);
	const previousApprovalActive = useRef(approvalActive);
	useEffect(() => {
		const wasActive = previousApprovalActive.current;
		previousApprovalActive.current = approvalActive;
		if (!wasActive || approvalActive) return;
		editor.current?.setText(textRef.current);
	}, [approvalActive]);

	const previousEditingQueuedTurnIdRef = useRef(editingQueuedTurnId);
	useEffect(() => {
		const previous = previousEditingQueuedTurnIdRef.current;
		previousEditingQueuedTurnIdRef.current = editingQueuedTurnId;
		if (previous && !editingQueuedTurnId) {
			clearEditorView();
		}
	}, [clearEditorView, editingQueuedTurnId]);

	const persistComposerText = useCallback((text: string) => {
		if (!draftScope) return;
		const result = writeChatComposerText(draftScope, text);
		if (result.ok) composerRevision.current = result.draft.composer.revision;
		unsavedText.current = result.ok ? undefined : { text, revision: composerRevision.current };
		// A disabled Lexical editor can still publish an internal state update
		// while its editability changes. It must not erase the recovery notice
		// for a durable delivery that still owns this exact composer revision.
		if (!result.ok || !result.draft.composer.delivery) {
			setTextDraftPersistenceError(
				result.ok
					? null
					: "chat.draft.saveFailed",
			);
		}
	}, [draftScope]);

	const onEditorChange = useCallback((snapshot: ComposerEditorSnapshot) => {
		// Changing Lexical's editability can publish the optimistic visual clear.
		if (optimisticSend && !snapshot.text && !textRef.current) return;
		// Editing a recalled message makes it the draft; until then the saved draft stands.
		if (historyBrowse.current && snapshot.text !== historyBrowse.current.shown) historyBrowse.current = null;
		const previousText = textRef.current;
		textRef.current = snapshot.text;
		onQueuedDraftChange?.(snapshot.text);
		if (snapshot.text !== previousText && !historyBrowse.current) persistComposerText(snapshot.text);
		if (hasTextRef.current !== snapshot.hasText) {
			hasTextRef.current = snapshot.hasText;
			setHasText(snapshot.hasText);
		}

		const previous = previousTrigger.current;
		const next = snapshot.trigger;
		triggerRef.current = next;
		if (
			previous?.key !== next?.key ||
			previous?.end !== next?.end ||
			previous?.query !== next?.query ||
			previous?.kind !== next?.kind
		) {
			highlightedRef.current = 0;
			setHighlighted(0);
			previousTrigger.current = next;
			setTrigger(next);
		}
		if (dismissedKeyRef.current && next?.key !== dismissedKeyRef.current) {
			dismissedKeyRef.current = null;
			setDismissedKey(null);
		}
	}, [onQueuedDraftChange, optimisticSend, persistComposerText]);
	// A walk holds one branch's prompts. Once another branch or conversation is
	// active they are abandoned history, so put the user's own draft back.
	const walkedScope = useRef(sentMessagesScope);
	useEffect(() => {
		if (walkedScope.current === sentMessagesScope) return;
		walkedScope.current = sentMessagesScope;
		const browse = historyBrowse.current;
		if (!browse) return;
		historyBrowse.current = null;
		editor.current?.setText(browse.draft, "start");
		const snapshot = editor.current?.getSnapshot();
		if (snapshot) onEditorChange(snapshot);
	}, [onEditorChange, sentMessagesScope]);
	useImperativeHandle(
		focusRef,
		() => ({
			focus: focusEditor,
			isEmpty: () => !hasDraft,
			setDraft: (text: string) => {
				editor.current?.setText(text);
				const snapshot = editor.current?.getSnapshot();
				if (snapshot) onEditorChange(snapshot);
				editor.current?.focus();
			},
			attachFiles: (files: File[]) => draft.attachFilesRef.current(files),
		}),
		[focusEditor, hasDraft, onEditorChange],
	);

	const pick = useCallback((value: string) => {
		const currentTrigger = triggerRef.current;
		if (!currentTrigger) return;
		editor.current?.insertToken(currentTrigger, value);
		triggerRef.current = undefined;
		previousTrigger.current = undefined;
		setTrigger(undefined);
		highlightedRef.current = 0;
		setHighlighted(0);
		dismissedKeyRef.current = null;
		setDismissedKey(null);
	}, []);

	return {
		abandonUncertainSteer,
		acceptAndClearDurableDelivery,
		clearAcceptedDraft,
		clearEditorView,
		onEditorChange,
		persistComposerText,
		pick,
	};
}
